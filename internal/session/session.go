// Package session owns mutable authentication state. Callers only see deep copies.
package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

const CookieName = "waygate_session"

var ErrRevoked = errors.New("session expired or revoked")

type User struct {
	ID              uint32
	Username, Email string
	GMLevel         uint8
	AuthVersion     uint64
}

func (u *User) IsStaff(min uint8) bool {
	if u == nil {
		return false
	}
	if min == 0 {
		min = 1
	}
	return u.GMLevel >= min
}

type Flash struct{ Kind, Text string }

// Data is a detached snapshot. Changes take effect only through Session.Update.
type OIDCFlow struct {
	State, Nonce, Verifier, Purpose, Next string
	Started, Expires                      time.Time
	Fresh, Verified                       bool
	UserID                                uint32
	AuthVersion                           uint64
	Issuer, Subject, Email                string
	EmailVerified                         bool
	LinkKey                               []byte `json:"-"`
}

type Data struct {
	AuthMethod        string
	OIDC              OIDCFlow
	CredentialExpiry  time.Time `json:"-"`
	CredentialPurpose string    `json:"-"`

	User                                        *User
	PendingUser                                 *User
	PendingNext                                 string
	PendingExpiry                               time.Time
	TOTPSecret                                  string   `json:"-"`
	TOTPURL                                     string   `json:"-"`
	TOTPQR                                      string   `json:"-"`
	TOTPCodes                                   []string `json:"-"`
	WebAuthnJSON                                []byte
	WebAuthnName, WebAuthnNext, WebAuthnPurpose string
	WebAuthnExpiry                              time.Time
	WebAuthnStarted                             time.Time
	WebAuthnVersion                             uint64
	ReauthPurpose                               string
	ReauthExpiry                                time.Time
	CredentialKey                               []byte `json:"-"`
	CSRF                                        string
	Flash                                       *Flash
	Created, Expiry                             time.Time
}
type Session struct {
	ID        string // immutable
	data      Data
	store     *Store
	destroyed bool
}
type Store struct {
	mu               sync.Mutex
	items            map[string]*Session
	db               *sql.DB
	ttl              time.Duration
	secure, identity bool
	lastPrune        time.Time
}
type contextKey struct{}

