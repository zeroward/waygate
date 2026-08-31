package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/downloads"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/session"
	"github.com/zeroward/waygate/internal/status"
	"github.com/zeroward/waygate/internal/validate"
	"github.com/zeroward/waygate/internal/wg"
)

type homeView struct {
	status.Snapshot
	LatestKB  *kb.Article
	WGEnabled bool
	WGUp      bool
	WGPort    int
	Calendar  []kb.RealmEvent
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	hv := homeView{
		Snapshot:  s.status.Get(r.Context()),
		WGEnabled: s.cfg.WGEnabled,
		WGUp:      s.wireGuardUp(r.Context()),
		WGPort:    s.cfg.WGPort,
		Calendar:  s.upcomingCalendar(r.Context()),
	}
	if hv.WGPort <= 0 {
		hv.WGPort = 51820
	}
	if s.kb != nil {
		art, err := s.kb.LatestPublished(r.Context())
		if err != nil {
			s.log.Error("home kb", "err", err)
		} else {
			hv.LatestKB = art
		}
	}
	s.view(w, r, "home.html", "Home", "home", hv)
}

func (s *Server) wireGuardUp(ctx context.Context) bool {
	if !s.cfg.WGEnabled {
		return false
	}
	if s.cfg.DemoMode {
		return true
	}
	s.wgLiveMu.Lock()
	defer s.wgLiveMu.Unlock()
	ttl := s.cfg.StatusCache
	if ttl <= 0 {
		ttl = 20 * time.Second
	}
	if !s.wgLiveAt.IsZero() && time.Since(s.wgLiveAt) < ttl {
		return s.wgLive
	}
	pctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	up := wg.Probe(pctx, s.cfg.WGAgentListen, s.cfg.WGDir)
	s.wgLive = up
	s.wgLiveAt = time.Now()
	return up
}

func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User != nil {
		return sess
	}
	next := r.URL.RequestURI()
	s.flashRedirect(w, r, "/account?next="+url.QueryEscape(next), "info", "Log in to continue.")
	return nil
}

func safeNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.Contains(raw, "://") {
		return "/account"
	}
	return raw
}

func (s *Server) downloadsPage(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	tab := r.URL.Query().Get("tab")
	switch tab {
	case downloads.CatClient, downloads.CatPatches, downloads.CatMods:
	default:
		tab = "all"
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 64 {
		q = q[:64]
	}
	s.view(w, r, "downloads.html", "Downloads", "downloads", map[string]any{
		"Tab":    tab,
		"Query":  q,
		"Intro":  s.downloads.Intro(),
		"Items":  s.downloads.Search(tab, q),
		"Counts": s.downloads.Counts(),
	})
}

func (s *Server) downloadsFile(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if s.dlRL != nil && !s.dlRL.Allow(s.ip(r)+":"+sess.User.Username) {
		http.Error(w, "Too many downloads. Wait and try again.", http.StatusTooManyRequests)
		return
	}
	id := r.PathValue("id")
	item, ok := s.downloads.Get(id)
	if !ok || !item.Ready {
		http.NotFound(w, r)
		return
	}
	name := strings.ReplaceAll(filepath.Base(item.FileName), `"`, "")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.log.Info("download", "id", item.ID, "file", name, "bytes", item.Size, "ip", s.ip(r))
	http.ServeFile(w, r, item.AbsPath)
}

func (s *Server) online(w http.ResponseWriter, r *http.Request) {
	snap := s.status.Get(r.Context())
	s.view(w, r, "online.html", "Online players", "online", snap)
}

func (s *Server) leaderboards(w http.ResponseWriter, r *http.Request) {
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "kills", "honor", "arena", "playtime", "gold":
	default:
		tab = "playtime"
	}
	snap := s.status.Get(r.Context())
	s.view(w, r, "leaderboards.html", "Leaderboards", "leaderboards", map[string]any{
		"Tab":  tab,
		"Snap": snap,
	})
}

const wotlkExpansion uint8 = 2

func (s *Server) registerGET(w http.ResponseWriter, r *http.Request) {
	s.view(w, r, "register.html", "Register", "register", registerForm{NeedKey: s.registerKey() != ""})
}

type registerForm struct {
	Username string
	Email    string
	Error    string
	NeedKey  bool
}

func (s *Server) registerKey() string {
	if s.id != nil {
		if key, set := s.id.Store().RegisterKeyOverride(); set {
			return strings.TrimSpace(key)
		}
	}
	return strings.TrimSpace(s.cfg.RegisterKey)
}

func validRegisterKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	n := utf8.RuneCountInString(key)
	if n < 4 || n > 64 {
		return errors.New("registration key must be 4–64 characters")
	}
	return nil
}

