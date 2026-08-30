package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/downloads"
	"github.com/zeroward/waygate/internal/session"
	"github.com/zeroward/waygate/internal/status"
	"github.com/zeroward/waygate/internal/validate"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	snap := s.status.Get(r.Context())
	s.view(w, r, "home.html", "Home", "home", snap)
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
	if s.requireLogin(w, r) == nil {
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
	case "kills", "honor", "arena", "playtime":
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
	s.view(w, r, "register.html", "Register", "register", registerForm{})
}

type registerForm struct {
	Username string
	Email    string
	Error    string
}

func (s *Server) registerPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	ip := s.ip(r)
	form := registerForm{
		Username: strings.TrimSpace(r.FormValue("username")),
		Email:    strings.TrimSpace(r.FormValue("email")),
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
	if err := s.accounts.Create(r.Context(), form.Username, pass, form.Email, wotlkExpansion); err != nil {
		switch {
		case errors.Is(err, account.ErrTaken):
			fail(err.Error())
		case errors.Is(err, account.ErrEmailTaken):
			fail(err.Error())
		default:
			s.log.Error("register", "err", err, "user", form.Username)
			fail("could not create the account right now")
		}
		return
	}
	acc, err := s.accounts.Authenticate(r.Context(), form.Username, pass)
	sess := s.sessions.GetOrCreate(w, r)
	if err == nil {
		sess = s.sessions.Regenerate(w, sess)
		sess.User = toUser(acc)
	}
	sess.SetFlash("success", "Account created. Set your realmlist and enter Azeroth.")
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func toUser(a *account.Account) *session.User {
	return &session.User{ID: a.ID, Username: a.Username, Email: a.Email, GMLevel: a.GMLevel}
}

func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		s.flashRedirect(w, r, "/account", "error", "Log in first.")
		return nil
	}
	if !sess.User.IsStaff(s.cfg.GMMinLevel) {
		http.Error(w, "Admin panel only.", http.StatusForbidden)
		return nil
	}
	return sess
}

func (s *Server) staffGET(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
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
	selected := s.staffSelected(r.Context(), rows, r.URL.Query().Get("select"), q, includeBots)
	canModify := true
	canRank := true
	selRank := 0
	if selected != nil {
		selRank = int(selected.GMLevel)
		if selRank > actorGM {
			canModify = false
			canRank = false
		}
		if selected.Username == sess.User.Username {
			canRank = false
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
	})
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
	if err := s.accounts.Create(r.Context(), user, pass, email, wotlkExpansion); err != nil {
		switch {
		case errors.Is(err, account.ErrTaken), errors.Is(err, account.ErrEmailTaken):
			s.flashRedirect(w, r, "/staff", "error", err.Error())
		default:
			s.log.Error("staff create", "err", err, "actor", s.sessions.GetOrCreate(w, r).User.Username, "target", user)
			s.flashRedirect(w, r, "/staff", "error", "could not create the account")
		}
		return
	}
	s.log.Info("staff create", "actor", s.sessions.GetOrCreate(w, r).User.Username, "target", strings.ToUpper(user))
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
	if err := s.accounts.SetGMLevel(r.Context(), sess.User.GMLevel, sess.User.Username, target, level); err != nil {
		switch {
		case errors.Is(err, account.ErrBadRank) && level == account.RankSuperGM:
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Super GM cannot be granted from this panel.")
		case errors.Is(err, account.ErrBadRank):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You can only assign a rank below your own (GM or Admin, never Super GM).")
		case errors.Is(err, account.ErrForbidden):
			if strings.EqualFold(target, sess.User.Username) {
				s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "You cannot change your own rank.")
			} else {
				s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "Cannot modify "+account.RankName(s.accountsGM(r, target))+".")
			}
		case errors.Is(err, account.ErrNotFound):
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "account not found")
		default:
			s.log.Error("staff rank", "err", err, "actor", sess.User.Username, "target", target, "rank", level)
			s.flashRedirect(w, r, s.staffReturnURL(r, target), "error", "could not update rank")
		}
		return
	}
	s.log.Info("staff rank", "actor", sess.User.Username, "target", strings.ToUpper(target), "rank", level)
	s.flashRedirect(w, r, s.staffReturnURL(r, target), "success", "Set "+target+" to "+account.RankName(level)+".")
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
		list, err := s.status.AccountCharacters(r.Context(), sess.User.ID)
		if err != nil {
			s.log.Error("account characters", "err", err, "account", sess.User.ID)
		} else {
			chars = list
		}
	}
	s.view(w, r, "account.html", "Account", "account", map[string]any{
		"Characters": chars,
	})
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
	res, err := s.status.Unstuck(r.Context(), sess.User.ID, uint32(guid))
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
	acc, err := s.accounts.Authenticate(r.Context(), user, pass)
	if err != nil {
		s.flashRedirect(w, r, "/account", "error", "Invalid username or password.")
		return
	}
	sess := s.sessions.Regenerate(w, s.sessions.GetOrCreate(w, r))
	sess.User = toUser(acc)
	sess.SetFlash("success", "Welcome back, "+acc.Username+".")
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
	if err := s.accounts.ChangePassword(r.Context(), sess.User.Username, old, nw); err != nil {
		if errors.Is(err, account.ErrBadPassword) {
			s.flashRedirect(w, r, "/account", "error", "current password is incorrect")
			return
		}
		s.log.Error("password change", "user", sess.User.Username, "err", err)
		s.flashRedirect(w, r, "/account", "error", "could not change password")
		return
	}
	s.flashRedirect(w, r, "/account", "success", "Password updated. Use it at the login screen.")
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
	acc, err := s.accounts.FindByEmail(r.Context(), email)
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
	if err := s.accounts.ConsumeResetToken(token, nw, r.Context()); err != nil {
		s.flashRedirect(w, r, r.URL.Path, "error", "reset link is invalid or expired")
		return
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
