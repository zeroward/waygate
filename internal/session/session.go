package session

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const CookieName = "waygate_session"

type User struct {
	ID       uint32
	Username string
	Email    string
	GMLevel  uint8
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

type Flash struct {
	Kind string
	Text string
}

type Session struct {
	ID          string
	User        *User
	PendingUser *User
	PendingNext string
	TOTPSecret  string
	TOTPURL     string
	TOTPQR      string
	TOTPCodes   []string
	CSRF        string
	Flash       *Flash
	Created     time.Time
	Expiry      time.Time
}

type Store struct {
	mu     sync.Mutex
	items  map[string]*Session
	ttl    time.Duration
	secure bool
	lastGC time.Time
}

func NewStore(ttl time.Duration, secure bool) *Store {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	s := &Store{
		items:  make(map[string]*Session),
		ttl:    ttl,
		secure: secure,
		lastGC: time.Now(),
	}
	go s.loop()
	return s
}

func (s *Store) loop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.gc()
	}
}

func (s *Store) gc() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.items {
		if now.After(sess.Expiry) {
			delete(s.items, id)
		}
	}
	s.lastGC = now
}

func (s *Store) GetOrCreate(w http.ResponseWriter, r *http.Request) *Session {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		s.mu.Lock()
		sess, ok := s.items[c.Value]
		s.mu.Unlock()
		if ok && time.Now().Before(sess.Expiry) {
			return sess
		}
	}
	return s.create(w)
}

func (s *Store) create(w http.ResponseWriter) *Session {
	id := randomID()
	csrf := randomID()
	sess := &Session{
		ID:      id,
		CSRF:    csrf,
		Created: time.Now(),
		Expiry:  time.Now().Add(s.ttl),
	}
	s.mu.Lock()
	s.items[id] = sess
	s.mu.Unlock()
	s.writeCookie(w, id)
	return sess
}

func (s *Store) Regenerate(w http.ResponseWriter, old *Session) *Session {
	s.mu.Lock()
	if old != nil {
		delete(s.items, old.ID)
	}
	s.mu.Unlock()
	n := s.create(w)
	if old != nil && old.User != nil {
		u := *old.User
		n.User = &u
	}
	return n
}

func (s *Store) Destroy(w http.ResponseWriter, sess *Session) {
	if sess != nil {
		s.mu.Lock()
		delete(s.items, sess.ID)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Store) writeCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(s.ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (sess *Session) SetFlash(kind, text string) {
	sess.Flash = &Flash{Kind: kind, Text: text}
}

func (sess *Session) TakeFlash() *Flash {
	f := sess.Flash
	sess.Flash = nil
	return f
}

func (sess *Session) ValidCSRF(token string) bool {
	if sess == nil || sess.CSRF == "" || token == "" {
		return false
	}
	return hmacEqual(sess.CSRF, token)
}

func hmacEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("session: entropy: " + err.Error())
	}
	return hex.EncodeToString(b)
}
