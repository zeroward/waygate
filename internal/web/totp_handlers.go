package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/session"
)

func (s *Server) totpLoginPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	if sess.Snapshot().PendingUser == nil || time.Now().After(sess.Snapshot().PendingExpiry) {
		s.flashRedirect(w, r, "/account", "error", "Log in with your password first.")
		return
	}
	if !s.authLimit(w, r, sess.Snapshot().PendingUser.Username) {
		s.flashRedirect(w, r, "/account", "error", "Too many attempts. Wait and try again.")
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	if err := s.id.Store().ValidateTOTP(r.Context(), sess.Snapshot().PendingUser.ID, code); err != nil {
		s.flashRedirect(w, r, "/account", "error", "Invalid authenticator code.")
		return
	}
	state := sess.Snapshot()
	current, err := s.checkUser(r, state.PendingUser)
	if err != nil || current.AuthMode == identity.AuthSSO {
		http.Error(w, "Please sign in again.", 401)
		return
	}
	next := state.PendingNext
	u := toSiteUser(current)
	sess = s.transition(w, r, sess, func(d *session.Data) {
		if d.PendingUser == nil || d.PendingUser.ID != u.ID || time.Now().After(d.PendingExpiry) {
			panic(session.ErrRevoked)
		}
		*d = session.Data{User: u, CredentialKey: state.CredentialKey}
	})
	sess.SetFlash("success", "Welcome back, "+u.Username+".")
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

func (s *Server) totpStartPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.requireAction(w, r, sess, "totp-start") {
		return
	}
	if _, _, err := s.id.Store().StartTOTP(r.Context(), sess.Snapshot().User.ID, sess.Snapshot().User.Username, s.cfg.RealmName, sess.Snapshot().User.AuthVersion); err != nil {
		if errors.Is(err, identity.ErrTOTPEnabled) {
			s.flashRedirect(w, r, "/account#totp", "error", "Disable the current authenticator before setting up a new one.")
			return
		}
		s.log.Error("totp start", "err", err)
		s.flashRedirect(w, r, "/account#totp", "error", "Could not start authenticator setup.")
		return
	}
	s.flashRedirect(w, r, "/account#totp", "info", "Scan the QR code with your authenticator app, then confirm with a code.")
}

func (s *Server) totpConfirmPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.authLimit(w, r, sess.Snapshot().User.Username) {
		s.flashRedirect(w, r, "/account#totp", "error", "Too many attempts. Wait and try again.")
		return
	}
	codes, err := s.id.Store().ConfirmTOTP(r.Context(), sess.Snapshot().User.ID, r.FormValue("code"), sess.Snapshot().User.AuthVersion)
	if err != nil {
		s.flashRedirect(w, r, "/account#totp", "error", err.Error())
		return
	}
	sess = s.refreshSelf(w, r, sess)
	sess.Update(func(d *session.Data) { d.TOTPCodes = codes })
	s.flashRedirect(w, r, "/account#totp", "success", "Authenticator enabled. Store the recovery codes; they are shown once.")
}

func (s *Server) totpDisablePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.requireAction(w, r, sess, "totp-disable") {
		return
	}
	if err := s.id.Store().DisableTOTP(r.Context(), sess.Snapshot().User.ID, sess.Snapshot().User.AuthVersion); err != nil {
		s.flashRedirect(w, r, "/account", "error", "Could not disable authenticator.")
		return
	}
	sess = s.refreshSelf(w, r, sess)
	sess.Update(func(d *session.Data) { d.TOTPSecret, d.TOTPURL, d.TOTPQR, d.TOTPCodes = "", "", "", nil })
	s.flashRedirect(w, r, "/account#totp", "success", "Authenticator disabled.")
}