func NewStore(db *sql.DB, ttl time.Duration, secure bool) (*Store, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	s := &Store{items: map[string]*Session{}, db: db, ttl: ttl, secure: secure}
	if db != nil {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS http_sessions(id TEXT PRIMARY KEY,data TEXT NOT NULL,expiry TEXT NOT NULL,created_at TEXT NOT NULL,user_id INTEGER); CREATE INDEX IF NOT EXISTS idx_http_sessions_user ON http_sessions(user_id); CREATE INDEX IF NOT EXISTS idx_http_sessions_expiry ON http_sessions(expiry)`); err != nil {
			return nil, err
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&n); err != nil {
			return nil, err
		}
		s.identity = n > 0
	}
	return s, nil
}
func copyData(d Data) Data {
	d.OIDC.LinkKey = append([]byte(nil), d.OIDC.LinkKey...)
	if d.User != nil {
		u := *d.User
		d.User = &u
	}
	if d.PendingUser != nil {
		u := *d.PendingUser
		d.PendingUser = &u
	}
	if d.Flash != nil {
		f := *d.Flash
		d.Flash = &f
	}
	d.CredentialKey = append([]byte(nil), d.CredentialKey...)
	d.WebAuthnJSON = append([]byte(nil), d.WebAuthnJSON...)
	d.TOTPCodes = append([]string(nil), d.TOTPCodes...)
	return d
}
func (s *Session) Snapshot() Data {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.destroyed {
		panic(ErrRevoked)
	}
	return copyData(s.data)
}
func (s *Session) Update(fn func(*Data)) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.destroyed || time.Now().After(s.data.Expiry) {
		panic(ErrRevoked)
	}
	d := copyData(s.data)
	fn(&d)
	if err := s.store.persistLocked(s.ID, d, false); err != nil {
		panic(err)
	}
	s.data = copyData(d)
}
func associated(d Data) *User {
	if d.User != nil {
		return d.User
	}
	return d.PendingUser
}
func (s *Store) persistLocked(id string, d Data, create bool) error {
	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var uid any
	if u := associated(d); u != nil {
		uid = u.ID
		if s.identity {
			var v uint64
			if err := tx.QueryRow(`SELECT auth_version FROM users WHERE id=?`, u.ID).Scan(&v); err != nil {
				return err
			}
			if v != u.AuthVersion {
				return ErrRevoked
			}
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if create {
		_, err = tx.Exec(`INSERT INTO http_sessions(id,data,expiry,created_at,user_id) VALUES(?,?,?,?,?)`, id, string(raw), d.Expiry.UTC().Format(time.RFC3339Nano), d.Created.UTC().Format(time.RFC3339Nano), uid)
	} else {
		var res sql.Result
		res, err = tx.Exec(`UPDATE http_sessions SET data=?,expiry=?,user_id=? WHERE id=?`, string(raw), d.Expiry.UTC().Format(time.RFC3339Nano), uid, id)
		if err == nil {
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrRevoked
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) getLocked(id string) *Session {
	if cached := s.items[id]; cached != nil {
		if cached.destroyed || time.Now().After(cached.data.Expiry) {
			delete(s.items, id)
			return nil
		}
		// Durable deletion/version checks prevent stale process-local cache acceptance.
		if s.db != nil {
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM http_sessions WHERE id=?`, id).Scan(&n); err != nil {
				panic(err)
			}
			if n == 0 {
				s.invalidateLocked(cached)
				return nil
			}
		}
		return cached
	}
	if s.db == nil {
		return nil
	}
	var raw string
	err := s.db.QueryRow(`SELECT data FROM http_sessions WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		panic(err)
	}
	var d Data
	if json.Unmarshal([]byte(raw), &d) != nil || time.Now().After(d.Expiry) {
		return nil
	}
	h := &Session{ID: id, data: d, store: s}
	s.items[id] = h
	return h
}
func (s *Store) get(id string) *Session { s.mu.Lock(); defer s.mu.Unlock(); return s.getLocked(id) }
func (s *Store) GetOrCreate(w http.ResponseWriter, r *http.Request) *Session {
	if h, ok := r.Context().Value(contextKey{}).(*Session); ok {
		s.mu.Lock()
		live := !h.destroyed && time.Now().Before(h.data.Expiry)
		s.mu.Unlock()
		if !live {
			panic(ErrRevoked)
		}
		return h
	}
	if c, err := r.Cookie(CookieName); err == nil && len(c.Value) == 64 {
		if h := s.get(c.Value); h != nil {
			return h
		}
	}
	return s.create(w)
}
func (s *Store) Bind(w http.ResponseWriter, r *http.Request) *http.Request {
	return WithSession(r, s.GetOrCreate(w, r))
}

// WithSession binds only this request to a newly rotated session.
func WithSession(r *http.Request, h *Session) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, h))
}
func (s *Store) NewAnonymous(w http.ResponseWriter) *Session { return s.create(w) }
func (s *Store) create(w http.ResponseWriter) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if now.Sub(s.lastPrune) > time.Minute {
		if s.db != nil {
			if _, err := s.db.Exec("DELETE FROM http_sessions WHERE expiry < ?", now.Format(time.RFC3339Nano)); err != nil {
				panic(err)
			}
		}
		for _, cached := range s.items {
			if now.After(cached.data.Expiry) {
				s.invalidateLocked(cached)
			}
		}
		s.lastPrune = now
	}
	h := &Session{ID: randomID(), store: s, data: Data{CSRF: randomID(), Created: now, Expiry: now.Add(s.ttl)}}
	if err := s.persistLocked(h.ID, h.data, true); err != nil {
		panic(err)
	}
	s.items[h.ID] = h
	s.writeCookie(w, h.ID)
	return h
}

// SaveLatest is retained for test/helper compatibility; mutations persist immediately.
func (s *Store) SaveLatest(h *Session) {}
func (s *Store) invalidateLocked(h *Session) {
	h.destroyed = true
	clear(h.data.CredentialKey)
	h.data = Data{}
	delete(s.items, h.ID)
}
func (s *Store) RevokeUser(id uint32, except string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		if _, err := s.db.Exec(`DELETE FROM http_sessions WHERE user_id=? AND id<>?`, id, except); err != nil {
			panic(err)
		}
	}
	for key, h := range s.items {
		if u := associated(h.data); key != except && u != nil && u.ID == id {
			s.invalidateLocked(h)
		}
	}
}
func (s *Store) Regenerate(w http.ResponseWriter, old *Session) *Session {
	return s.Transition(w, old, func(d *Data) {
		d.PendingUser = nil
		d.PendingNext = ""
		d.PendingExpiry = time.Time{}
		d.WebAuthnJSON = nil
		d.WebAuthnPurpose = ""
		d.ReauthPurpose = ""
	})
}

// Transition atomically deletes the old row and inserts the new authenticated state.
func (s *Store) Transition(w http.ResponseWriter, old *Session, fn func(*Data)) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Data{}
	if old != nil {
		if old.destroyed || time.Now().After(old.data.Expiry) {
			panic(ErrRevoked)
		}
		d = copyData(old.data)
	}
	fn(&d)
	d.Created = time.Now().UTC()
	d.Expiry = d.Created.Add(s.ttl)
	d.CSRF = randomID()
	h := &Session{ID: randomID(), data: copyData(d), store: s}
	if s.db != nil {
		tx, err := s.db.Begin()
		if err != nil {
			panic(err)
		}
		defer tx.Rollback()
		if old != nil {
			res, e := tx.Exec(`DELETE FROM http_sessions WHERE id=?`, old.ID)
			if e != nil {
				panic(e)
			}
			n, e := res.RowsAffected()
			if e != nil {
				panic(e)
			}
			if n != 1 {
				panic(ErrRevoked)
			}
		}
		var uid any
		if u := associated(d); u != nil {
			uid = u.ID
			if s.identity {
				var v uint64
				if err = tx.QueryRow(`SELECT auth_version FROM users WHERE id=?`, u.ID).Scan(&v); err != nil {
					panic(err)
				}
				if v != u.AuthVersion {
					panic(ErrRevoked)
				}
			}
		}
		raw, err := json.Marshal(d)
		if err != nil {
			panic(err)
		}
		if _, err = tx.Exec(`INSERT INTO http_sessions(id,data,expiry,created_at,user_id) VALUES(?,?,?,?,?)`, h.ID, string(raw), d.Expiry.Format(time.RFC3339Nano), d.Created.Format(time.RFC3339Nano), uid); err != nil {
			panic(err)
		}
		if err = tx.Commit(); err != nil {
			panic(err)
		}
	}
	if old != nil {
		s.invalidateLocked(old)
	}
	s.items[h.ID] = h
	s.writeCookie(w, h.ID)
	return h
}
func (s *Store) Destroy(w http.ResponseWriter, h *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h != nil {
		if s.db != nil {
			if _, err := s.db.Exec(`DELETE FROM http_sessions WHERE id=?`, h.ID); err != nil {
				panic(err)
			}
		}
		s.invalidateLocked(h)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
func (s *Store) writeCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: id, Path: "/", MaxAge: int(s.ttl.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
func (h *Session) SetFlash(kind, text string) {
	h.Update(func(d *Data) { d.Flash = &Flash{Kind: kind, Text: text} })
}
func (h *Session) TakeFlash() *Flash {
	var f *Flash
	h.Update(func(d *Data) { f = d.Flash; d.Flash = nil })
	return f
}
func (h *Session) ValidCSRF(token string) bool {
	d := h.Snapshot()
	return d.CSRF != "" && token != "" && subtle.ConstantTimeCompare([]byte(d.CSRF), []byte(token)) == 1
}
func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
