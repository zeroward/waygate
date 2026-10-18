package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/zeroward/waygate/internal/session"
)

func regressionCookie(t *testing.T, s *Server) (*http.Cookie, uint32) {
	t.Helper()
	u, err := s.id.Store().CreateUser(context.Background(), "AUDITADMIN", "audit@example.invalid", "!migrated", 3)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s.sessions.GetOrCreate(rec, httptest.NewRequest("GET", "/", nil))
	sess.Update(func(d *session.Data) { d.User = toSiteUser(u) })
	s.sessions.SaveLatest(sess)
	return rec.Result().Cookies()[0], u.ID
}
func TestRegressionKBRetainsDemotedPrivilege(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	cookie, uid := regressionCookie(t, s)
	if err := s.id.Store().SetStaffLevel(context.Background(), uid, 2); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/kb/admin", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("demoted session authorized: %d", w.Code)
	}
	t.Log("Fixed: KB rejects demoted session")
}
func TestRegressionPendingMFACompletesAfterBan(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	u, err := s.id.Register(ctx, "AuditPlayer", "AuditPass123", "audit@example.invalid", "", "AuditPass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := s.id.Store().StartTOTP(ctx, u.ID, u.Username, "Audit")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.id.Store().ConfirmTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sess := s.sessions.GetOrCreate(rec, httptest.NewRequest("GET", "/", nil))
	u, err = s.id.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess.Update(func(d *session.Data) { d.PendingUser = toSiteUser(u); d.PendingExpiry = time.Now().Add(time.Minute) })
	csrf := sess.Snapshot().CSRF
	s.sessions.SaveLatest(sess)
	if err := s.accounts.Ban(ctx, 4, "AUDITOR", u.Username, "permanent", "fixture suspension"); err != nil {
		t.Fatal(err)
	}
	s.sessions.RevokeUser(u.ID, "")
	form := url.Values{"csrf_token": {csrf}, "code": {code}}
	req := httptest.NewRequest("POST", "/account/login/totp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(rec.Result().Cookies()[0])
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	next := httptest.NewRequest("GET", "/downloads", nil)
	for _, c := range w.Result().Cookies() {
		next.AddCookie(c)
	}
	if got := s.sessions.GetOrCreate(httptest.NewRecorder(), next); got.Snapshot().User != nil {
		t.Fatal("banned pending user authenticated")
	}
	t.Log("Fixed: pending MFA cannot complete after ban and revocation")
}

func TestActionGrantsAreExpiringPurposeBoundAndSingleUse(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	cookie, _ := regressionCookie(t, s)
	r := httptest.NewRequest("GET", "/account", nil)
	r.AddCookie(cookie)
	h := s.sessions.GetOrCreate(httptest.NewRecorder(), r)
	if s.consumeGrant(h, "passkey-add") {
		t.Fatal("unverified session has grant")
	}
	s.grant(h, "totp-start")
	if s.consumeGrant(h, "passkey-add") {
		t.Fatal("wrong action accepted")
	}
	if !s.consumeGrant(h, "totp-start") || s.consumeGrant(h, "totp-start") {
		t.Fatal("grant not single use")
	}
	s.grant(h, "passkey-add")
	h.Update(func(d *session.Data) { d.ReauthExpiry = time.Now().Add(-time.Second) })
	if s.consumeGrant(h, "passkey-add") {
		t.Fatal("expired grant accepted")
	}
	s.setChallenge(h, []byte(`{"challenge":"synthetic"}`), "reauth", "passkey-add", "")
	if _, ok := s.takeChallenge(h, "register"); ok {
		t.Fatal("wrong purpose accepted")
	}
	if _, ok := s.takeChallenge(h, "reauth"); !ok {
		t.Fatal("challenge lost")
	}
	if _, ok := s.takeChallenge(h, "reauth"); ok {
		t.Fatal("challenge replay")
	}
	s.setChallenge(h, []byte(`{}`), "register", "", "")
	h.Update(func(d *session.Data) { d.WebAuthnExpiry = time.Now().Add(-time.Second) })
	if _, ok := s.takeChallenge(h, "register"); ok {
		t.Fatal("expired challenge accepted")
	}
}
func TestMFAReadFailureDoesNotIssueLogin(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	if _, err := s.id.Register(ctx, "FailClosed", "Password123", "fc@example.invalid", "", "Password123", 2); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h := s.sessions.GetOrCreate(rec, httptest.NewRequest("GET", "/account", nil))
	if _, err := s.kb.SQL().Exec(`DROP TABLE user_totp`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf_token": {h.Snapshot().CSRF}, "username": {"FailClosed"}, "password": {"Password123"}}
	req := httptest.NewRequest("POST", "/account/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(rec.Result().Cookies()[0])
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, req)
	if out.Code != 503 || h.Snapshot().User != nil || h.Snapshot().PendingUser != nil {
		t.Fatalf("MFA failure issued auth, status=%d", out.Code)
	}
}

func TestMFACompletionRotatesTwice(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	u, err := s.id.Register(ctx, "TwoStep", "Password123", "two@example.invalid", "", "Password123", 2)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := s.id.Store().StartTOTP(ctx, u.ID, u.Username, "Test")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if _, err = s.id.Store().ConfirmTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	beforeRec := httptest.NewRecorder()
	before := s.sessions.GetOrCreate(beforeRec, httptest.NewRequest("GET", "/account", nil))
	oldCSRF := before.Snapshot().CSRF
	form := url.Values{"csrf_token": {oldCSRF}, "username": {u.Username}, "password": {"Password123"}}
	req := httptest.NewRequest("POST", "/account/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(beforeRec.Result().Cookies()[0])
	pendingRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(pendingRec, req)
	if pendingRec.Code != 303 {
		t.Fatalf("password login %d: %s", pendingRec.Code, pendingRec.Body.String())
	}
	pendingReq := httptest.NewRequest("GET", "/account", nil)
	pendingReq.AddCookie(pendingRec.Result().Cookies()[0])
	pending := s.sessions.GetOrCreate(httptest.NewRecorder(), pendingReq)
	state := pending.Snapshot()
	if pending.ID == before.ID || state.CSRF == oldCSRF || state.PendingUser == nil || state.User != nil || state.PendingExpiry.After(time.Now().Add(5*time.Minute)) {
		t.Fatal("password stage did not rotate into bounded pending state")
	}
	code, _ = totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	form = url.Values{"csrf_token": {state.CSRF}, "code": {code}}
	req = httptest.NewRequest("POST", "/account/login/totp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(pendingRec.Result().Cookies()[0])
	doneRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(doneRec, req)
	if doneRec.Code != 303 {
		t.Fatalf("MFA login %d: %s", doneRec.Code, doneRec.Body.String())
	}
	doneReq := httptest.NewRequest("GET", "/account", nil)
	doneReq.AddCookie(doneRec.Result().Cookies()[0])
	done := s.sessions.GetOrCreate(httptest.NewRecorder(), doneReq)
	if done.ID == pending.ID || done.Snapshot().CSRF == state.CSRF || done.Snapshot().User == nil || done.Snapshot().PendingUser != nil {
		t.Fatal("MFA completion did not rotate and clear pending state")
	}
	stale := s.sessions.GetOrCreate(httptest.NewRecorder(), pendingReq)
	if stale.Snapshot().User != nil || stale.Snapshot().PendingUser != nil {
		t.Fatal("pending cookie remained usable")
	}
}
func TestActiveBanBlocksProtectedRoutesWithoutExplicitRevocation(t *testing.T) {
	ts, s := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	u, err := s.id.Register(ctx, "Suspended", "Password123", "suspended@example.invalid", "", "Password123", 2)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/account", "/downloads", "/tickets", "/staff", "/kb/admin", "/account/wg/1/conf"}
	cookies := make([]*http.Cookie, len(paths))
	for i := range paths {
		rec := httptest.NewRecorder()
		h := s.sessions.GetOrCreate(rec, httptest.NewRequest("GET", "/", nil))
		h.Update(func(d *session.Data) { d.User = toSiteUser(u) })
		cookies[i] = rec.Result().Cookies()[0]
	}
	if err = s.accounts.Ban(ctx, 4, "ADMIN", u.Username, "permanent", "fixture ban"); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(cookies[i])
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, req)
		if out.Code != 401 {
			t.Errorf("%s allowed banned session: %d", path, out.Code)
		}
	}
}