func checkRegisterKey(got, want string) bool {
	if want == "" {
		return true
	}
	a := sha256.Sum256([]byte(strings.TrimSpace(got)))
	b := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (s *Server) registerPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	ip := s.ip(r)
	form := registerForm{
		Username: strings.TrimSpace(r.FormValue("username")),
		Email:    strings.TrimSpace(r.FormValue("email")),
		NeedKey:  s.registerKey() != "",
	}
	fail := func(msg string) {
		form.Error = msg
		w.WriteHeader(http.StatusBadRequest)
		s.view(w, r, "register.html", "Register", "register", form)
	}
	if !s.regRL.Allow(ip) {
		fail("Too many registration attempts. Wait and try again.")
		return
	}
	if err := s.captcha.Verify(r.Context(), captchaToken(r), ip); err != nil {
		fail(err.Error())
		return
	}
	if !checkRegisterKey(r.FormValue("register_key"), s.registerKey()) {
		fail("Invalid registration key.")
		return
	}
	if err := validate.Username(form.Username); err != nil {
		fail(err.Error())
		return
	}
	pass := r.FormValue("password")
	confirm := r.FormValue("password_confirm")
	if pass != confirm {
		fail("passwords do not match")
		return
	}
	if err := validate.Password(pass, form.Username, s.cfg.PasswordMinLength); err != nil {
		fail(err.Error())
		return
	}
	if err := validate.Email(form.Email); err != nil {
		fail(err.Error())
		return
	}
	if s.mail.Enabled() {
		if taken, err := s.id.UsernameTaken(r.Context(), form.Username); err != nil {
			fail("could not create the account right now")
			return
		} else if taken {
			fail(identity.ErrTaken.Error())
			return
		}
		if et, err := s.id.EmailTaken(r.Context(), form.Email); err != nil {
			fail("could not create the account right now")
			return
		} else if et {
			fail(identity.ErrEmailTaken.Error())
			return
		}
		if taken, err := s.accounts.UsernameTaken(r.Context(), form.Username); err != nil {
			fail("could not create the account right now")
			return
		} else if taken {
			fail(account.ErrTaken.Error())
			return
		}
		if s.kb != nil && (s.kb.HasPendingUsername(r.Context(), form.Username) || s.kb.HasPendingEmail(r.Context(), form.Email)) {
			if s.kb.HasPendingUsername(r.Context(), form.Username) {
				fail(account.ErrTaken.Error())
			} else {
				fail(account.ErrEmailTaken.Error())
			}
			return
		}
		siteHash, err := identity.HashPassword(pass)
		if err != nil {
			fail("could not create the account right now")
			return
		}
		salt, verifier, err := account.SignupVerifier(form.Username, pass)
		if err != nil {
			fail("could not create the account right now")
			return
		}
		token, err := s.kb.PutPending(r.Context(), kb.PendingSignup{
			Username: form.Username, Email: form.Email, Salt: salt, Verifier: verifier, Expansion: wotlkExpansion,
			PasswordHash: siteHash, WowUsername: form.Username,
		})
		if err != nil {
			s.log.Error("register pending", "err", err, "user", form.Username)
			fail("could not create the account right now")
			return
		}
		link := s.cfg.SiteURL + "/account/verify/" + token
		body := "Confirm this account for " + s.cfg.RealmName + ".\n\n" +
			"Username: " + strings.ToUpper(form.Username) + "\n\n" +
			"The account is not active until you open this link (expires in 24 hours):\n" + link + "\n\n" +
			"If you did not register, ignore this message."
		if err := s.mail.Send(form.Email, s.cfg.RealmName+" confirm your account", body); err != nil {
			s.kb.DeletePendingToken(r.Context(), token)
			s.log.Error("verify mail", "err", err, "user", form.Username)
			fail("could not send the confirmation email. Try again later.")
			return
		}
		s.flashRedirect(w, r, "/account", "info", "Check your email to activate the account. It cannot log in in-game or here until you confirm.")
		return
	}
	u, err := s.id.Register(r.Context(), form.Username, pass, form.Email, form.Username, pass, wotlkExpansion)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrTaken), errors.Is(err, account.ErrTaken):
			fail(identity.ErrTaken.Error())
		case errors.Is(err, identity.ErrEmailTaken), errors.Is(err, account.ErrEmailTaken):
			fail(identity.ErrEmailTaken.Error())
		default:
			s.log.Error("register", "err", err, "user", form.Username)
			fail("could not create the account right now")
		}
		return
	}
	sess := s.sessions.Regenerate(w, s.sessions.GetOrCreate(w, r))
	sess.User = toSiteUser(u)
	if dek, err := s.id.UnlockDEK(r.Context(), u.ID, pass); err == nil {
		sess.CredentialKey = dek
	}
	sess.SetFlash("success", "Account created. Set your realmlist and enter Azeroth.")
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func toSiteUser(u identity.User) *session.User {
	return &session.User{ID: u.ID, Username: u.Username, Email: u.Email, GMLevel: u.StaffLevel}
}

func (s *Server) wowAccountIDs(ctx context.Context, userID uint32) []uint32 {
	if s.id == nil {
		return nil
	}
	ids, err := s.id.AccountIDs(ctx, userID)
	if err != nil {
		return nil
	}
	return ids
}

func (s *Server) staffMin() uint8 {
	if s.cfg.GMMinLevel < 1 {
		return 3
	}
	return s.cfg.GMMinLevel
}

func (s *Server) modMin() uint8 {
	m := s.cfg.GMModLevel
	if m < 1 {
		m = 1
	}
	if m > s.staffMin() {
		m = s.staffMin()
	}
	return m
}

func (s *Server) applyStaffLevel(ctx context.Context, sess *session.Session) {
	if s.id == nil || sess == nil || sess.User == nil {
		return
	}
	u, err := s.id.GetByID(ctx, sess.User.ID)
	if err != nil {
		return
	}
	sess.User.GMLevel = u.StaffLevel
}

func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		s.flashRedirect(w, r, "/account", "error", "Log in first.")
		return nil
	}
	s.applyStaffLevel(r.Context(), sess)
	if !sess.User.IsStaff(s.staffMin()) {
		http.Error(w, "Admin panel only.", http.StatusForbidden)
		return nil
	}
	return sess
}

func (s *Server) requireMod(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		s.flashRedirect(w, r, "/account", "error", "Log in first.")
		return nil
	}
	s.applyStaffLevel(r.Context(), sess)
	if !sess.User.IsStaff(s.modMin()) {
		http.Error(w, "Staff tickets only.", http.StatusForbidden)
		return nil
	}
	return sess
}

