package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/session"
	"github.com/zeroward/waygate/internal/sso"
	"github.com/zeroward/waygate/internal/validate"
)

func (s *Server) ssoReady(w http.ResponseWriter) bool {
	if s.oidc == nil {
		http.Error(w, "Single sign-on is unavailable. Contact the site administrator.", 503)
		return false
	}
	return true
}
func (s *Server) beginSSO(w http.ResponseWriter, r *http.Request, h *session.Session, flow session.OIDCFlow) {
	t, dest, err := s.oidc.Begin(r.Context(), flow.Purpose != "login")
	if err != nil {
		http.Error(w, "Single sign-on is temporarily unavailable. Try again later.", 503)
		return
	}
	flow.State, flow.Nonce, flow.Verifier = t.State, t.Nonce, t.Verifier
	flow.Started, flow.Expires, flow.Fresh = t.Started, t.Expires, t.Fresh
	h.Update(func(d *session.Data) { d.OIDC = flow })
	// Finish POST navigation on our own origin. An external 303 from a form
	// would violate the initiating document's form-action 'self' CSP. The new
	// document navigates using a normal link, with optional self-hosted JS.
	if r.Method == http.MethodPost {
		s.view(w, r, "sso_redirect.html", "Continue to single sign-on", "account", map[string]any{"AuthorizationURL": dest})
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) || !s.authLimit(w, r, "sso") {
		return
	}
	h := s.sessions.GetOrCreate(w, r)
	if h.Snapshot().User != nil {
		http.Redirect(w, r, "/account", 303)
		return
	}
	s.beginSSO(w, r, h, session.OIDCFlow{Purpose: "login", Next: safeNext(r.URL.Query().Get("next"))})
}
func verifiedIdentity(f session.OIDCFlow) identity.SSOIdentity {
	return identity.SSOIdentity{Issuer: f.Issuer, Subject: f.Subject, Email: f.Email, EmailVerified: f.EmailVerified}
}
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) {
		return
	}
	h := s.sessions.GetOrCreate(w, r)
	var f session.OIDCFlow
	ok := false
	h.Update(func(d *session.Data) {
		if d.OIDC.State != "" && subtle.ConstantTimeCompare([]byte(d.OIDC.State), []byte(r.URL.Query().Get("state"))) == 1 {
			f = d.OIDC
			d.OIDC = session.OIDCFlow{}
			ok = true
		}
	})
	defer clear(f.LinkKey)
	if !ok || !time.Now().Before(f.Expires) || r.URL.Query().Get("error") != "" {
		http.Error(w, "SSO request expired or was cancelled. Start again.", 400)
		return
	}
	claims, err := s.oidc.Finish(r.Context(), sso.Transaction{State: f.State, Nonce: f.Nonce, Verifier: f.Verifier, Started: f.Started, Expires: f.Expires, Fresh: f.Fresh}, r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "SSO verification failed. Start again.", 401)
		return
	}
	f.State, f.Nonce, f.Verifier = "", "", ""
	f.Issuer, f.Subject, f.Email, f.EmailVerified = claims.Issuer, claims.Subject, claims.Email, claims.EmailVerified
	f.Verified = true
	if f.Purpose == "link" {
		u, e := s.id.GetByID(r.Context(), f.UserID)
		if e != nil || u.AuthMode != identity.AuthLocal || u.AuthVersion != f.AuthVersion || len(f.LinkKey) != 32 || s.id.RejectIfBanned(r.Context(), u) != nil {
			http.Error(w, "Account verification expired. Start linking again.", 401)
			return
		}
		if _, e = s.id.Store().SSOUser(r.Context(), verifiedIdentity(f)); !errors.Is(e, identity.ErrNotFound) {
			http.Error(w, "That SSO identity cannot be linked.", 409)
			return
		}
		h.Update(func(d *session.Data) { d.OIDC = f })
		http.Redirect(w, r, "/auth/oidc/onboard", 303)
		return
	}
	u, err := s.id.Store().SSOUser(r.Context(), verifiedIdentity(f))
	if f.Purpose != "login" {
		state := h.Snapshot()
		if err != nil || state.User == nil || u.ID != f.UserID || u.ID != state.User.ID || u.AuthVersion != f.AuthVersion || u.AuthVersion != state.User.AuthVersion || state.AuthMethod != identity.AuthSSO || s.id.RejectIfBanned(r.Context(), u) != nil {
			http.Error(w, "Verify using the SSO identity linked to this account.", 401)
			return
		}
		if !validSSOPurpose(f.Purpose) {
			http.Error(w, "Invalid verification purpose.", 400)
			return
		}
		dek, e := s.id.Store().UnlockSSO(r.Context(), u, s.vault)
		if e != nil {
			http.Error(w, "Saved credentials are unavailable. Contact the site administrator.", 503)
			return
		}
		defer clear(dek)
		s.transition(w, r, h, func(d *session.Data) {
			*d = session.Data{User: toSiteUser(u), AuthMethod: identity.AuthSSO, CredentialKey: dek, CredentialPurpose: f.Purpose, CredentialExpiry: time.Now().Add(5 * time.Minute)}
		})
		http.Redirect(w, r, "/account#wow", 303)
		return
	}
	if errors.Is(err, identity.ErrNotFound) {
		h = s.transition(w, r, h, func(d *session.Data) { *d = session.Data{OIDC: f} })
		http.Redirect(w, r, "/auth/oidc/onboard", 303)
		return
	}
	if err != nil || s.id.RejectIfBanned(r.Context(), u) != nil {
		http.Error(w, "Could not sign in to this account.", 401)
		return
	}
	s.completeSSO(w, r, h, u, f.Next, false)
}
func (s *Server) completeSSO(w http.ResponseWriter, r *http.Request, h *session.Session, u identity.User, next string, revoke bool) {
	h = s.transition(w, r, h, func(d *session.Data) { *d = session.Data{User: toSiteUser(u), AuthMethod: identity.AuthSSO} })
	if revoke {
		s.sessions.RevokeUser(u.ID, h.ID)
	}
	s.log.Info("SSO authenticated", "user_id", u.ID)
	http.Redirect(w, r, safeNext(next), 303)
}
func (s *Server) ssoOnboardGET(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) {
		return
	}
	f := s.sessions.GetOrCreate(w, r).Snapshot().OIDC
	if !f.Verified || !time.Now().Before(f.Expires) {
		s.flashRedirect(w, r, "/account", "error", "SSO request expired. Start again.")
		return
	}
	name := ""
	if f.Purpose == "link" {
		u, err := s.id.GetByID(r.Context(), f.UserID)
		if err != nil {
			http.Error(w, "Account unavailable.", 400)
			return
		}
		name = u.Username
	}
	s.view(w, r, "sso.html", "Single sign-on", "account", map[string]any{"Flow": f, "LinkUsername": name, "NeedKey": s.registerKey() != ""})
}
func (s *Server) ssoSignupPOST(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) || !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	h := s.sessions.GetOrCreate(w, r)
	f := h.Snapshot().OIDC
	if !f.Verified || f.Purpose != "login" || !time.Now().Before(f.Expires) || h.Snapshot().User != nil {
		http.Error(w, "SSO request expired. Start again.", 400)
		return
	}
	fail := func(msg string) { s.flashRedirect(w, r, "/auth/oidc/onboard", "error", msg) }
	if !s.regRL.Allow(s.ip(r)) {
		http.Error(w, "Too many registration attempts. Try later.", 429)
		return
	}
	if !checkRegisterKey(r.FormValue("register_key"), s.registerKey()) {
		fail("Invalid registration key.")
		return
	}
	if err := s.captcha.Verify(r.Context(), captchaToken(r), s.ip(r)); err != nil {
		fail("Captcha verification failed.")
		return
	}
	name := strings.TrimSpace(r.FormValue("username"))
	if err := validate.Username(name); err != nil {
		fail(err.Error())
		return
	}
	if !f.EmailVerified || validate.Email(f.Email) != nil {
		fail("Your provider must supply a verified email address to create an account.")
		return
	}
	// Reserve existing realm usernames for their owners, including accounts not
	// imported yet. Future realm imports also refuse name-only links to SSO users.
	taken, err := s.accounts.UsernameTaken(r.Context(), name)
	if err != nil {
		fail("Account registration is temporarily unavailable.")
		return
	}
	if taken {
		fail("That username belongs to an existing game account. Link it or choose another website username.")
		return
	}
	if s.kb.HasPendingUsername(r.Context(), name) || s.kb.HasPendingEmail(r.Context(), f.Email) {
		fail("An account with these details is awaiting email confirmation.")
		return
	}
	u, err := s.id.Store().CreateSSO(r.Context(), name, verifiedIdentity(f), s.vault)
	if err != nil {
		if errors.Is(err, identity.ErrEmailTaken) {
			fail("An account already uses this email. Link your existing account instead.")
		} else {
			fail("Could not create the account. The username or SSO identity may already be registered.")
		}
		return
	}
	s.log.Info("SSO account created", "user_id", u.ID)
	s.completeSSO(w, r, h, u, "/account", false)
}
func (s *Server) ssoLinkPOST(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) || !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	h := s.sessions.GetOrCreate(w, r)
	state := h.Snapshot()
	name := r.FormValue("username")
	if state.User != nil {
		name = state.User.Username
	}
	if !s.authLimit(w, r, name) {
		return
	}
	u, err := s.id.Authenticate(r.Context(), name, r.FormValue("current_password"))
	if err != nil || (state.User != nil && (state.User.ID != u.ID || state.User.AuthVersion != u.AuthVersion)) {
		http.Error(w, "Could not verify the existing account.", 401)
		return
	}
	// Authenticate may claim a legacy account; refresh its stored version/hash.
	u, err = s.id.GetByID(r.Context(), u.ID)
	if err != nil || u.AuthMode != identity.AuthLocal {
		http.Error(w, "Could not verify the existing account.", 401)
		return
	}
	on, err := s.id.TOTPEnabled(r.Context(), u.ID)
	if err != nil || (on && s.id.Store().ValidateTOTP(r.Context(), u.ID, r.FormValue("code")) != nil) {
		http.Error(w, "A valid authenticator or recovery code is required.", 401)
		return
	}
	dek, err := s.id.UnlockDEK(r.Context(), u.ID, r.FormValue("current_password"))
	if err != nil {
		http.Error(w, "Saved credentials could not be unlocked. Linking was not started.", 409)
		return
	}
	defer clear(dek)
	s.beginSSO(w, r, h, session.OIDCFlow{Purpose: "link", UserID: u.ID, AuthVersion: u.AuthVersion, LinkKey: dek, Next: "/account"})
}
func (s *Server) ssoConfirmPOST(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) || !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	h := s.sessions.GetOrCreate(w, r)
	var f session.OIDCFlow
	h.Update(func(d *session.Data) { f = d.OIDC; d.OIDC = session.OIDCFlow{} })
	defer clear(f.LinkKey)
	if !f.Verified || f.Purpose != "link" || !time.Now().Before(f.Expires) || len(f.LinkKey) != 32 {
		http.Error(w, "Linking expired. Start again.", 400)
		return
	}
	u, err := s.id.GetByID(r.Context(), f.UserID)
	if err != nil || u.AuthVersion != f.AuthVersion || u.AuthMode != identity.AuthLocal || s.id.RejectIfBanned(r.Context(), u) != nil {
		http.Error(w, "Account verification expired.", 401)
		return
	}
	u, err = s.id.Store().LinkSSO(r.Context(), u, verifiedIdentity(f), f.LinkKey, s.vault)
	if err != nil {
		http.Error(w, "Could not link the account. No authentication changes were saved.", 409)
		return
	}
	s.log.Info("SSO account linked", "user_id", u.ID)
	s.completeSSO(w, r, h, u, "/account", true)
}
func validSSOPurpose(p string) bool {
	return p == "wow-reveal" || p == "wow-create" || p == "wow-password"
}
func (s *Server) ssoReauthPOST(w http.ResponseWriter, r *http.Request) {
	if !s.ssoReady(w) || !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	h := s.requireLogin(w, r)
	if h == nil {
		return
	}
	state := h.Snapshot()
	purpose := r.FormValue("purpose")
	u, err := s.checkUser(r, state.User)
	if err != nil || u.AuthMode != identity.AuthSSO || state.AuthMethod != identity.AuthSSO || !validSSOPurpose(purpose) {
		http.Error(w, "Invalid verification request.", 400)
		return
	}
	if !s.authLimit(w, r, u.Username) {
		return
	}
	h.Update(func(d *session.Data) {
		d.CredentialKey = nil
		d.CredentialPurpose = ""
		d.CredentialExpiry = time.Time{}
	})
	s.beginSSO(w, r, h, session.OIDCFlow{Purpose: purpose, UserID: u.ID, AuthVersion: u.AuthVersion})
}

// gameKey requires an action-specific SSO grant. Mutation grants are single-use;
// reveal access expires after five minutes and is never persisted to SQLite.
func (s *Server) gameKey(h *session.Session, purpose string, consume bool) []byte {
	var key []byte
	h.Update(func(d *session.Data) {
		if d.User == nil {
			return
		}
		if d.AuthMethod != identity.AuthSSO {
			key = append([]byte(nil), d.CredentialKey...)
			return
		}
		if d.CredentialPurpose == purpose && time.Now().Before(d.CredentialExpiry) {
			key = append([]byte(nil), d.CredentialKey...)
			if !consume {
				return
			}
		}
		if consume || !time.Now().Before(d.CredentialExpiry) {
			d.CredentialKey = nil
			d.CredentialPurpose = ""
			d.CredentialExpiry = time.Time{}
		}
	})
	return key
}
