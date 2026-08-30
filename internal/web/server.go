package web

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/armory"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/downloads"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/ratelimit"
	"github.com/zeroward/waygate/internal/session"
	"github.com/zeroward/waygate/internal/status"
	"github.com/zeroward/waygate/internal/wow"
)

type Server struct {
	cfg       config.Config
	log       *slog.Logger
	tpl       *template.Template
	sessions  *session.Store
	accounts  *account.Service
	status    *status.Cache
	captcha   *captcha.Verifier
	mail      *mail.Sender
	regRL     *ratelimit.Limiter
	loginRL   *ratelimit.Limiter
	contactRL *ratelimit.Limiter
	resetRL   *ratelimit.Limiter
	kbRL      *ratelimit.Limiter
	unstuckRL *ratelimit.Limiter
	ticketRL  *ratelimit.Limiter
	kb        *kb.Store
	id        *identity.Service
	downloads *downloads.Store
	armory    *armory.Service
}

func New(
	cfg config.Config,
	log *slog.Logger,
	accounts *account.Service,
	st *status.Cache,
	cap *captcha.Verifier,
	mailer *mail.Sender,
) (*Server, error) {
	funcs := template.FuncMap{
		"playtime":  wow.Playtime,
		"classSlug": func(id uint8) string { return wow.ClassSlug(id) },
		"expansion": wow.ExpansionName,
		"eq":        func(a, b string) bool { return a == b },
		"initial": func(s string) string {
			r, _ := utf8.DecodeRuneInString(strings.TrimSpace(s))
			if r == 0 {
				return "?"
			}
			return strings.ToUpper(string(r))
		},
		"rankName": account.RankName,
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(embedded, "templates/*.html")
	if err != nil {
		return nil, err
	}
	kbMax := cfg.RateKB
	if kbMax < 1 {
		kbMax = 20
	}
	unstuckMax := cfg.RateUnstuck
	if unstuckMax < 1 {
		unstuckMax = 5
	}
	ticketMax := cfg.RateTickets
	if ticketMax < 1 {
		ticketMax = 5
	}
	kbStore, err := kb.Open(cfg.KBPath)
	if err != nil {
		return nil, err
	}
	idStore, err := identity.NewStore(kbStore.SQL())
	if err != nil {
		_ = kbStore.Close()
		return nil, err
	}
	idSvc := identity.New(idStore, accounts, cfg.WowCredentialsMax)
	if !cfg.DemoMode {
		if err := idSvc.MigrateFromAC(context.Background(), log); err != nil {
			log.Error("identity migrate", "err", err)
		}
	}
	s := &Server{
		cfg:       cfg,
		log:       log,
		tpl:       tpl,
		sessions:  session.NewStore(cfg.SessionTTL, cfg.SessionSecure),
		accounts:  accounts,
		status:    st,
		captcha:   cap,
		mail:      mailer,
		regRL:     ratelimit.New(cfg.RateWindow, cfg.RateRegister),
		loginRL:   ratelimit.New(cfg.RateWindow, cfg.RateLogin),
		contactRL: ratelimit.New(cfg.RateWindow, cfg.RateContact),
		resetRL:   ratelimit.New(cfg.RateWindow, cfg.RateReset),
		kbRL:      ratelimit.New(cfg.RateWindow, kbMax),
		unstuckRL: ratelimit.New(cfg.RateWindow, unstuckMax),
		ticketRL:  ratelimit.New(cfg.RateWindow, ticketMax),
		kb:        kbStore,
		id:        idSvc,
		downloads: downloads.New(cfg.DownloadsDir, cfg.DownloadsCatalog),
		armory:    armory.New(cfg, st.Database(), log),
	}
	if err := s.seedHowToConnect(); err != nil {
		_ = kbStore.Close()
		return nil, err
	}
	s.downloads.SetScanMax(cfg.ClamAVScanMaxBytes())
	// ClamAV is installed but scanning is off until the upload/scan path is finished.
	if cfg.ClamAVAddr != "" {
		s.log.Info("clamav scanning disabled", "addr", cfg.ClamAVAddr)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /register", s.registerGET)
	mux.HandleFunc("POST /register", s.registerPOST)
	mux.HandleFunc("GET /connect", s.connectRedirect)
	mux.HandleFunc("GET /connect/", s.connectRedirect)
	mux.HandleFunc("GET /kb", s.kbIndex)
	mux.HandleFunc("GET /kb/{slug}", s.kbArticle)
	mux.HandleFunc("GET /realmlist.wtf", s.realmlistFile)
	mux.HandleFunc("GET /downloads", s.downloadsPage)
	mux.HandleFunc("GET /downloads/{id}", s.downloadsFile)
	mux.HandleFunc("GET /online", s.online)
	mux.HandleFunc("GET /leaderboards", s.leaderboards)
	mux.HandleFunc("GET /armory", s.armorySearch)
	mux.HandleFunc("GET /armory/{name}", s.armoryInspect)
	mux.HandleFunc("GET /account", s.accountGET)
	mux.HandleFunc("POST /account/login", s.loginPOST)
	mux.HandleFunc("POST /account/login/totp", s.totpLoginPOST)
	mux.HandleFunc("POST /account/totp/start", s.totpStartPOST)
	mux.HandleFunc("POST /account/totp/confirm", s.totpConfirmPOST)
	mux.HandleFunc("POST /account/totp/disable", s.totpDisablePOST)
	mux.HandleFunc("POST /account/logout", s.logoutPOST)
	mux.HandleFunc("POST /account/password", s.passwordPOST)
	mux.HandleFunc("POST /account/unstuck", s.unstuckPOST)
	mux.HandleFunc("POST /account/wow", s.wowCredentialPOST)
	mux.HandleFunc("GET /tickets", s.ticketsList)
	mux.HandleFunc("GET /tickets/new", s.ticketsNew)
	mux.HandleFunc("POST /tickets", s.ticketsCreate)
	mux.HandleFunc("GET /tickets/{id}", s.ticketsView)
	mux.HandleFunc("POST /tickets/{id}/comment", s.ticketsComment)
	mux.HandleFunc("GET /staff/tickets", s.staffTickets)
	mux.HandleFunc("GET /staff/tickets/{id}", s.staffTicketView)
	mux.HandleFunc("POST /staff/tickets/{id}", s.staffTicketUpdate)
	mux.HandleFunc("GET /staff", s.staffGET)
	mux.HandleFunc("POST /staff/create", s.staffCreatePOST)
	mux.HandleFunc("POST /staff/reset", s.staffResetPOST)
	mux.HandleFunc("POST /staff/rank", s.staffRankPOST)
	mux.HandleFunc("POST /staff/ban", s.staffBanPOST)
	mux.HandleFunc("POST /staff/unban", s.staffUnbanPOST)
	mux.HandleFunc("POST /staff/downloads", s.staffDownloadPOST)
	mux.HandleFunc("POST /staff/downloads/delete", s.staffDownloadDeletePOST)
	mux.HandleFunc("GET /staff/kb", s.staffKB)
	mux.HandleFunc("GET /staff/kb/new", s.staffKBNew)
	mux.HandleFunc("POST /staff/kb", s.staffKBCreate)
	mux.HandleFunc("POST /staff/kb/preview", s.staffKBPreview)
	mux.HandleFunc("GET /staff/kb/{id}", s.staffKBEdit)
	mux.HandleFunc("POST /staff/kb/{id}", s.staffKBUpdate)
	mux.HandleFunc("POST /staff/kb/{id}/delete", s.staffKBDelete)
	mux.HandleFunc("GET /account/verify/{token}", s.verifyGET)
	mux.HandleFunc("GET /account/reset", s.resetGET)
	mux.HandleFunc("POST /account/reset", s.resetPOST)
	mux.HandleFunc("GET /account/reset/{token}", s.resetConfirmGET)
	mux.HandleFunc("POST /account/reset/{token}", s.resetConfirmPOST)
	mux.HandleFunc("GET /contact", s.contactGET)
	mux.HandleFunc("POST /contact", s.contactPOST)

	static, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(fileServer)))

	return s.middleware(mux)
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) seedHowToConnect() error {
	body := stripLeadingH1(loadConnect(s.cfg))
	if strings.TrimSpace(body) == "" {
		body = "This realm runs **AzerothCore WotLK 3.3.5a** (client build **12340**).\n\nDownload the client from the [Downloads](/downloads) tab.\n"
	}
	return s.kb.SeedIfMissing(context.Background(), kb.Article{
		Slug:         "how-to-connect",
		Title:        "How to connect",
		BodyMarkdown: body,
		Summary:      "Get a 3.3.5a client, set the realmlist, and start Wow.exe.",
		Category:     "Getting started",
		SortOrder:    0,
		Published:    true,
		CreatedBy:    "system",
		UpdatedBy:    "system",
	})
}