func (s *Server) staffGET(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		s.flashRedirect(w, r, "/account", "error", "Log in first.")
		return
	}
	s.applyStaffLevel(r.Context(), sess)
	if !sess.User.IsStaff(s.staffMin()) {
		if sess.User.IsStaff(s.modMin()) {
			http.Redirect(w, r, "/staff/tickets", http.StatusSeeOther)
			return
		}
		http.Error(w, "Admin panel only.", http.StatusForbidden)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	includeBots := r.URL.Query().Get("bots") == "1"
	pageN := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("p")); err == nil && p > 1 {
		pageN = p
	}
	const per = 40
	rows, total, err := s.accounts.ListAccounts(r.Context(), account.ListFilter{
		Query:       q,
		IncludeBots: includeBots,
		Limit:       per,
		Offset:      (pageN - 1) * per,
	})
	if err != nil {
		s.log.Error("staff list", "err", err)
		s.flashRedirect(w, r, "/account", "error", "Could not load accounts.")
		return
	}
	pages := (total + per - 1) / per
	actorGM := int(sess.User.GMLevel)
	s.attachGatehouse(r.Context(), rows)
	sortStaffAccounts(rows)
	selected := s.staffSelected(r.Context(), rows, r.URL.Query().Get("select"), q, includeBots)
	canModify := true
	canRank := true
	selRank := 0
	var linked []string
	var selChars []status.Character
	if selected != nil {
		var ownerID uint32
		if s.id != nil {
			gh, uid, names := s.id.LinkedWowNames(r.Context(), selected.Username)
			ownerID = uid
			if selected.Gatehouse == "" {
				selected.Gatehouse = gh
			}
			if len(names) > 0 {
				selected.Linked = names
			}
		}
		selRank = int(selected.GMLevel)
		if selected.Gatehouse != "" && s.id != nil {
			if u, err := s.id.GetByUsername(r.Context(), selected.Gatehouse); err == nil {
				selRank = int(u.StaffLevel)
			}
		}
		if selRank > actorGM {
			canModify = false
			canRank = false
		}
		if selected.Username == sess.User.Username || strings.EqualFold(selected.Gatehouse, sess.User.Username) {
			canRank = false
		}
		linked = selected.Linked
		ids := []uint32{selected.ID}
		if ownerID != 0 {
			if accIDs, err := s.id.AccountIDs(r.Context(), ownerID); err == nil && len(accIDs) > 0 {
				ids = accIDs
			}
		}
		if list, err := s.status.AccountCharactersMany(r.Context(), ids); err == nil {
			selChars = list
		}
	}
	s.view(w, r, "staff.html", "Admin panel", "staff", map[string]any{
		"Accounts":          rows,
		"Total":             total,
		"Query":             q,
		"IncludeBots":       includeBots,
		"Page":              pageN,
		"Pages":             pages,
		"HasPrev":           pageN > 1,
		"HasNext":           pageN < pages,
		"PrevPage":          pageN - 1,
		"NextPage":          pageN + 1,
		"Selected":          selected,
		"ActorGM":           actorGM,
		"ActorUser":         sess.User.Username,
		"SelRank":           selRank,
		"CanModify":         canModify,
		"CanRank":           canRank,
		"Downloads":         s.downloads.List(""),
		"DownloadsWritable": s.downloads.Writable(),
		"DownloadsMax":      downloads.HumanSize(s.cfg.DownloadsMaxBytes()),
		"DownloadsScanMax":  downloadScanMax(s),
		"DownloadsScanning": s.downloads.Scanning(),
		"Events":            s.recentStaffEvents(r.Context()),
		"OpenTickets":       s.openTickets(r.Context()),
		"WGOn":              s.cfg.WGEnabled,
		"WGEndpoint":        s.wgEndpoint(),
		"WGPort":            s.cfg.WGPort,
		"WGRealmIP":         wg.TunnelIP(s.cfg.WGServerAddr),
		"RegisterKey":       s.registerKey(),
		"BannerMessage":     s.bannerMessage(r.Context()),
		"BannerUntil":       s.bannerUntilLocal(r.Context()),
		"LinkedLogins":      linked,
		"Calendar":          s.staffCalendar(r.Context()),
		"SelChars":          selChars,
	})
}

func sortStaffAccounts(rows []account.ListedAccount) {
	sort.SliceStable(rows, func(i, j int) bool {
		gi, gj := rows[i].Gatehouse, rows[j].Gatehouse
		if gi == "" && gj != "" {
			return false
		}
		if gi != "" && gj == "" {
			return true
		}
		if gi != gj {
			return gi < gj
		}
		return rows[i].Username < rows[j].Username
	})
}

func (s *Server) attachGatehouse(ctx context.Context, rows []account.ListedAccount) {
	if s.id == nil || len(rows) == 0 {
		return
	}
	ids := make([]uint32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	owners := s.id.Store().GatehouseByAccountIDs(ctx, ids)
	for i := range rows {
		o, ok := owners[rows[i].ID]
		if !ok {
			continue
		}
		rows[i].Gatehouse = o.Username
		rows[i].Linked = o.Linked
		if o.StaffLevel <= account.RankAdmin && rows[i].GMLevel <= account.RankAdmin {
			rows[i].GMLevel = o.StaffLevel
		}
	}
}

func (s *Server) registerKeyPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	key := strings.TrimSpace(r.FormValue("register_key"))
	if err := validRegisterKey(key); err != nil {
		s.flashRedirect(w, r, "/staff#register-key", "error", err.Error())
		return
	}
	if err := s.id.Store().SetRegisterKey(r.Context(), key); err != nil {
		s.flashRedirect(w, r, "/staff#register-key", "error", "Could not save the registration key.")
		return
	}
	if key == "" {
		s.logStaff(sess.User.Username, "register-key", "open")
		s.flashRedirect(w, r, "/staff#register-key", "success", "Public registration is open (no key required).")
		return
	}
	s.logStaff(sess.User.Username, "register-key", "set")
	s.flashRedirect(w, r, "/staff#register-key", "success", "Registration now requires a key.")
}

func (s *Server) logStaff(actor, action, target string) {
	if s.kb == nil {
		return
	}
	if err := s.kb.LogEvent(context.Background(), actor, action, target); err != nil {
		s.log.Error("staff event", "err", err, "action", action)
	}
}

func (s *Server) recentStaffEvents(ctx context.Context) []kb.Event {
	if s.kb == nil {
		return nil
	}
	ev, err := s.kb.RecentEvents(ctx, 200)
	if err != nil {
		s.log.Error("staff events", "err", err)
		return nil
	}
	return ev
}

func (s *Server) openTickets(ctx context.Context) []kb.Ticket {
	if s.kb == nil {
		return nil
	}
	list, err := s.kb.ListOpenTickets(ctx)
	if err != nil {
		s.log.Error("staff open tickets", "err", err)
		return nil
	}
	return list
}

