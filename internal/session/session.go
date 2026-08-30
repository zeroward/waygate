package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	ID            string
	User          *User
	PendingUser   *User
	PendingNext   string
	TOTPSecret    string   `json:"-"`
	TOTPURL       string   `json:"-"`
	TOTPQR        string   `json:"-"`
	TOTPCodes     []string `json:"-"`
	WebAuthnJSON  []byte
	WebAuthnName  string
	WebAuthnNext  string
	CredentialKey []byte `json:"-"`
	CSRF          string
	Flash         *Flash
	Created       time.Time
	Expiry        time.Time

	store      *Store
	replacedBy *Session
	destroyed  bool
}

type Store struct {
	mu     sync.Mutex
	items  map[string]*Session
	db     *sql.DB
	ttl    time.Duration
	secure bool
	lastGC time.Time
}

func NewStore(db *sql.DB, ttl time.Duration, secure bool) (*Store, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	s := &Store{
		items:  make(map[string]*Session),
		db:     db,
		ttl:    ttl,
		secure: secure,
		lastGC: time.Now(),
	}
	if db != nil {
		if err := migrate(db); err != nil {
			return nil, err
		}
	}
	go s.loop()
	return s, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS http_sessions (
  id TEXT PRIMARY KEY,
  data TEXT NOT NULL,
  expiry TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_http_sessions_expiry ON http_sessions(expiry);
`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE http_sessions ADD COLUMN user_id INTEGER`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_http_sessions_user ON http_sessions(user_id)`)
	return nil
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
	for id, sess := range s.items {
		if now.After(sess.Expiry) {
			delete(s.items, id)
		}
	}
	s.lastGC = now
	s.mu.Unlock()
	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM http_sessions WHERE expiry < ?`, now.UTC().Format(time.RFC3339))
	}
}

func (s *Store) GetOrCreate(w http.ResponseWriter, r *http.Request) *Session {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if sess := s.get(c.Value); sess != nil {
			return sess
		}
	}
	return s.create(w)
}

func (s *Store) get(id string) *Session {
	now := time.Now()
	s.mu.Lock()
	if sess, ok := s.items[id]; ok {
		s.mu.Unlock()
		if now.After(sess.Expiry) || sess.destroyed {
			return nil
		}
		return sess
	}
	s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	var blob, expiry string
	err := s.db.QueryRow(`SELECT data, expiry FROM http_sessions WHERE id = ?`, id).Scan(&blob, &expiry)
	if err != nil {
		return nil
	}
	exp, err := time.Parse(time.RFC3339, expiry)
	if err != nil || now.After(exp) {
		_, _ = s.db.Exec(`DELETE FROM http_sessions WHERE id = ?`, id)
		return nil
	}
	sess := &Session{store: s}
	if err := json.Unmarshal([]byte(blob), sess); err != nil {
		return nil
	}
	sess.ID = id
	sess.store = s
	if sess.Expiry.IsZero() {
		sess.Expiry = exp
	}
	s.mu.Lock()
	if existing, ok := s.items[id]; ok {
		s.mu.Unlock()
		return existing
	}
	s.items[id] = sess
	s.mu.Unlock()
	return sess
}

func (s *Store) create(w http.ResponseWriter) *Session {
	id := randomID()
	csrf := randomID()
	sess := &Session{
		ID:      id,
		CSRF:    csrf,
		Created: time.Now().UTC(),
		Expiry:  time.Now().UTC().Add(s.ttl),
		store:   s,
	}
	s.mu.Lock()
	s.items[id] = sess
	s.mu.Unlock()
	s.save(sess)
	s.writeCookie(w, id)
	return sess
}

func (s *Store) SaveLatest(sess *Session) {
	for sess != nil && sess.replacedBy != nil {
		sess = sess.replacedBy
	}
	if sess == nil || sess.destroyed {
		return
	}
	s.save(sess)
}

func (s *Store) save(sess *Session) {
	if s.db == nil || sess == nil || sess.destroyed {
		return
	}
	blob, err := json.Marshal(sess)
	if err != nil {
		return
	}
	var uid any
	if sess.User != nil {
		uid = sess.User.ID
	}
	_, _ = s.db.Exec(`INSERT INTO http_sessions (id, data, expiry, created_at, user_id) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data, expiry = excluded.expiry, user_id = excluded.user_id`,
		sess.ID, string(blob), sess.Expiry.UTC().Format(time.RFC3339), sess.Created.UTC().Format(time.RFC3339), uid)
}

func (s *Store) RevokeUser(userID uint32, except string) {
	if userID == 0 {
		return
	}
	s.mu.Lock()
	for id, sess := range s.items {
		if id == except || sess == nil || sess.User == nil || sess.User.ID != userID {
			continue
		}
		sess.destroyed = true
		delete(s.items, id)
	}
	s.mu.Unlock()
	if s.db == nil {
		return
	}
	if except == "" {
		_, _ = s.db.Exec(`DELETE FROM http_sessions WHERE user_id = ?`, userID)
		return
	}
	_, _ = s.db.Exec(`DELETE FROM http_sessions WHERE user_id = ? AND id != ?`, userID, except)
}

func (s *Store) deleteID(id string) {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM http_sessions WHERE id = ?`, id)
	}
}

func (s *Store) Regenerate(w http.ResponseWriter, old *Session) *Session {
	n := s.create(w)
	if old != nil {
		if old.User != nil {
			u := *old.User
			n.User = &u
		}
		if len(old.CredentialKey) > 0 {
			n.CredentialKey = append([]byte(nil), old.CredentialKey...)
		}
		old.replacedBy = n
		old.destroyed = true
		s.deleteID(old.ID)
		s.save(n)
	}
	return n
}

func (s *Store) Destroy(w http.ResponseWriter, sess *Session) {
	if sess != nil {
		sess.destroyed = true
		s.deleteID(sess.ID)
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
