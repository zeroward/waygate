package web

import (
	"net/http"
	"strings"

	"github.com/zeroward/waygate/internal/identity"
)

func (s *Server) totpLoginPOST(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	if sess.PendingUser == nil {
		s.flashRedirect(w, r, "/account", "error", "Log in with your password first.")
		return
	}
	if !s.loginRL.Allow(s.ip(r) + ":totp:" + sess.PendingUser.Username) {
		s.flashRedirect(w, r, "/account", "error", "Too many attempts. Wait and try again.")
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	if err := s.id.Store().ValidateTOTP(r.Context(), sess.PendingUser.ID, code); err != nil {
		s.flashRedirect(w, r, "/account", "error", "Invalid authenticator code.")
		return
	}
	next := sess.PendingNext
	u := *sess.PendingUser
	sess.PendingUser = nil
	sess.PendingNext = ""
	sess = s.sessions.Regenerate(w, sess)
	sess.User = &u
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
	secret, url, err := s.id.Store().StartTOTP(r.Context(), sess.User.ID, sess.User.Username, s.cfg.RealmName)
	if err != nil {
		s.log.Error("totp start", "err", err)
		s.flashRedirect(w, r, "/account", "error", "Could not start authenticator setup.")
		return
	}
	sess.TOTPSecret = secret
	sess.TOTPURL = url
	sess.TOTPQR = ""
	if qr, err := identity.QRDataURI(url); err != nil {
		s.log.Error("totp qr", "err", err)
	} else {
		sess.TOTPQR = qr
	}
	sess.TOTPCodes = nil
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
	codes, err := s.id.Store().ConfirmTOTP(r.Context(), sess.User.ID, r.FormValue("code"))
	if err != nil {
		s.flashRedirect(w, r, "/account#totp", "error", err.Error())
		return
	}
	sess.TOTPSecret, sess.TOTPURL, sess.TOTPQR = "", "", ""
	sess.TOTPCodes = codes
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
	if err := s.id.Store().ValidateTOTP(r.Context(), sess.User.ID, r.FormValue("code")); err != nil {
		s.flashRedirect(w, r, "/account#totp", "error", "Invalid authenticator code.")
		return
	}
	if err := s.id.Store().DisableTOTP(r.Context(), sess.User.ID); err != nil {
		s.flashRedirect(w, r, "/account", "error", "Could not disable authenticator.")
		return
	}
	sess.TOTPSecret, sess.TOTPURL, sess.TOTPQR, sess.TOTPCodes = "", "", "", nil
	s.flashRedirect(w, r, "/account#totp", "success", "Authenticator disabled.")
}
