package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/ratelimit"
	"github.com/zeroward/waygate/internal/session"
	"github.com/zeroward/waygate/internal/sso"
	"github.com/zeroward/waygate/internal/ssotest"
)

type ssoBrowser struct {
	t        *testing.T
	app      *Server
	server   *httptest.Server
	provider *ssotest.Provider
	client   *http.Client
}
type browserResult struct {
	code           int
	body, location string
	header         http.Header
}

func newSSOBrowser(t *testing.T) *ssoBrowser {
	t.Helper()
	ts, app := testWeb(t)
	t.Cleanup(func() { ts.Close(); _ = app.kb.Close() })
	p := ssotest.New(t)
	path := filepath.Join(t.TempDir(), "keys.json")
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	if err := os.WriteFile(path, []byte(`{"active":"test","keys":{"test":"`+key+`"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := identity.LoadVault(path)
	if err != nil {
		t.Fatal(err)
	}
	app.vault = v
	app.cfg.SSOEnabled = true
	app.cfg.OIDCDisplayName = "Test Identity"
	app.cfg.NetBirdSetupURL = "https://network.example/enroll"
	app.oidc = sso.New(p.URL(), "test-client", "test-secret", ts.URL+"/auth/oidc/callback")
	app.loginRL = ratelimit.New(time.Minute, 500)
	jar, _ := cookiejar.New(nil)
	b := &ssoBrowser{t: t, app: app, server: ts, provider: p, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	b.get("/account")
	return b
}
func (b *ssoBrowser) request(method, path string, form url.Values) browserResult {
	b.t.Helper()
	if strings.HasPrefix(path, "/") {
		path = b.server.URL + path
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return browserResult{res.StatusCode, string(raw), res.Header.Get("Location"), res.Header}
}
func (b *ssoBrowser) get(path string) browserResult { return b.request("GET", path, nil) }
func (b *ssoBrowser) handle() *session.Session {
	req := httptest.NewRequest("GET", b.server.URL+"/account", nil)
	u, _ := url.Parse(b.server.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	return b.app.sessions.GetOrCreate(httptest.NewRecorder(), req)
}
func (b *ssoBrowser) post(path string, form url.Values) browserResult {
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf_token") {
		form.Set("csrf_token", b.handle().Snapshot().CSRF)
	}
	return b.request("POST", path, form)
}
func (b *ssoBrowser) complete(start browserResult) browserResult {
	b.t.Helper()
	dest := start.location
	if start.code == 200 {
		match := regexp.MustCompile(`id="sso-continue" href="([^"]+)"`).FindStringSubmatch(start.body)
		if len(match) != 2 {
			b.t.Fatalf("missing SSO continuation link: %s", start.body)
		}
		dest = html.UnescapeString(match[1])
		if !strings.Contains(start.header.Get("Content-Security-Policy"), "form-action 'self'") {
			b.t.Fatal("SSO weakened form-action policy")
		}
	} else if start.code != 303 {
		b.t.Fatalf("start %d: %s", start.code, start.body)
	}
	callback := ssotest.Authorize(b.t, dest)
	return b.get(callback)
}
func (b *ssoBrowser) registerLocal(name string) identity.User {
	b.t.Helper()
	u, err := b.app.id.Register(context.Background(), name, "Website123", name+"@example.test", "", "GamePass123", 2)
	if err != nil {
		b.t.Fatal(err)
	}
	return u
}
func (b *ssoBrowser) localLogin(name string) {
	b.t.Helper()
	res := b.post("/account/login", url.Values{"username": {name}, "password": {"Website123"}})
	if res.code != 303 {
		b.t.Fatalf("local login %d", res.code)
	}
}
func (b *ssoBrowser) link(name string) {
	b.t.Helper()
	res := b.complete(b.post("/account/sso/link", url.Values{"username": {name}, "current_password": {"Website123"}}))
	if res.code != 303 || res.location != "/auth/oidc/onboard" {
		b.t.Fatalf("link callback: %+v", res)
	}
	res = b.post("/account/sso/confirm", nil)
	if res.code != 303 {
		b.t.Fatalf("link: %+v", res)
	}
}
func (b *ssoBrowser) reauth(purpose string) browserResult {
	return b.complete(b.post("/account/sso/reauth", url.Values{"purpose": {purpose}}))
}
func TestSSOBrowserLinkAndGameCredentials(t *testing.T) {
	b := newSSOBrowser(t)
	ctx := context.Background()
	u := b.registerLocal("LINKHERO")
	if err := b.app.id.Store().SetStaffLevel(ctx, u.ID, 3); err != nil {
		t.Fatal(err)
	}
	b.localLogin(u.Username)
	oldSession := b.handle()
	oldData := oldSession.Snapshot()
	oldCookie := &http.Cookie{Name: session.CookieName, Value: oldSession.ID}
	token, err := b.app.accounts.IssueResetToken(u.Username)
	if err != nil {
		t.Fatal(err)
	}
	b.link(u.Username)
	current, err := b.app.id.GetByID(ctx, u.ID)
	if err != nil || current.AuthMode != identity.AuthSSO || current.StaffLevel != 3 {
		t.Fatalf("conversion: %+v %v", current, err)
	}
	if b.handle().Snapshot().AuthMethod != identity.AuthSSO {
		t.Fatal("not an SSO session")
	}
	body := b.get("/account")
	if body.code != 200 {
		t.Fatal(body)
	}
	for _, text := range []string{`action="/account/password"`, `id="totp"`, `id="passkeys"`, `value="GamePass123"`} {
		if strings.Contains(body.body, text) {
			t.Fatalf("local controls or locked secret shown: %s", text)
		}
	}
	if !strings.Contains(body.body, "https://network.example/enroll") {
		t.Fatal("NetBird link missing")
	}
	req := httptest.NewRequest("GET", "/downloads", nil)
	req.AddCookie(oldCookie)
	w := httptest.NewRecorder()
	b.app.Handler().ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatal("old local session still authorized")
	}
	if res := b.reauth("wow-reveal"); res.code != 303 {
		t.Fatal(res)
	}
	body = b.get("/account")
	if !strings.Contains(body.body, `value="GamePass123"`) {
		t.Fatal("saved game password did not survive linking")
	}
	b.handle().Update(func(d *session.Data) { d.CredentialExpiry = time.Now().Add(-time.Second) })
	if strings.Contains(b.get("/account").body, `value="GamePass123"`) {
		t.Fatal("expired reveal still shows secret")
	}
	if res := b.reauth("wow-create"); res.code != 303 {
		t.Fatal(res)
	}
	res := b.post("/account/wow", url.Values{"wow_username": {"EXTRAHERO"}, "wow_password": {"ExtraPass123"}, "wow_password_confirm": {"ExtraPass123"}})
	if res.code != 303 {
		t.Fatal(res)
	}
	if _, err = b.app.accounts.Authenticate(ctx, "EXTRAHERO", "ExtraPass123"); err != nil {
		t.Fatal("SSO game create", err)
	}
	if len(b.app.gameKey(b.handle(), "wow-create", false)) != 0 {
		t.Fatal("creation grant was not consumed")
	}
	if res = b.reauth("wow-password"); res.code != 303 {
		t.Fatal(res)
	}
	res = b.post("/account/wow/password", url.Values{"wow_username": {u.Username}, "wow_password": {"Changed123"}, "wow_password_confirm": {"Changed123"}})
	if res.code != 303 {
		t.Fatal(res)
	}
	if _, err = b.app.accounts.Authenticate(ctx, u.Username, "Changed123"); err != nil {
		t.Fatal("game reset failed", err)
	}
	if len(b.app.gameKey(b.handle(), "wow-password", false)) != 0 {
		t.Fatal("password grant was not consumed")
	}
	for _, path := range []string{"/account/password", "/account/login", "/account/login/totp", "/account/totp/start", "/account/totp/confirm", "/account/totp/disable", "/account/passkey/register/begin", "/account/passkey/register/finish", "/account/passkey/delete", "/account/passkey/login/begin", "/account/passkey/login/finish", "/account/reauth/password", "/account/reauth/passkey/begin", "/account/reauth/passkey/finish", "/account/wow/unlock", "/account/reset"} {
		res = b.post(path, url.Values{"current_password": {"Website123"}})
		if res.code != 403 {
			t.Fatalf("local path %s: %d", path, res.code)
		}
	}
	// Reset tokens issued before linking cannot restore local auth, even anonymously.
	b.app.cfg.SMTPHost = "test"
	b.app.cfg.SMTPFrom = "test@example.test"
	b.post("/account/logout", nil)
	b.get("/account")
	res = b.post("/account/reset/"+token, url.Values{"new_password": {"Restored123"}, "new_password_confirm": {"Restored123"}})
	if _, err = b.app.id.Authenticate(ctx, u.Username, "Restored123"); err == nil {
		t.Fatal("old reset token restored local access")
	}
	res = b.post("/account/login", url.Values{"username": {u.Username}, "password": {"Website123"}})
	if b.handle().Snapshot().User != nil {
		t.Fatal("local password logged in after linking")
	}
	if oldData.AuthMethod == identity.AuthSSO {
		t.Fatal("test did not begin with a local session")
	}
	res = b.complete(b.get("/auth/oidc/start"))
	if res.code != 303 || b.handle().Snapshot().User == nil || b.handle().Snapshot().PendingUser != nil {
		t.Fatal("SSO login incorrectly requires local MFA", res)
	}
}
func TestSSOSignupInviteIdentityAndNoAutomaticEmailMatching(t *testing.T) {
	b := newSSOBrowser(t)
	b.app.cfg.RegisterKey = "invite-only"
	local := b.registerLocal("EXISTING")
	b.provider.SetIdentity("new-subject", local.Email, true)
	res := b.complete(b.get("/auth/oidc/start?next=//evil.example"))
	if res.location != "/auth/oidc/onboard" || b.handle().Snapshot().User != nil {
		t.Fatal("email auto linked", res)
	}
	res = b.post("/auth/oidc/signup", url.Values{"username": {"SSONEW"}, "register_key": {"wrong"}})
	if _, err := b.app.id.GetByUsername(context.Background(), "SSONEW"); err == nil {
		t.Fatal("invite bypass")
	}
	res = b.post("/auth/oidc/signup", url.Values{"username": {"SSONEW"}, "register_key": {"invite-only"}})
	if !strings.Contains(b.get("/auth/oidc/onboard").body, "Link your existing account") {
		t.Fatal("email collision not handled", res)
	}
	b.provider.SetIdentity("new-subject", "new@example.test", false)
	b.complete(b.get("/auth/oidc/start"))
	b.post("/auth/oidc/signup", url.Values{"username": {"SSONEW"}, "register_key": {"invite-only"}})
	if _, err := b.app.id.GetByUsername(context.Background(), "SSONEW"); err == nil {
		t.Fatal("unverified email accepted")
	}
	b.provider.SetIdentity("new-subject", "new@example.test", true)
	b.complete(b.get("/auth/oidc/start"))
	res = b.post("/auth/oidc/signup", url.Values{"username": {"SSONEW"}, "register_key": {"invite-only"}})
	if res.code != 303 || b.handle().Snapshot().User == nil {
		t.Fatalf("signup failed %+v", res)
	}
	u, err := b.app.id.GetByUsername(context.Background(), "SSONEW")
	if err != nil || u.StaffLevel != 0 {
		t.Fatalf("signup %+v %v", u, err)
	}
	links, err := b.app.id.Links(context.Background(), u.ID)
	if err != nil || len(links) != 0 {
		t.Fatal("signup created game account")
	}
	b.post("/account/logout", nil)
	// Changing profile email must not change an already-bound account or identity.
	b.provider.SetIdentity("new-subject", "changed@example.test", true)
	res = b.complete(b.get("/auth/oidc/start?next=https://evil.example"))
	if res.location != "/account" || b.handle().Snapshot().User.ID != u.ID {
		t.Fatal("identity was not stable", res)
	}
	if b.handle().Snapshot().User.Email != u.Email {
		t.Fatal("provider overwrote local email")
	}
}
func TestSSOStateReplayCancellationAndProofBinding(t *testing.T) {
	b := newSSOBrowser(t)
	start := b.get("/auth/oidc/start")
	callback := ssotest.Authorize(t, start.location)
	q, _ := url.Parse(callback)
	wrong := *q
	v := wrong.Query()
	v.Set("state", "wrong")
	wrong.RawQuery = v.Encode()
	before := b.provider.TokenCount()
	if res := b.get(wrong.String()); res.code != 400 {
		t.Fatal(res)
	}
	if b.provider.TokenCount() != before {
		t.Fatal("wrong state exchanged token")
	}
	if res := b.get(callback); res.code != 303 {
		t.Fatal(res)
	}
	before = b.provider.TokenCount()
	if res := b.get(callback); res.code != 400 {
		t.Fatal("callback replay accepted", res)
	}
	if b.provider.TokenCount() != before {
		t.Fatal("replay reached token endpoint")
	}
	b.provider.Fault("cancel")
	if res := b.complete(b.get("/auth/oidc/start")); res.code != 400 {
		t.Fatal("cancellation accepted", res)
	}
	b.provider.Fault("")
	u := b.registerLocal("PROOFHERO")
	b.link(u.Username)
	b.provider.SetIdentity("wrong-identity", "other@example.test", true)
	if res := b.reauth("wow-reveal"); res.code != 401 {
		t.Fatal("wrong identity granted proof", res)
	}
	if len(b.app.gameKey(b.handle(), "wow-reveal", false)) != 0 {
		t.Fatal("wrong identity unlocked credentials")
	}
	b.provider.SetIdentity("subject-1", "sso@example.test", true)
	if res := b.reauth("wow-create"); res.code != 303 {
		t.Fatal(res)
	}
	if key := b.app.gameKey(b.handle(), "wow-reveal", false); len(key) != 0 {
		t.Fatal("create grant exposed passwords")
	}
	if key := b.app.gameKey(b.handle(), "wow-password", true); len(key) != 0 {
		t.Fatal("wrong purpose accepted")
	}
	b.provider.Fault("token-error")
	if res := b.reauth("wow-reveal"); res.code != 503 && res.code != 401 {
		t.Fatal("outage granted access", res)
	}
}
func TestSSOLinkRequiresMFAAndFreshUnchangedAccount(t *testing.T) {
	b := newSSOBrowser(t)
	ctx := context.Background()
	u := b.registerLocal("MFAHERO")
	secret, _, err := b.app.id.Store().StartTOTP(ctx, u.ID, u.Username, "test")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	recovery, err := b.app.id.Store().ConfirmTOTP(ctx, u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	res := b.post("/account/sso/link", url.Values{"username": {u.Username}, "current_password": {"Website123"}})
	if res.code != 401 {
		t.Fatal("link bypassed MFA", res)
	}
	res = b.post("/account/sso/link", url.Values{"username": {u.Username}, "current_password": {"Website123"}, "code": {recovery[0]}})
	res = b.complete(res)
	if res.code != 303 {
		t.Fatal(res)
	}
	if err = b.app.id.SetPassword(ctx, u.Username, "NewLocal123"); err != nil {
		t.Fatal(err)
	}
	res = b.post("/account/sso/confirm", nil)
	if res.code != 401 {
		t.Fatal("stale proof linked", res)
	}
	current, err := b.app.id.GetByID(ctx, u.ID)
	if err != nil || current.AuthMode != identity.AuthLocal {
		t.Fatal("stale conversion", err)
	}
}
func TestSSOSessionsAndLinkKeysDoNotPersistSecrets(t *testing.T) {
	b := newSSOBrowser(t)
	u := b.registerLocal("SECRETTEST")
	b.post("/account/sso/link", url.Values{"username": {u.Username}, "current_password": {"Website123"}})
	flow := b.handle().Snapshot().OIDC
	if len(flow.LinkKey) != 32 {
		t.Fatal("test has no link key")
	}
	var raw string
	if err := b.app.kb.SQL().QueryRow(`SELECT data FROM http_sessions WHERE id=?`, b.handle().ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, base64.StdEncoding.EncodeToString(flow.LinkKey)) || strings.Contains(raw, "test-access-token") {
		t.Fatal("credential material persisted")
	}
	var restored session.Data
	if err := json.Unmarshal([]byte(raw), &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.OIDC.LinkKey) != 0 {
		t.Fatal("restored linking key")
	}
	// Simulate restart during linking: provider proof alone must not complete it.
	b.handle().Update(func(d *session.Data) { d.OIDC.LinkKey = nil })
	auth := b.post("/account/sso/link", url.Values{"username": {u.Username}, "current_password": {"Website123"}})
	b.handle().Update(func(d *session.Data) { d.OIDC.LinkKey = nil })
	if res := b.complete(auth); res.code != 401 {
		t.Fatal("link survived without local key", res)
	}
}
func TestSSOBansAndRemovedVPNRoutes(t *testing.T) {
	b := newSSOBrowser(t)
	u := b.registerLocal("BANHERO")
	b.link(u.Username)
	if err := b.app.accounts.Ban(context.Background(), 4, "ADMIN", u.Username, "permanent", "test suspension"); err != nil {
		t.Fatal(err)
	}
	if res := b.get("/account"); res.code != 401 {
		t.Fatal("banned session authorized", res)
	}
	b.get("/account")
	if res := b.complete(b.get("/auth/oidc/start")); res.code != 401 {
		t.Fatal("banned SSO login authorized", res)
	}
	for _, path := range []string{"/account/wg/1/conf", "/account/wg/1/zip", "/staff/wg"} {
		if res := b.get(path); res.code != 404 {
			t.Fatalf("retired route %s: %d", path, res.code)
		}
	}
	for _, path := range []string{"/account/wg", "/account/wg/1/delete", "/staff/wg"} {
		if res := b.post(path, nil); res.code != 404 && res.code != 405 {
			t.Fatalf("retired route %s: %d", path, res.code)
		}
	}
}

func TestSSOLinkingCSRFAndExpiredTransactions(t *testing.T) {
	b := newSSOBrowser(t)
	u := b.registerLocal("CSRFHERO")
	if res := b.post("/account/sso/link", url.Values{"csrf_token": {"wrong"}, "username": {u.Username}, "current_password": {"Website123"}}); res.code != 403 {
		t.Fatal("link CSRF bypass", res)
	}
	start := b.get("/auth/oidc/start")
	callback := ssotest.Authorize(t, start.location)
	b.handle().Update(func(d *session.Data) { d.OIDC.Expires = time.Now().Add(-time.Second) })
	before := b.provider.TokenCount()
	if res := b.get(callback); res.code != 400 {
		t.Fatal("expired callback accepted", res)
	}
	if b.provider.TokenCount() != before {
		t.Fatal("expired callback exchanged tokens")
	}
	res := b.complete(b.post("/account/sso/link", url.Values{"username": {u.Username}, "current_password": {"Website123"}}))
	if res.code != 303 {
		t.Fatal(res)
	}
	if res = b.post("/account/sso/confirm", url.Values{"csrf_token": {"wrong"}}); res.code != 403 {
		t.Fatal("confirmation CSRF bypass", res)
	}
	b.handle().Update(func(d *session.Data) { d.OIDC.Expires = time.Now().Add(-time.Second) })
	if res = b.post("/account/sso/confirm", nil); res.code != 400 {
		t.Fatal("expired confirmation accepted", res)
	}
	current, err := b.app.id.GetByID(context.Background(), u.ID)
	if err != nil || current.AuthMode != identity.AuthLocal {
		t.Fatal("expired flow modified account")
	}
}

func TestSSORejectsCurrentVersionLocalAndPendingSessions(t *testing.T) {
	b := newSSOBrowser(t)
	u := b.registerLocal("MODEHERO")
	b.link(u.Username)
	current, err := b.app.id.GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, pending := range []bool{false, true} {
		rec := httptest.NewRecorder()
		h := b.app.sessions.NewAnonymous(rec)
		h.Update(func(d *session.Data) {
			if pending {
				d.PendingUser = toSiteUser(current)
				d.PendingExpiry = time.Now().Add(time.Minute)
			} else {
				d.User = toSiteUser(current)
			}
		})
		req := httptest.NewRequest("GET", "/account", nil)
		req.AddCookie(rec.Result().Cookies()[0])
		w := httptest.NewRecorder()
		b.app.Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("local session for SSO identity authorized (pending=%v): %d", pending, w.Code)
		}
	}
}