func (s *Server) staffSelected(ctx context.Context, rows []account.ListedAccount, raw, q string, includeBots bool) *account.ListedAccount {
	selectUser := strings.ToUpper(strings.TrimSpace(raw))
	if selectUser == "" {
		return nil
	}
	for i := range rows {
		if rows[i].Username == selectUser {
			return &rows[i]
		}
	}
	listed, err := s.accounts.GetListed(ctx, selectUser)
	if err != nil {
		return nil
	}
	if !listedMatchesFilter(listed, q, includeBots, s.cfg.BotPrefixes) {
		return nil
	}
	return &listed
}

func listedMatchesFilter(a account.ListedAccount, q string, includeBots bool, prefixes []string) bool {
	if !includeBots {
		u := strings.ToUpper(a.Username)
		for _, p := range prefixes {
			if p != "" && strings.HasPrefix(u, strings.ToUpper(p)) {
				return false
			}
		}
	}
	q = strings.ToUpper(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	return strings.Contains(a.Username, q) || strings.Contains(strings.ToUpper(a.Email), q)
}

func (s *Server) staffReturnURL(r *http.Request, selectUser string) string {
	v := url.Values{}
	if q := strings.TrimSpace(r.FormValue("q")); q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("q"))
		if q != "" {
			v.Set("q", q)
		}
	} else {
		v.Set("q", q)
	}
	if r.FormValue("bots") == "1" || r.URL.Query().Get("bots") == "1" {
		v.Set("bots", "1")
	}
	if p := r.FormValue("p"); p == "" {
		p = r.URL.Query().Get("p")
		if p != "" && p != "1" {
			v.Set("p", p)
		}
	} else if p != "1" {
		v.Set("p", p)
	}
	if selectUser != "" {
		v.Set("select", strings.ToUpper(selectUser))
	}
	enc := v.Encode()
	if enc == "" {
		return "/staff"
	}
	return "/staff?" + enc
}

func (s *Server) staffCreatePOST(w http.ResponseWriter, r *http.Request) {
	if s.requireStaff(w, r) == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	user := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	pass := r.FormValue("password")
	if pass != r.FormValue("password_confirm") {
		s.flashRedirect(w, r, "/staff", "error", "passwords do not match")
		return
	}
	if err := validate.Username(user); err != nil {
		s.flashRedirect(w, r, "/staff", "error", err.Error())
		return
	}
	if err := validate.Password(pass, user, s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, "/staff", "error", err.Error())
		return
	}
	if email != "" {
		if err := validate.Email(email); err != nil {
			s.flashRedirect(w, r, "/staff", "error", err.Error())
			return
		}
	}
	if _, err := s.id.Register(r.Context(), user, pass, email, user, pass, wotlkExpansion); err != nil {
		switch {
		case errors.Is(err, identity.ErrTaken), errors.Is(err, account.ErrTaken),
			errors.Is(err, identity.ErrEmailTaken), errors.Is(err, account.ErrEmailTaken):
			s.flashRedirect(w, r, "/staff", "error", err.Error())
		default:
			s.log.Error("staff create", "err", err, "actor", s.sessions.GetOrCreate(w, r).User.Username, "target", user)
			s.flashRedirect(w, r, "/staff", "error", "could not create the account")
		}
		return
	}
	actor := s.sessions.GetOrCreate(w, r).User.Username
	s.log.Info("staff create", "actor", actor, "target", strings.ToUpper(user))
	s.logStaff(actor, "create", strings.ToUpper(user))
	s.flashRedirect(w, r, "/staff?select="+url.QueryEscape(strings.ToUpper(user)), "success", "Created account "+user+".")
}

func (s *Server) staffResetPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	target := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("new_password")
	if pass != r.FormValue("new_password_confirm") {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "passwords do not match")
		return
	}
	if err := validate.Username(target); err != nil {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", err.Error())
		return
	}
	if err := validate.Password(pass, target, s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", err.Error())
		return
	}
	if err := s.accounts.ResetPasswordByGM(r.Context(), sess.User.GMLevel, target, pass); err != nil {
		switch {
		case errors.Is(err, account.ErrForbidden):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Cannot modify GM "+strconv.Itoa(int(s.accountsGM(r, target))))
		case errors.Is(err, account.ErrNotFound):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "account not found")
		default:
			s.log.Error("staff reset", "err", err, "actor", sess.User.Username, "target", target)
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "could not reset password")
		}
		return
	}
	s.log.Info("staff reset", "actor", sess.User.Username, "target", strings.ToUpper(target))
	s.logStaff(sess.User.Username, "reset", strings.ToUpper(target))
	s.flashRedirect(w, r, s.staffReturnURL(r, target), "success", "Password updated for "+target+".")
}

func (s *Server) staffRankPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	target := strings.TrimSpace(r.FormValue("username"))
	lvl, err := strconv.Atoi(strings.TrimSpace(r.FormValue("rank")))
	if err != nil || lvl < 0 || lvl > 255 {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "That rank cannot be granted.")
		return
	}
	level := uint8(lvl)
	if err := validate.Username(target); err != nil {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", err.Error())
		return
	}
	gh, userID, names := s.linkedWowNames(r.Context(), target)
	if userID != 0 && userID == sess.User.ID {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You cannot change your own rank.")
		return
	}
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	current := s.accountsGM(r, names[0])
	for _, n := range names {
		if g := s.accountsGM(r, n); g > current {
			current = g
		}
	}
	if userID != 0 {
		if u, err := s.id.GetByID(r.Context(), userID); err == nil && u.StaffLevel > current {
			current = u.StaffLevel
		}
	}
	if err := account.CanGrantRank(sess.User.GMLevel, current, level); err != nil {
		switch {
		case errors.Is(err, account.ErrBadRank) && level == account.RankSuperGM:
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Super GM cannot be granted from this panel.")
		case errors.Is(err, account.ErrBadRank):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You can only assign a rank below your own (GM or Admin, never Super GM).")
		default:
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Cannot modify "+account.RankName(current)+".")
		}
		return
	}
	var last error
	applied := 0
	for _, n := range names {
		if g := s.accountsGM(r, n); g > account.RankAdmin {
			continue
		}
		if err := s.accounts.ApplyGMLevel(r.Context(), n, level); err != nil {
			last = err
			continue
		}
		applied++
	}
	if applied == 0 {
		s.log.Error("staff rank", "err", last, "actor", sess.User.Username, "target", target, "rank", level)
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "could not update rank")
		return
	}
	if userID != 0 {
		_ = s.id.Store().SetStaffLevel(r.Context(), userID, level)
		if level == account.RankPlayer {
			s.sessions.RevokeUser(userID, "")
		}
	} else {
		s.syncWebsiteStaffLevel(r.Context(), target, level)
	}
	label := strings.ToUpper(target)
	if gh != "" {
		label = gh
	}
	s.log.Info("staff rank", "actor", sess.User.Username, "target", label, "rank", level, "logins", applied)
	s.logStaff(sess.User.Username, "rank", label+"="+account.RankName(level))
	s.flashRedirect(w, r, s.staffReturnURL(r, target), "success", "Set "+label+" to "+account.RankName(level)+" on "+strconv.Itoa(applied)+" Wow.exe login(s).")
}