func loadConnect(cfg config.Config) string {
	raw := ""
	if cfg.HowToConnectFile != "" {
		if b, err := os.ReadFile(cfg.HowToConnectFile); err == nil {
			raw = string(b)
		}
	}
	if raw == "" {
		if b, err := embedded.ReadFile("content/how-to-connect.md"); err == nil {
			raw = string(b)
		}
	}
	r := strings.NewReplacer(
		"{{PUBLIC_HOST}}", cfg.PublicHost,
		"{{PUBLIC_AUTH_PORT}}", strconv.Itoa(cfg.PublicAuthPort),
		"{{PUBLIC_WORLD_PORT}}", strconv.Itoa(cfg.PublicWorldPort),
		"{{REALM_NAME}}", cfg.RealmName,
		"{{CORE_NAME}}", cfg.CoreName,
	)
	return r.Replace(raw)
}

type page struct {
	Title        string
	Active       string
	CSRF         string
	Flash        *session.Flash
	User         *session.User
	Staff        bool
	Demo         bool
	RealmName    string
	CoreName     string
	CanEditKB    bool
	SMTP         bool
	DiscordURL   string
	ContactEmail string
	CaptchaProv  string
	CaptchaKey   string
	PublicHost   string
	PublicAuth   int
	PublicWorld  int
	Year         int
	Next         string
	Data         any
}

