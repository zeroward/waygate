package session

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatal(err)
	}
	return db
}

func cookieReq(rec *httptest.ResponseRecorder, path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func TestSessionPersistsAcrossStoreReopen(t *testing.T) {
	db := testDB(t)
	s1, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s1.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	sess.Update(func(d *Data) { d.User = &User{ID: 7, Username: "HEROONE", Email: "h@example.com", GMLevel: 2} })
	sess.SetFlash("success", "welcome")
	s1.SaveLatest(sess)

	s2, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.GetOrCreate(httptest.NewRecorder(), cookieReq(rec, "/"))
	if got.Snapshot().User == nil || got.Snapshot().User.ID != 7 || got.Snapshot().User.Username != "HEROONE" || got.Snapshot().User.GMLevel != 2 {
		t.Fatalf("user %+v", got.Snapshot().User)
	}
	if got.Snapshot().CSRF == "" || got.Snapshot().CSRF != sess.Snapshot().CSRF {
		t.Fatal("csrf")
	}
	f := got.TakeFlash()
	if f == nil || f.Text != "welcome" {
		t.Fatalf("flash %+v", f)
	}
	s2.SaveLatest(got)

	s3, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	again := s3.GetOrCreate(httptest.NewRecorder(), cookieReq(rec, "/"))
	if again.TakeFlash() != nil {
		t.Fatal("flash should be consumed")
	}
}

func TestCredentialKeyNotPersisted(t *testing.T) {
	db := testDB(t)
	s1, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s1.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	sess.Update(func(d *Data) { d.User = &User{ID: 7, Username: "HEROONE"} })
	sess.Update(func(d *Data) { d.CredentialKey = []byte("0123456789abcdef0123456789abcdef") })
	s1.SaveLatest(sess)

	s2, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.GetOrCreate(httptest.NewRecorder(), cookieReq(rec, "/"))
	if len(got.Snapshot().CredentialKey) != 0 {
		t.Fatal("DEK must not persist in sqlite")
	}
	var blob string
	if err := db.QueryRow(`SELECT data FROM http_sessions WHERE id = ?`, sess.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "0123456789abcdef") || strings.Contains(blob, "CredentialKey") {
		t.Fatalf("session json leaked key: %s", blob)
	}
}

func TestRevokeUserLeavesCurrent(t *testing.T) {
	db := testDB(t)
	s, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec1 := httptest.NewRecorder()
	a := s.GetOrCreate(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	a.Update(func(d *Data) { d.User = &User{ID: 9, Username: "HERO"} })
	s.SaveLatest(a)
	rec2 := httptest.NewRecorder()
	b := s.GetOrCreate(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	b.Update(func(d *Data) { d.User = &User{ID: 9, Username: "HERO"} })
	s.SaveLatest(b)
	s.RevokeUser(9, a.ID)
	if s.GetOrCreate(httptest.NewRecorder(), cookieReq(rec2, "/")).Snapshot().User != nil {
		t.Fatal("other session still live")
	}
	keep := s.GetOrCreate(httptest.NewRecorder(), cookieReq(rec1, "/"))
	if keep.Snapshot().User == nil || keep.Snapshot().User.ID != 9 {
		t.Fatal("current session revoked")
	}
}

func TestSessionRegenerateInvalidatesOldID(t *testing.T) {
	db := testDB(t)
	s, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	old := s.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	old.Update(func(d *Data) { d.User = &User{ID: 1, Username: "HEROONE"} })
	oldID := old.ID
	rec2 := httptest.NewRecorder()
	n := s.Regenerate(rec2, old)
	s.SaveLatest(old)
	if n.Snapshot().User == nil || n.Snapshot().User.Username != "HEROONE" {
		t.Fatal("user not copied")
	}
	if n.ID == oldID {
		t.Fatal("id reused")
	}
	s2, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	stale := httptest.NewRequest(http.MethodGet, "/", nil)
	stale.AddCookie(&http.Cookie{Name: CookieName, Value: oldID})
	got := s2.GetOrCreate(httptest.NewRecorder(), stale)
	if got.ID == oldID && got.Snapshot().User != nil {
		t.Fatal("old session still live")
	}
	fresh := s2.GetOrCreate(httptest.NewRecorder(), cookieReq(rec2, "/"))
	if fresh.Snapshot().User == nil || fresh.Snapshot().User.Username != "HEROONE" {
		t.Fatalf("new sess %+v", fresh.Snapshot().User)
	}
}

func TestExpiredSessionNotLoaded(t *testing.T) {
	db := testDB(t)
	s, err := NewStore(db, 20*time.Millisecond, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	sess.Update(func(d *Data) { d.User = &User{ID: 1, Username: "HEROONE"} })
	s.SaveLatest(sess)
	time.Sleep(40 * time.Millisecond)
	s2, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.GetOrCreate(httptest.NewRecorder(), cookieReq(rec, "/"))
	if got.Snapshot().User != nil {
		t.Fatal("expired session reused")
	}
}

func TestDestroyRemovesRow(t *testing.T) {
	db := testDB(t)
	s, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s.GetOrCreate(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	sess.Update(func(d *Data) { d.User = &User{ID: 1, Username: "HEROONE"} })
	s.SaveLatest(sess)
	id := sess.ID
	s.Destroy(httptest.NewRecorder(), sess)
	s2, err := NewStore(db, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: id})
	got := s2.GetOrCreate(httptest.NewRecorder(), req)
	if got.Snapshot().User != nil {
		t.Fatal("destroyed session loaded")
	}
}
