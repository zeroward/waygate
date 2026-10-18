package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/session"
)

func newWebAuthn(cfg config.Config) (*webauthn.WebAuthn, error) {
	id, origins, err := webAuthnRP(cfg)
	if err != nil {
		return nil, err
	}
	if id == "" || len(origins) == 0 {
		return nil, nil
	}
	return webauthn.New(&webauthn.Config{
		RPID:                  id,
		RPDisplayName:         cfg.RealmName,
		RPOrigins:             origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
}

func webAuthnRP(cfg config.Config) (id string, origins []string, err error) {
	var siteHost string
	origins = append([]string(nil), cfg.WebAuthnOrigins...)
	if cfg.SiteURL != "" {
		u, perr := url.Parse(cfg.SiteURL)
		if perr != nil {
			return "", nil, perr
		}
		siteHost = u.Hostname()
		if len(origins) == 0 && u.Scheme != "" && u.Host != "" {
			origins = []string{u.Scheme + "://" + u.Host}
		}
	}
	override := strings.TrimSpace(cfg.WebAuthnRPID)
	if override != "" {
		if siteHost == "" || !strings.EqualFold(override, siteHost) {
			return "", nil, fmt.Errorf("WEBAUTHN_RP_ID %q must match SITE_URL host %q (do not use PUBLIC_HOST)", override, siteHost)
		}
		id = override
	} else {
		id = siteHost
	}
	if id == "" || len(origins) == 0 {
		return "", nil, nil
	}
	return id, origins, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func (s *Server) requireCSRFJSON(w http.ResponseWriter, r *http.Request) bool {
	sess := s.sessions.GetOrCreate(w, r)
	token := r.Header.Get("X-CSRF-Token")
	if !sess.ValidCSRF(token) {
		writeJSONError(w, http.StatusForbidden, "Invalid request token. Reload the page and try again.")
		return false
	}
	return true
}

func (s *Server) passkeysReady(w http.ResponseWriter) bool {
	if s.wa != nil && s.id != nil {
		return true
	}
	writeJSONError(w, http.StatusServiceUnavailable, "Passkeys are not available on this site.")
	return false
}

func (s *Server) requireLoginJSON(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.Snapshot().User == nil {
		writeJSONError(w, http.StatusUnauthorized, "Log in first.")
		return nil
	}
	return sess
}

func (s *Server) limitJSONBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
}

func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	sess := s.requireLoginJSON(w, r)
	if sess == nil {
		return
	}
	if !s.consumeGrant(sess, "passkey-add") {
		writeJSONError(w, 403, "Verify your identity before adding a passkey.")
		return
	}
	s.limitJSONBody(w, r)
	var in struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	wu, err := s.id.Store().WAUser(r.Context(), sess.Snapshot().User.ID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not start passkey setup.")
		return
	}
	if len(wu.Creds) >= identity.MaxPasskeys {
		writeJSONError(w, http.StatusBadRequest, "You already have the maximum number of passkeys.")
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range wu.Creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts := []webauthn.RegistrationOption{
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	}
	if len(exclude) > 0 {
		opts = append(opts, webauthn.WithExclusions(exclude))
	}
	creation, sd, err := s.wa.BeginRegistration(wu, opts...)
	if err != nil {
		s.log.Error("passkey register begin", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "Could not start passkey setup.")
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Could not start passkey setup.")
		return
	}
	s.setChallenge(sess, raw, "register", identity.SanitizePasskeyName(in.Name), "")
	writeJSON(w, http.StatusOK, creation)
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	sess := s.requireLoginJSON(w, r)
	if sess == nil {
		return
	}
	state, ok := s.takeChallenge(sess, "register")
	if !ok {
		writeJSONError(w, 400, "Passkey request expired. Try again.")
		return
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(state.WebAuthnJSON, &sd); err != nil || sd.Challenge == "" {
		writeJSONError(w, http.StatusBadRequest, "Passkey setup expired. Try again.")
		return
	}
	name := state.WebAuthnName

	wu, err := s.id.Store().WAUser(r.Context(), sess.Snapshot().User.ID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not save the passkey.")
		return
	}
	s.limitJSONBody(w, r)
	cred, err := s.wa.FinishRegistration(wu, sd, r)
	if err != nil || cred == nil || !cred.Flags.UserVerified {
		s.log.Error("passkey register finish", "err", err)
		writeJSONError(w, http.StatusBadRequest, "Could not verify the passkey.")
		return
	}
	if err := s.id.Store().InsertPasskey(r.Context(), sess.Snapshot().User.ID, name, cred, sess.Snapshot().User.AuthVersion); err != nil {
		if errors.Is(err, identity.ErrTooManyPasskeys) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Error("passkey insert", "err", err)
		writeJSONError(w, http.StatusBadRequest, "Could not save the passkey.")
		return
	}
	sess = s.refreshSelf(w, r, sess)
	sess.SetFlash("success", "Passkey added.")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	if !s.loginRL.Allow("security:ip:" + s.ip(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "Too many attempts. Wait and try again.")
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	s.limitJSONBody(w, r)
	var in struct {
		Next string `json:"next"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	assertion, sd, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.log.Error("passkey login begin", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "Could not start passkey login.")
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Could not start passkey login.")
		return
	}
	s.setChallenge(sess, raw, "login", "", safeNext(in.Next))
	writeJSON(w, http.StatusOK, assertion)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysReady(w) || !s.requireCSRFJSON(w, r) {
		return
	}
	if !s.loginRL.Allow(s.ip(r) + ":passkey:finish") {
		writeJSONError(w, http.StatusTooManyRequests, "Too many attempts. Wait and try again.")
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	state, ok := s.takeChallenge(sess, "login")
	if !ok {
		writeJSONError(w, 400, "Passkey request expired. Try again.")
		return
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(state.WebAuthnJSON, &sd); err != nil || sd.Challenge == "" {
		writeJSONError(w, http.StatusBadRequest, "Passkey login expired. Try again.")
		return
	}
	next := state.WebAuthnNext

	s.limitJSONBody(w, r)
	var found identity.WAUser
	cred, err := s.wa.FinishDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := s.id.Store().DiscoverableUser(r.Context(), rawID, userHandle)
		if err != nil {
			return nil, err
		}
		if wu, ok := u.(identity.WAUser); ok {
			if !s.loginRL.Allow("security:user:" + strings.ToUpper(wu.Username)) {
				return nil, fmt.Errorf("too many authentication attempts")
			}
			found = wu
		}
		return u, nil
	}, sd, r)
	if err != nil || found.ID == 0 || cred == nil || !cred.Flags.UserVerified {
		s.log.Error("passkey login finish", "err", err)
		writeJSONError(w, http.StatusUnauthorized, "Passkey was not recognized.")
		return
	}
	current, err := s.id.GetByID(r.Context(), found.ID)
	if err != nil || current.AuthMode == identity.AuthSSO || current.AuthVersion != found.AuthVersion || current.AuthChanged >= state.WebAuthnStarted.UnixNano() || time.Now().After(state.WebAuthnExpiry) {
		writeJSONError(w, 401, "Passkey login expired. Try again.")
		return
	}
	found.User = current
	if err := s.id.RejectIfBanned(r.Context(), found.User); err != nil {
		if errors.Is(err, account.ErrBanned) {
			writeJSONError(w, http.StatusUnauthorized, "This account is suspended.")
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "Passkey was not recognized.")
		return
	}
	if cred != nil {
		if cred.Authenticator.CloneWarning {
			s.log.Error("passkey clone warning", "user", found.Username)
		}
		if err := s.id.Store().UpdatePasskey(r.Context(), cred); err != nil {
			s.log.Error("passkey update", "err", err, "user", found.Username)
			writeJSONError(w, 503, "Could not save authentication.")
			return
		}
	}
	sess = s.transition(w, r, sess, func(d *session.Data) { *d = session.Data{User: toSiteUser(found.User)} })
	sess.SetFlash("success", "Welcome back, "+found.Username+".")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "next": safeNext(next)})
}

func (s *Server) passkeyDeletePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.requireAction(w, r, sess, "passkey-delete") {
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if id < 1 {
		s.flashRedirect(w, r, "/account#passkeys", "error", "Passkey not found.")
		return
	}
	if err := s.id.Store().DeletePasskey(r.Context(), sess.Snapshot().User.ID, id, sess.Snapshot().User.AuthVersion); err != nil {
		s.flashRedirect(w, r, "/account#passkeys", "error", "Passkey not found.")
		return
	}
	sess = s.refreshSelf(w, r, sess)
	s.flashRedirect(w, r, "/account#passkeys", "success", "Passkey removed.")
}