func (s *Server) view(w http.ResponseWriter, r *http.Request, name, title, active string, data any) {
	sess := s.sessions.GetOrCreate(w, r)
	p := page{
		Title:        title,
		Active:       active,
		CSRF:         sess.CSRF,
		Flash:        sess.TakeFlash(),
		User:         sess.User,
		Staff:        sess.User.IsStaff(s.cfg.GMMinLevel),
		CanEditKB:    s.canEditKB(sess.User),
		Demo:         s.cfg.DemoMode,
		RealmName:    s.cfg.RealmName,
		CoreName:     s.cfg.CoreName,
		SMTP:         s.cfg.SMTPConfigured(),
		DiscordURL:   s.cfg.DiscordURL,
		ContactEmail: s.cfg.ContactEmail,
		CaptchaProv:  s.captcha.Provider(),
		CaptchaKey:   s.captcha.SiteKey(),
		PublicHost:   s.cfg.PublicHost,
		PublicAuth:   s.cfg.PublicAuthPort,
		PublicWorld:  s.cfg.PublicWorldPort,
		Year:         time.Now().Year(),
		Next:         safeNext(r.URL.Query().Get("next")),
		Data:         data,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, p); err != nil {
		s.log.Error("template", "err", err, "name", name)
		http.Error(w, "The Frozen Throne is silent. Try again shortly.", http.StatusInternalServerError)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	sess := s.sessions.GetOrCreate(w, r)
	if !sess.ValidCSRF(r.FormValue("csrf_token")) {
		http.Error(w, "Invalid request token. Reload the page and try again.", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Request too large or malformed.", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) ip(r *http.Request) string {
	return clientIP(r, s.cfg.TrustProxy)
}
