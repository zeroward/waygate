package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/session"
)

func (s *Server) authLimit(w http.ResponseWriter, r *http.Request, name string) bool {
	if !s.loginRL.Allow("security:ip:"+s.ip(r)) || !s.loginRL.Allow("security:user:"+strings.ToUpper(strings.TrimSpace(name))) {
		http.Error(w, "Too many attempts. Wait and try again.", http.StatusTooManyRequests)
		return false
	}
	return true
}
func (s *Server) checkUser(r *http.Request, u *session.User) (identity.User, error) {
	current, err := s.id.GetByID(r.Context(), u.ID)
	if err != nil {
		return identity.User{}, err
	}
	if current.AuthVersion != u.AuthVersion {
		return identity.User{}, session.ErrRevoked
	}
	if err = s.id.RejectIfBanned(r.Context(), current); err != nil {
		return identity.User{}, err
	}
	return current, nil
}
func (s *Server) validateSession(w http.ResponseWriter, r *http.Request) bool {
	h := s.sessions.GetOrCreate(w, r)
	d := h.Snapshot()
	u := d.User
	if u == nil {
		u = d.PendingUser
	}
	if u == nil {
		return true
	}
	current, err := s.checkUser(r, u)
	if err == nil && current.AuthMode == identity.AuthSSO && (d.AuthMethod != identity.AuthSSO || d.PendingUser != nil) {
		err = session.ErrRevoked
	}
	if err == nil && d.PendingUser != nil && (d.PendingExpiry.IsZero() || time.Now().After(d.PendingExpiry)) {
		err = session.ErrRevoked
	}
	if err == nil {
		return true
	}
	if errors.Is(err, session.ErrRevoked) || errors.Is(err, account.ErrBanned) || errors.Is(err, identity.ErrNotFound) {
		s.sessions.Destroy(w, h)
		http.Error(w, "Please sign in again.", http.StatusUnauthorized)
	} else {
		s.log.Error("authentication state unavailable", "user_id", u.ID)
		http.Error(w, "Account verification is temporarily unavailable.", http.StatusServiceUnavailable)
	}
	return false
}

// refreshSelf runs only after a successful self-service change committed its new version.
func (s *Server) refreshSelf(w http.ResponseWriter, r *http.Request, h *session.Session) *session.Session {
	old := h.Snapshot()
	if old.User == nil {
		panic(session.ErrRevoked)
	}
	u, err := s.id.GetByID(r.Context(), old.User.ID)
	if err != nil {
		panic(err)
	}
	if u.AuthVersion != old.User.AuthVersion+1 {
		panic(session.ErrRevoked)
	}
	if err = s.id.RejectIfBanned(r.Context(), u); err != nil {
		panic(err)
	}
	fresh := s.transition(w, r, h, func(d *session.Data) { *d = session.Data{User: toSiteUser(u), CredentialKey: old.CredentialKey} })
	s.sessions.RevokeUser(u.ID, fresh.ID)
	return fresh
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, old *session.Session, fn func(*session.Data)) *session.Session {
	fresh := s.sessions.Transition(w, old, fn)
	*r = *session.WithSession(r, fresh)
	return fresh
}