func (s *Server) linkedWowNames(ctx context.Context, wowUsername string) (gatehouse string, userID uint32, names []string) {
	if s.id == nil {
		return "", 0, []string{strings.ToUpper(strings.TrimSpace(wowUsername))}
	}
	return s.id.LinkedWowNames(ctx, wowUsername)
}

func (s *Server) syncWebsiteStaffLevel(ctx context.Context, wowUsername string, level uint8) {
	if s.id == nil {
		return
	}
	listed, err := s.accounts.GetListed(ctx, wowUsername)
	if err == nil {
		if ln, err := s.id.Store().LinkByAccount(ctx, listed.ID); err == nil {
			_ = s.id.Store().SetStaffLevel(ctx, ln.UserID, level)
			return
		}
	}
	if u, err := s.id.GetByUsername(ctx, wowUsername); err == nil {
		_ = s.id.Store().SetStaffLevel(ctx, u.ID, level)
	}
}

func (s *Server) staffBanPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	target := strings.TrimSpace(r.FormValue("username"))
	dur := strings.TrimSpace(r.FormValue("duration"))
	reason := r.FormValue("reason")
	if err := validate.Username(target); err != nil {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", err.Error())
		return
	}
	_, _, label, ok := account.ParseBanDuration(dur)
	if !ok {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Pick a suspension length.")
		return
	}
	gh, userID, names := s.linkedWowNames(r.Context(), target)
	if userID != 0 && userID == sess.User.ID {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You cannot suspend your own account.")
		return
	}
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	var last error
	banned := 0
	for _, n := range names {
		if err := s.accounts.Ban(r.Context(), sess.User.GMLevel, sess.User.Username, n, dur, reason); err != nil {
			last = err
			continue
		}
		banned++
	}
	if banned == 0 {
		switch {
		case errors.Is(last, account.ErrForbidden):
			if strings.EqualFold(target, sess.User.Username) || strings.EqualFold(gh, sess.User.Username) {
				s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You cannot suspend your own account.")
			} else {
				s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Cannot modify "+account.RankName(s.accountsGM(r, target))+".")
			}
		case errors.Is(last, account.ErrInvalidBan):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Give a reason (at least 3 characters).")
		case errors.Is(last, account.ErrNotFound):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "account not found")
		default:
			s.log.Error("staff ban", "err", last, "actor", sess.User.Username, "target", target)
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "could not suspend the account")
		}
		return
	}
	if userID != 0 {
		s.sessions.RevokeUser(userID, "")
	}
	who := strings.ToUpper(target)
	if gh != "" {
		who = gh
	}
	s.log.Info("staff ban", "actor", sess.User.Username, "target", who, "duration", label, "logins", banned)
	s.logStaff(sess.User.Username, "ban", who+"="+label)
	s.flashRedirect(w, r, s.staffReturnURL(r, target), "success", "Suspended "+who+" ("+label+") on "+strconv.Itoa(banned)+" Wow.exe login(s).")
}

func (s *Server) staffUnbanPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	target := strings.TrimSpace(r.FormValue("username"))
	if err := validate.Username(target); err != nil {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", err.Error())
		return
	}
	gh, userID, names := s.linkedWowNames(r.Context(), target)
	if userID != 0 && userID == sess.User.ID {
		s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You cannot change your own suspension.")
		return
	}
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	var last error
	n := 0
	for _, name := range names {
		if err := s.accounts.Unban(r.Context(), sess.User.GMLevel, sess.User.Username, name); err != nil {
			last = err
			continue
		}
		n++
	}
	if n == 0 {
		switch {
		case errors.Is(last, account.ErrForbidden):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Cannot modify "+account.RankName(s.accountsGM(r, target))+".")
		case errors.Is(last, account.ErrNotFound):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "account not found")
		default:
			s.log.Error("staff unban", "err", last, "actor", sess.User.Username, "target", target)
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "could not lift the suspension")
		}
		return
	}
	who := strings.ToUpper(target)
	if gh != "" {
		who = gh
	}
	s.log.Info("staff unban", "actor", sess.User.Username, "target", who, "logins", n)
	s.logStaff(sess.User.Username, "unban", who)
	s.flashRedirect(w, r, s.staffReturnURL(r, target), "success", "Lifted suspension for "+who+" on "+strconv.Itoa(n)+" Wow.exe login(s).")
}

func (s *Server) accountsGM(r *http.Request, username string) uint8 {
	listed, err := s.accounts.GetListed(r.Context(), username)
	if err != nil {
		return 0
	}
	return listed.GMLevel
}

func captchaToken(r *http.Request) string {
	if v := r.FormValue("cf-turnstile-response"); v != "" {
		return v
	}
	return r.FormValue("h-captcha-response")
}

