package session

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRegressionPendingSessionSurvivesRevocation(t *testing.T) {
	db := testDB(t)
	s, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p := s.GetOrCreate(rec, httptest.NewRequest("GET", "/", nil))
	p.Update(func(d *Data) { d.PendingUser = &User{ID: 7, Username: "AUDIT"} })
	s.SaveLatest(p)
	s.RevokeUser(7, "")
	if got := s.get(p.ID); got != nil {
		t.Fatal("pending session survived revocation")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM http_sessions WHERE id=? AND user_id IS NULL`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("pending session remains persisted")
	}
	t.Log("Fixed: pending authentication revoked in memory and SQLite")
}
func TestRegressionConcurrentSessionMutation(t *testing.T) {
	s, err := NewStore(nil, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p := s.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.SetFlash("info", "synthetic")
				p.TakeFlash()
			}
		}()
	}
	wg.Wait()
}
