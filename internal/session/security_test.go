package session

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func expectRevoked(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != ErrRevoked {
			t.Errorf("want revoked, got %v", r)
		}
	}()
	fn()
}
func TestPendingRevocationAndNoResurrection(t *testing.T) {
	db := testDB(t)
	s, _ := NewStore(db, time.Hour, false)
	rec := httptest.NewRecorder()
	h := s.GetOrCreate(rec, httptest.NewRequest("GET", "/", nil))
	h.Update(func(d *Data) { d.PendingUser = &User{ID: 7}; d.PendingExpiry = time.Now().Add(time.Minute) })
	other, _ := NewStore(db, time.Hour, false)
	stale := other.GetOrCreate(httptest.NewRecorder(), cookieReq(rec, "/"))
	s.RevokeUser(7, "")
	if s.get(h.ID) != nil {
		t.Fatal("pending session survived")
	}
	expectRevoked(t, func() { h.SetFlash("info", "late") })
	expectRevoked(t, func() { stale.SetFlash("info", "late") })
	expectRevoked(t, func() { other.Regenerate(httptest.NewRecorder(), stale) })
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM http_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatal("session resurrected", err)
	}
}
func TestSnapshotsDetachedAndConcurrentUpdates(t *testing.T) {
	s, _ := NewStore(nil, time.Hour, false)
	h := s.GetOrCreate(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	h.Update(func(d *Data) { d.User = &User{ID: 7}; d.CredentialKey = []byte{1, 2}; d.WebAuthnJSON = []byte{3} })
	d := h.Snapshot()
	d.User.ID = 9
	d.CredentialKey[0] = 9
	d.WebAuthnJSON[0] = 9
	if got := h.Snapshot(); got.User.ID != 7 || got.CredentialKey[0] != 1 || got.WebAuthnJSON[0] != 3 {
		t.Fatal("snapshot aliases canonical data")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				h.SetFlash("info", "fixture")
				h.TakeFlash()
				h.Snapshot()
			}
		}()
	}
	wg.Wait()
	oldCSRF := h.Snapshot().CSRF
	fresh := s.Regenerate(httptest.NewRecorder(), h)
	if fresh.ID == h.ID || fresh.Snapshot().CSRF == oldCSRF {
		t.Fatal("transition did not rotate")
	}
	expectRevoked(t, func() { h.Update(func(d *Data) { d.User = &User{ID: 7} }) })
}
func TestDurableAuthVersionRejectsCachedUpdates(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY,auth_version INTEGER); INSERT INTO users VALUES(7,0)`); err != nil {
		t.Fatal(err)
	}
	s, _ := NewStore(db, time.Hour, false)
	h := s.GetOrCreate(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	h.Update(func(d *Data) { d.User = &User{ID: 7} })
	if _, err := db.Exec(`UPDATE users SET auth_version=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	expectRevoked(t, func() { h.SetFlash("info", "stale") })
	expectRevoked(t, func() { s.Regenerate(httptest.NewRecorder(), h) })
}

func TestOldRequestCannotFollowAnotherRequestsLogin(t *testing.T) {
	s, _ := NewStore(nil, time.Hour, false)
	rec := httptest.NewRecorder()
	oldRequest := s.Bind(rec, httptest.NewRequest("GET", "/", nil))
	old := s.GetOrCreate(rec, oldRequest)
	s.Transition(httptest.NewRecorder(), old, func(d *Data) { d.User = &User{ID: 7} })
	expectRevoked(t, func() { s.GetOrCreate(httptest.NewRecorder(), oldRequest) })
}