func (s *Server) accountGET(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.GetOrCreate(w, r)
	var chars []status.Character
	if sess.User != nil {
		list, err := s.status.AccountCharactersMany(r.Context(), s.wowAccountIDs(r.Context(), sess.User.ID))
		if err != nil {
			s.log.Error("account characters", "err", err, "user", sess.User.ID)
		} else {
			chars = list
		}
	}
	var wowLogins []wowLoginView
	if sess.User != nil && s.id != nil {
		wowLogins = s.wowLoginViews(r, sess)
	}
	totpOn := false
	totpSecret, totpURL, totpQR := "", "", ""
	var totpCodes []string
	var passkeys []identity.Passkey
	var wgPeers []wgPeerView
	if sess.User != nil {
		totpOn = s.id.TOTPEnabled(r.Context(), sess.User.ID)
		totpCodes = sess.TOTPCodes
		sess.TOTPCodes = nil
		if !totpOn {
			if secret, otpURL, err := s.id.Store().PendingTOTP(r.Context(), sess.User.ID, sess.User.Username, s.cfg.RealmName); err == nil && secret != "" {
				totpSecret, totpURL = secret, otpURL
				if qr, err := identity.QRDataURI(otpURL); err == nil {
					totpQR = qr
				}
			}
		}
		passkeys, _ = s.id.Store().ListPasskeys(r.Context(), sess.User.ID)
		if s.wgOn() {
			if list, err := s.id.Store().ListWGPeers(r.Context(), sess.User.ID); err == nil {
				if keys, err := wg.EnsureServerKeys(s.cfg.WGDir); err == nil {
					wgPeers = s.wgPeerViews(list, keys.Public)
				}
			}
		}
	}
	s.view(w, r, "account.html", "Account", "account", map[string]any{
		"Characters":  chars,
		"WowLogins":   wowLogins,
		"WowUnlocked": sess.User != nil && len(sess.CredentialKey) == 32,
		"WowMax":      s.cfg.WowCredentialsMax,
		"TOTPOn":      totpOn,
		"TOTPPending": sess.PendingUser != nil && sess.User == nil,
		"TOTPSecret":  totpSecret,
		"TOTPURL":     template.URL(totpURL),
		"TOTPQR":      template.URL(totpQR),
		"TOTPCodes":   totpCodes,
		"Passkeys":    passkeys,
		"PasskeysOK":  s.wa != nil,
		"PasskeyMax":  identity.MaxPasskeys,
		"WGOn":        s.wgOn(),
		"WGPeers":     wgPeers,
		"WGMax":       s.cfg.WGPeerMax,
	})
}

func (s *Server) wowCredentialPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	user := strings.TrimSpace(r.FormValue("wow_username"))
	pass := r.FormValue("wow_password")
	if pass != r.FormValue("wow_password_confirm") {
		s.flashRedirect(w, r, "/account", "error", "WoW passwords do not match")
		return
	}
	if err := validate.Username(user); err != nil {
		s.flashRedirect(w, r, "/account", "error", err.Error())
		return
	}
	if err := validate.Password(pass, user, s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, "/account", "error", err.Error())
		return
	}
	if len(sess.CredentialKey) != 32 {
		s.flashRedirect(w, r, "/account#wow", "error", "Enter your website password to save client logins.")
		return
	}
	if _, err := s.id.AddCredentialWithDEK(r.Context(), sess.User.ID, user, pass, "", wotlkExpansion, sess.CredentialKey); err != nil {
		switch {
		case errors.Is(err, identity.ErrTaken), errors.Is(err, account.ErrTaken):
			s.flashRedirect(w, r, "/account", "error", "that client username is taken")
		case errors.Is(err, identity.ErrTooMany):
			s.flashRedirect(w, r, "/account", "error", "you already have the maximum number of WoW client logins")
		case errors.Is(err, account.ErrNotFound):
			s.log.Error("wow credential", "err", err, "user", sess.User.Username)
			s.flashRedirect(w, r, "/account#wow", "error", "The realm did not finish creating that login. Try again in a moment.")
		default:
			s.log.Error("wow credential", "err", err, "user", sess.User.Username)
			s.flashRedirect(w, r, "/account#wow", "error", "could not create the client login")
		}
		return
	}
	s.flashRedirect(w, r, "/account#wow", "success", "Added WoW client login "+strings.ToUpper(user)+". Use it in the 3.3.5a client.")
}

type wowLoginView struct {
	Username  string
	Password  string
	HasSecret bool
	LastLogin string
	Chars     int
}

func (s *Server) wowLoginViews(r *http.Request, sess *session.Session) []wowLoginView {
	links, err := s.id.Links(r.Context(), sess.User.ID)
	if err != nil {
		return nil
	}
	ids := make([]uint32, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.AccountID)
	}
	stats := s.status.AccountStats(r.Context(), ids)
	dek := sess.CredentialKey
	out := make([]wowLoginView, 0, len(links))
	for _, l := range links {
		st := stats[l.AccountID]
		row := wowLoginView{
			Username:  l.Username,
			HasSecret: l.HasSecret(),
			LastLogin: st.LastLogin,
			Chars:     st.Chars,
		}
		if row.LastLogin == "" {
			row.LastLogin = "—"
		}
		if len(dek) == 32 && l.HasSecret() {
			if p, err := s.id.OpenClientPassword(l, dek); err == nil {
				row.Password = p
			} else {
				row.HasSecret = false
			}
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) wowUnlockPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	dek, err := s.id.UnlockDEK(r.Context(), sess.User.ID, r.FormValue("current_password"))
	if err != nil {
		s.flashRedirect(w, r, "/account#wow", "error", "Website password is incorrect.")
		return
	}
	sess.CredentialKey = dek
	s.flashRedirect(w, r, "/account#wow", "success", "Client passwords unlocked for this session.")
}

