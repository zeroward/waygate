package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/zeroward/waygate/internal/session"
)

func validPurpose(p string) bool {
	switch p {
	case "passkey-add", "passkey-delete", "totp-start", "totp-disable":
		return true
	}
	return false
}
func (h *Server) passwordProof(r *http.Request, u *session.User, password, code string) bool {
	current, err := h.id.Authenticate(r.Context(), u.Username, password)
	if err != nil || current.ID != u.ID || current.AuthVersion != u.AuthVersion {
		return false
	}
	on, err := h.id.TOTPEnabled(r.Context(), u.ID)
	if err != nil {
		return false
	}
	return !on || h.id.Store().ValidateTOTP(r.Context(), u.ID, code) == nil
}
func (s *Server) consumeGrant(h *session.Session, purpose string) bool {
	ok := false
	h.Update(func(d *session.Data) {
		ok = d.User != nil && d.ReauthPurpose == purpose && time.Now().Before(d.ReauthExpiry)
		if ok {
			d.ReauthPurpose = ""
			d.ReauthExpiry = time.Time{}
		}
	})
	return ok
}
func (s *Server) grant(h *session.Session, purpose string) {
	h.Update(func(d *session.Data) { d.ReauthPurpose = purpose; d.ReauthExpiry = time.Now().Add(5 * time.Minute) })
}
func (s *Server) reauthPasswordPOST(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRFJSON(w, r) {
		return
	}
	h := s.requireLoginJSON(w, r)
	if h == nil {
		return
	}
	s.limitJSONBody(w, r)
	var in struct {
		Purpose  string `json:"purpose"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !validPurpose(in.Purpose) {
		writeJSONError(w, 400, "Invalid verification request.")
		return
	}
	u := h.Snapshot().User
	if !s.authLimit(w, r, u.Username) {
		return
	}
	if !s.passwordProof(r, u, in.Password, in.Code) {
		writeJSONError(w, 401, "Could not verify your credentials.")
		return
	}
	s.grant(h, in.Purpose)
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) setChallenge(h *session.Session, raw []byte, purpose, name, next string) {
	h.Update(func(d *session.Data) {
		d.WebAuthnJSON = raw
		d.WebAuthnPurpose = purpose
		d.WebAuthnName = name
		d.WebAuthnNext = next
		d.WebAuthnStarted = time.Now()
		d.WebAuthnExpiry = time.Now().Add(5 * time.Minute)
		d.WebAuthnVersion = 0
		if d.User != nil {
			d.WebAuthnVersion = d.User.AuthVersion
		}
	})
}
func (s *Server) takeChallenge(h *session.Session, purpose string) (session.Data, bool) {
	var out session.Data
	ok := false
	h.Update(func(d *session.Data) {
		if d.WebAuthnPurpose != purpose || !time.Now().Before(d.WebAuthnExpiry) {
			return
		}
		if d.User != nil && d.User.AuthVersion != d.WebAuthnVersion {
			return
		}
		out = *d
		out.WebAuthnJSON = append([]byte(nil), d.WebAuthnJSON...)
		d.WebAuthnJSON = nil
		d.WebAuthnPurpose = ""
		ok = true
	})
	return out, ok
}
func (s *Server) reauthPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	h := s.requireLoginJSON(w, r)
	if h == nil {
		return
	}
	var in struct {
		Purpose string `json:"purpose"`
	}
	s.limitJSONBody(w, r)
	if json.NewDecoder(r.Body).Decode(&in) != nil || !validPurpose(in.Purpose) {
		writeJSONError(w, 400, "Invalid verification request.")
		return
	}
	u := h.Snapshot().User
	if !s.authLimit(w, r, u.Username) {
		return
	}
	wu, err := s.id.Store().WAUser(r.Context(), u.ID)
	if err != nil {
		writeJSONError(w, 503, "Account verification unavailable.")
		return
	}
	opts, sd, err := s.wa.BeginLogin(wu, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeJSONError(w, 400, "Use your password and authenticator instead.")
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		panic(err)
	}
	s.setChallenge(h, raw, "reauth", in.Purpose, "")
	writeJSON(w, 200, opts)
}
func (s *Server) reauthPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	h := s.requireLoginJSON(w, r)
	if h == nil {
		return
	}
	u := h.Snapshot().User
	if !s.authLimit(w, r, u.Username) {
		return
	}
	state, ok := s.takeChallenge(h, "reauth")
	if !ok {
		writeJSONError(w, 400, "Verification expired.")
		return
	}
	var sd webauthn.SessionData
	if json.Unmarshal(state.WebAuthnJSON, &sd) != nil {
		writeJSONError(w, 400, "Verification expired.")
		return
	}
	wu, err := s.id.Store().WAUser(r.Context(), u.ID)
	if err != nil {
		writeJSONError(w, 503, "Account verification unavailable.")
		return
	}
	s.limitJSONBody(w, r)
	cred, err := s.wa.FinishLogin(wu, sd, r)
	if err != nil || cred == nil || !cred.Flags.UserVerified {
		writeJSONError(w, 401, "Passkey verification failed. Use a PIN or biometric.")
		return
	}
	if _, err = s.checkUser(r, u); err != nil {
		writeJSONError(w, 401, "Please sign in again.")
		return
	}
	if err = s.id.Store().UpdatePasskey(r.Context(), cred); err != nil {
		writeJSONError(w, 503, "Could not save verification.")
		return
	}
	s.grant(h, state.WebAuthnName)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// HTML actions support direct password+MFA proof or an action-bound passkey grant.
func (s *Server) requireAction(w http.ResponseWriter, r *http.Request, h *session.Session, purpose string) bool {
	if s.consumeGrant(h, purpose) {
		return true
	}
	u := h.Snapshot().User
	if !s.authLimit(w, r, u.Username) {
		return false
	}
	if s.passwordProof(r, u, r.FormValue("current_password"), r.FormValue("code")) {
		return true
	}
	http.Error(w, "Verify your password and authenticator, or use an existing passkey.", http.StatusForbidden)
	return false
}