func (s *Server) wowPasswordPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if len(sess.CredentialKey) != 32 {
		s.flashRedirect(w, r, "/account#wow", "error", "Enter your website password to save client logins.")
		return
	}
	user := strings.TrimSpace(r.FormValue("wow_username"))
	pass := r.FormValue("wow_password")
	if pass != r.FormValue("wow_password_confirm") {
		s.flashRedirect(w, r, "/account#wow", "error", "WoW passwords do not match")
		return
	}
	if err := validate.Username(user); err != nil {
		s.flashRedirect(w, r, "/account#wow", "error", err.Error())
		return
	}
	if err := validate.Password(pass, user, s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, "/account#wow", "error", err.Error())
		return
	}
	links, err := s.id.Links(r.Context(), sess.User.ID)
	if err != nil {
		s.flashRedirect(w, r, "/account#wow", "error", "Could not save the client password.")
		return
	}
	owned := false
	for _, l := range links {
		if strings.EqualFold(l.Username, user) {
			owned = true
			break
		}
	}
	if !owned {
		http.NotFound(w, r)
		return
	}
	if err := s.accounts.ResetPassword(r.Context(), user, pass); err != nil {
		s.log.Error("wow password", "err", err, "user", sess.User.Username)
		s.flashRedirect(w, r, "/account#wow", "error", "Could not save the client password.")
		return
	}
	if err := s.id.SealClientPassword(r.Context(), sess.User.ID, user, pass, sess.CredentialKey); err != nil {
		s.flashRedirect(w, r, "/account#wow", "error", "Could not save the client password.")
		return
	}
	s.flashRedirect(w, r, "/account#wow", "success", "Saved Wow.exe password for "+strings.ToUpper(user)+".")
}

func (s *Server) unstuckPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.unstuckRL.Allow(s.ip(r) + ":" + sess.User.Username) {
		s.flashRedirect(w, r, "/account", "error", "Too many unstuck attempts. Wait and try again.")
		return
	}
	guid, err := strconv.ParseUint(strings.TrimSpace(r.FormValue("guid")), 10, 32)
	if err != nil || guid == 0 {
		s.flashRedirect(w, r, "/account", "error", "Character not found.")
		return
	}
	res, err := s.status.UnstuckAny(r.Context(), s.wowAccountIDs(r.Context(), sess.User.ID), uint32(guid))
	if err != nil {
		switch {
		case errors.Is(err, status.ErrCharOnline):
			s.flashRedirect(w, r, "/account", "error", "Log out of the game first, then unstuck.")
		case errors.Is(err, status.ErrCharNotFound):
			s.flashRedirect(w, r, "/account", "error", "Character not found.")
		case errors.Is(err, status.ErrNoHomebind):
			s.flashRedirect(w, r, "/account", "error", "No hearthstone bind yet. Visit an inn in-game first.")
		default:
			s.log.Error("unstuck", "err", err, "account", sess.User.ID, "guid", guid, "actor", sess.User.Username)
			s.flashRedirect(w, r, "/account", "error", "Could not unstuck right now.")
		}
		return
	}
	s.log.Info("unstuck",
		"actor", sess.User.Username,
		"account", sess.User.ID,
		"guid", res.GUID,
		"name", res.Name,
		"from_map", res.FromMap,
		"from_zone", res.FromZone,
		"to_map", res.ToMap,
		"to_zone", res.ToZone,
		"via", res.Via,
	)
	s.logStaff(sess.User.Username, "unstuck", res.Name)
	s.flashRedirect(w, r, "/account", "success", res.Name+" was sent to their hearth. Log in in-game.")
}

func (s *Server) loginPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	ip := s.ip(r)
	if !s.loginRL.Allow(ip) {
		s.flashRedirect(w, r, "/account", "error", "Too many login attempts.")
		return
	}
	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	u, err := s.id.Authenticate(r.Context(), user, pass)
	if err != nil {
		if errors.Is(err, account.ErrBanned) {
			s.flashRedirect(w, r, "/account", "error", "This account is suspended.")
			return
		}
		if s.mail.Enabled() && s.kb != nil && s.kb.HasPendingUsername(r.Context(), user) {
			s.flashRedirect(w, r, "/account", "error", "Confirm the link we emailed before this account can log in.")
			return
		}
		s.flashRedirect(w, r, "/account", "error", "Invalid username or password.")
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	dek, _ := s.id.UnlockDEK(r.Context(), u.ID, pass)
	if s.id.TOTPEnabled(r.Context(), u.ID) {
		sess.PendingUser = toSiteUser(u)
		sess.PendingNext = r.FormValue("next")
		sess.User = nil
		sess.CredentialKey = dek
		s.flashRedirect(w, r, "/account", "info", "Enter the code from your authenticator app.")
		return
	}
	sess = s.sessions.Regenerate(w, sess)
	sess.User = toSiteUser(u)
	sess.CredentialKey = dek
	sess.SetFlash("success", "Welcome back, "+u.Username+".")
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (s *Server) logoutPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	s.sessions.Destroy(w, sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) passwordPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	old := r.FormValue("current_password")
	nw := r.FormValue("new_password")
	conf := r.FormValue("new_password_confirm")
	if nw != conf {
		s.flashRedirect(w, r, "/account", "error", "new passwords do not match")
		return
	}
	if err := validate.Password(nw, sess.User.Username, s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, "/account", "error", err.Error())
		return
	}
	if s.id.TOTPEnabled(r.Context(), sess.User.ID) {
		if err := s.id.Store().ValidateTOTP(r.Context(), sess.User.ID, r.FormValue("code")); err != nil {
			s.flashRedirect(w, r, "/account#password", "error", "Authenticator code required to change password.")
			return
		}
	}
	cur, err := s.id.GetByID(r.Context(), sess.User.ID)
	if err != nil {
		s.flashRedirect(w, r, "/account", "error", "could not change password")
		return
	}
	if err := s.id.ChangePassword(r.Context(), cur, old, nw); err != nil {
		if errors.Is(err, identity.ErrBadPassword) {
			s.flashRedirect(w, r, "/account", "error", "current password is incorrect")
			return
		}
		s.log.Error("password change", "user", sess.User.Username, "err", err)
		s.flashRedirect(w, r, "/account", "error", "could not change password")
		return
	}
	s.sessions.RevokeUser(sess.User.ID, sess.ID)
	if dek, err := s.id.UnlockDEK(r.Context(), sess.User.ID, nw); err == nil {
		sess.CredentialKey = dek
	}
	s.flashRedirect(w, r, "/account", "success", "Website password updated. Your WoW client password is unchanged.")
}

func (s *Server) verifyGET(w http.ResponseWriter, r *http.Request) {
	if !s.mail.Enabled() {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if _, err := s.kb.PeekPending(r.Context(), token); err != nil {
		s.flashRedirect(w, r, "/account", "error", "That confirmation link is invalid or expired. Register again if you still need an account.")
		return
	}
	s.view(w, r, "verify.html", "Confirm email", "account", map[string]any{"Token": token})
}

func (s *Server) verifyPOST(w http.ResponseWriter, r *http.Request) {
	if !s.mail.Enabled() {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	pending, err := s.kb.ConsumePending(r.Context(), token)
	if err != nil {
		s.flashRedirect(w, r, "/account", "error", "That confirmation link is invalid or expired. Register again if you still need an account.")
		return
	}
	hash := pending.PasswordHash
	if hash == "" {
		hash = "!migrated"
	}
	wowUser := pending.WowUsername
	if wowUser == "" {
		wowUser = pending.Username
	}
	u, err := s.id.Store().CreateUser(r.Context(), pending.Username, pending.Email, hash, 0)
	if err != nil {
		s.log.Error("verify user", "err", err, "user", pending.Username)
		s.flashRedirect(w, r, "/account", "error", "Could not activate the account. The username may already be taken.")
		return
	}
	if err := s.accounts.CreatePrepared(r.Context(), wowUser, pending.Email, pending.Expansion, pending.Salt, pending.Verifier); err != nil {
		s.log.Error("verify create", "err", err, "user", pending.Username)
		s.flashRedirect(w, r, "/account", "error", "Could not activate the account. The username may already be taken.")
		return
	}
	if listed, err := s.accounts.GetListed(r.Context(), wowUser); err == nil {
		_ = s.id.Store().Link(r.Context(), u.ID, listed.ID, wowUser)
	}
	s.flashRedirect(w, r, "/account", "success", "Email confirmed. You can log in on the website and in the 3.3.5a client.")
}

func (s *Server) resetGET(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SMTPConfigured() {
		http.NotFound(w, r)
		return
	}
	s.view(w, r, "reset.html", "Reset password", "account", map[string]any{"Stage": "request"})
}

func (s *Server) resetPOST(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SMTPConfigured() {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	ip := s.ip(r)
	if !s.resetRL.Allow(ip) {
		s.flashRedirect(w, r, "/account/reset", "error", "Too many reset attempts.")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	acc, err := s.id.GetByEmail(r.Context(), email)
	if err == nil {
		token, err := s.accounts.IssueResetToken(acc.Username)
		if err == nil {
			link := s.cfg.SiteURL + "/account/reset/" + token
			body := "A password reset was requested for " + acc.Username + " on " + s.cfg.RealmName + ".\n\n" +
				"This link expires in 15 minutes and can be used once:\n" + link + "\n\n" +
				"If you did not request this, ignore the message."
			if err := s.mail.Send(acc.Email, s.cfg.RealmName+" password reset", body); err != nil {
				s.log.Error("reset mail", "err", err)
			}
		}
	}
	s.flashRedirect(w, r, "/account/reset", "info", "If that email is registered, a reset link is on its way.")
}

func (s *Server) resetConfirmGET(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SMTPConfigured() {
		http.NotFound(w, r)
		return
	}
	s.view(w, r, "reset.html", "Choose a new password", "account", map[string]any{
		"Stage": "confirm",
		"Token": r.PathValue("token"),
	})
}

func (s *Server) resetConfirmPOST(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SMTPConfigured() {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	token := r.PathValue("token")
	nw := r.FormValue("new_password")
	if nw != r.FormValue("new_password_confirm") {
		s.flashRedirect(w, r, r.URL.Path, "error", "passwords do not match")
		return
	}
	if err := validate.Password(nw, "", s.cfg.PasswordMinLength); err != nil {
		s.flashRedirect(w, r, r.URL.Path, "error", err.Error())
		return
	}
	user, err := s.accounts.ConsumeResetTokenUser(token)
	if err != nil {
		s.flashRedirect(w, r, r.URL.Path, "error", "reset link is invalid or expired")
		return
	}
	if err := s.id.SetPassword(r.Context(), user, nw); err != nil {
		s.flashRedirect(w, r, r.URL.Path, "error", "reset link is invalid or expired")
		return
	}
	if u, err := s.id.GetByUsername(r.Context(), user); err == nil {
		s.sessions.RevokeUser(u.ID, "")
	}
	sess := s.sessions.GetOrCreate(w, r)
	sess.SetFlash("success", "Password reset. You can log in.")
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) contactGET(w http.ResponseWriter, r *http.Request) {
	s.view(w, r, "contact.html", "Contact", "contact", nil)
}

func (s *Server) contactPOST(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SMTPConfigured() {
		http.Error(w, "Mail is not configured.", http.StatusNotFound)
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	ip := s.ip(r)
	if !s.contactRL.Allow(ip) {
		s.flashRedirect(w, r, "/contact", "error", "Too many messages. Try later.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	message := strings.TrimSpace(r.FormValue("message"))
	if name == "" || len(message) < 10 || len(message) > 4000 {
		s.flashRedirect(w, r, "/contact", "error", "Name and a message (10–4000 characters) are required.")
		return
	}
	if err := validate.Email(email); err != nil {
		s.flashRedirect(w, r, "/contact", "error", err.Error())
		return
	}
	if subject == "" {
		subject = "Website contact"
	}
	to := s.cfg.ContactEmail
	if to == "" {
		to = s.cfg.SMTPFrom
	}
	body := "From: " + name + " <" + email + ">\nIP: " + ip + "\n\n" + message
	if err := s.mail.Send(to, "[Gatehouse] "+subject, body); err != nil {
		s.log.Error("contact mail", "err", err)
		s.flashRedirect(w, r, "/contact", "error", "Could not send the message.")
		return
	}
	s.flashRedirect(w, r, "/contact", "success", "Message sent.")
}

func (s *Server) flashRedirect(w http.ResponseWriter, r *http.Request, dest, kind, text string) {
	sess := s.sessions.GetOrCreate(w, r)
	sess.SetFlash(kind, text)
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
