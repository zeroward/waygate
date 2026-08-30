package web

import (
	"context"
	"io"
	"log/slog"
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

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/status"
)

func testWeb(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	return testWebKB(t, "")
}

func testWebKB(t *testing.T, kbPath string) (*httptest.Server, *Server) {
	t.Helper()
	cfg := config.Config{
		DemoMode:          true,
		RealmName:         "Icecrown",
		CoreName:          "AzerothCore WotLK 3.3.5a",
		PublicHost:        "127.0.0.1",
		PublicAuthPort:    3724,
		PublicWorldPort:   28085,
		DefaultExpansion:  2,
		SiteBlurb:         "Test realm",
		PasswordMinLength: 8,
		CaptchaProvider:   "none",
		StatusCache:       20 * time.Second,
		LeaderboardSize:   20,
		BotPrefixes:       []string{"RNDBOT"},
		RateWindow:        15 * time.Minute,
		RateRegister:      50,
		RateLogin:         50,
		RateContact:       50,
		RateReset:         50,
		RateKB:            50,
		RateTickets:       50,
		HowToConnectFile:  "",
		AccountMode:       "sql",
		DownloadsDir:      t.TempDir(),
		SiteURL:           "http://localhost",
		KBPath:            kbPath,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	st := status.New(cfg, nil, nil)
	srv, err := New(cfg, log, acc, st, captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler()), srv
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := testWeb(t)
	return ts
}

func TestHomeAndRegisterPages(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("home %d", res.StatusCode)
	}
	if !strings.Contains(string(body), "Icecrown") {
		t.Fatal("home missing realm name")
	}
	if !strings.Contains(string(body), "WotLK") {
		t.Fatal("home missing expansion badge")
	}
	if !strings.Contains(string(body), "bots") || !strings.Contains(string(body), "GMs") {
		t.Fatal("home missing bot/GM online counts")
	}
	if !strings.Contains(string(body), "Installed modules") || !strings.Contains(string(body), "Playerbots") {
		t.Fatal("home missing installed modules")
	}
	if !strings.Contains(string(body), "How to connect") || !strings.Contains(string(body), `/kb/how-to-connect`) {
		t.Fatal("home missing latest published KB card")
	}
	res2, err := http.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	b2, _ := io.ReadAll(res2.Body)
	if !strings.Contains(string(b2), `name="csrf_token"`) {
		t.Fatal("register form missing csrf")
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res3, err := noRedir.Get(ts.URL + "/downloads")
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon downloads %d", res3.StatusCode)
	}
	resKB, err := noRedir.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	resKB.Body.Close()
	if resKB.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon kb %d", resKB.StatusCode)
	}
	res4, err := noRedir.Get(ts.URL + "/connect")
	if err != nil {
		t.Fatal(err)
	}
	res4.Body.Close()
	if res4.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("anon connect %d", res4.StatusCode)
	}
	if loc := res4.Header.Get("Location"); loc != "/kb/how-to-connect" {
		t.Fatalf("connect location %s", loc)
	}
}

func TestHomeOKWithoutPublishedKB(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	art, err := srv.kb.GetBySlug(ctx, "how-to-connect")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.kb.Delete(ctx, art.ID); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("home %d", res.StatusCode)
	}
	if strings.Contains(string(body), `/kb/how-to-connect`) {
		t.Fatal("deleted article still on home")
	}
}

func TestLeaderboardsGoldTab(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()
	res, err := http.Get(ts.URL + "/leaderboards?tab=gold")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, `tab=gold`) || !strings.Contains(html, "Frostwarden") {
		t.Fatal("missing gold tab or demo row")
	}
	if !strings.Contains(html, "g") || !strings.Contains(html, "1234g") {
		t.Fatal("missing gold formatting")
	}
	if strings.Contains(html, "RNDBOT") {
		t.Fatal("bots on gold board")
	}
	if !strings.Contains(html, "/armory/Frostwarden") {
		t.Fatal("gold board should link names to Armory")
	}
}

func TestRegisterRejectsBadUsername(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	res, err := client.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))
	if csrf == "" {
		t.Fatal("no csrf")
	}

	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"ab"},
		"email":            {"a@b.com"},
		"password":         {"Abcd1234"},
		"password_confirm": {"Abcd1234"},
	}
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d body %s", res.StatusCode, out)
	}
	if !strings.Contains(string(out), "username must be") {
		t.Fatalf("expected validation error, got %s", out)
	}
}

func TestRegisterAndLoginDemo(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	res, err := client.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))

	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"HeroOne"},
		"email":            {"hero@example.com"},
		"password":         {"Abcd1234"},
		"password_confirm": {"Abcd1234"},
	}
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register status %d %s", res.StatusCode, b)
	}
}

func TestRegisterRequiresInviteKey(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.id.Store().SetRegisterKey(context.Background(), "chungus"); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	if !strings.Contains(html, `name="register_key"`) {
		t.Fatal("missing key field")
	}
	csrf := extractCSRF(html)
	form := url.Values{
		"csrf_token": {csrf}, "username": {"HeroOne"}, "email": {"h@example.com"},
		"password": {"Abcd1234"}, "password_confirm": {"Abcd1234"},
	}
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(out), "Invalid registration key") {
		t.Fatalf("no key %d %s", res.StatusCode, out)
	}
	form.Set("register_key", "wrong")
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	out, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(out), "Invalid registration key") {
		t.Fatalf("wrong key %d %s", res.StatusCode, out)
	}
	form.Set("register_key", "chungus")
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("good key %d", res.StatusCode)
	}
}

func TestStaffSetsRegisterKey(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	srv.accounts.GrantGM("Staffer", 2)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")
	res, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `id="register-key"`) {
		t.Fatal("missing staff register key form")
	}
	csrf := extractCSRF(string(body))
	res, err = client.PostForm(ts.URL+"/staff/register-key", url.Values{"csrf_token": {csrf}, "register_key": {"ice-crown"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	anon, err := http.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := io.ReadAll(anon.Body)
	anon.Body.Close()
	if !strings.Contains(string(reg), `name="register_key"`) {
		t.Fatal("register form missing key after staff set")
	}
}

func TestRegisterRequiresEmailVerifyWhenSMTP(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	var mailed string
	srv.mail.Intercept = func(to, subject, body string) error {
		if to != "hero@example.com" {
			t.Fatalf("to %s", to)
		}
		mailed = body
		return nil
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Get(ts.URL + "/register")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	form := url.Values{
		"csrf_token":       {extractCSRF(string(body))},
		"username":         {"HeroOne"},
		"email":            {"hero@example.com"},
		"password":         {"Abcd1234"},
		"password_confirm": {"Abcd1234"},
	}
	res, err = client.PostForm(ts.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register %d", res.StatusCode)
	}

	accPage, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	accBody, _ := io.ReadAll(accPage.Body)
	accPage.Body.Close()
	if strings.Contains(string(accBody), "user-name") && strings.Contains(string(accBody), "HEROONE") {
		t.Fatal("must not be logged in before verify")
	}

	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	denied, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	denBody, _ := io.ReadAll(denied.Body)
	denied.Body.Close()
	if !strings.Contains(string(denBody), "Confirm the link we emailed") {
		t.Fatalf("expected verify prompt: %s", denBody)
	}

	re := regexp.MustCompile(`/account/verify/([a-f0-9]+)`)
	m := re.FindStringSubmatch(mailed)
	if len(m) < 2 {
		t.Fatalf("no verify link in mail: %s", mailed)
	}
	res, err = client.Get(ts.URL + m[0])
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("verify %d", res.StatusCode)
	}

	follow := &http.Client{Jar: jar}
	login(t, follow, ts.URL, "HeroOne", "Abcd1234")
	ok, err := follow.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	okBody, _ := io.ReadAll(ok.Body)
	ok.Body.Close()
	if ok.StatusCode != 200 || !strings.Contains(string(okBody), "HEROONE") {
		t.Fatalf("after verify %d %s", ok.StatusCode, okBody)
	}
}

func extractCSRF(html string) string {
	re := regexp.MustCompile(`name="csrf_token" value="([a-f0-9]+)"`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func TestDownloadServesCatalogedZip(t *testing.T) {
	root := t.TempDir()
	clientDir := filepath.Join(root, "client")
	if err := os.MkdirAll(clientDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("PK fake wow client zip")
	if err := os.WriteFile(filepath.Join(clientDir, "WoW-3.3.5a.zip"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(`{
	  "intro": "Get the client.",
	  "items": [{"id":"client-335a","title":"WotLK Client","category":"client","file":"client/WoW-3.3.5a.zip","featured":true}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20, BotPrefixes: []string{"RNDBOT"},
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50,
		DownloadsDir: root, DownloadsCatalog: filepath.Join(root, "catalog.json"),
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	if err := acc.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	authed := &http.Client{Jar: jar}
	login(t, authed, ts.URL, "HeroOne", "Abcd1234")

	list, err := authed.Get(ts.URL + "/downloads?tab=client")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(list.Body)
	list.Body.Close()
	if !strings.Contains(string(body), "WotLK Client") || !strings.Contains(string(body), `/downloads/client-335a`) {
		t.Fatalf("listing: %s", body)
	}
	if !strings.Contains(string(body), `name="q"`) {
		t.Fatal("missing download search")
	}
	miss, err := authed.Get(ts.URL + "/downloads?q=no-such-file")
	if err != nil {
		t.Fatal(err)
	}
	missBody, _ := io.ReadAll(miss.Body)
	miss.Body.Close()
	if strings.Contains(string(missBody), "WotLK Client") || !strings.Contains(string(missBody), "No downloads match") {
		t.Fatalf("search miss: %s", missBody)
	}

	res, err := authed.Get(ts.URL + "/downloads/client-335a")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(got) != string(payload) {
		t.Fatalf("file status %d body %q", res.StatusCode, got)
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), "WoW-3.3.5a.zip") {
		t.Fatalf("disposition %s", res.Header.Get("Content-Disposition"))
	}

	res404, err := authed.Get(ts.URL + "/downloads/not-a-file")
	if err != nil {
		t.Fatal(err)
	}
	res404.Body.Close()
	if res404.StatusCode != 404 {
		t.Fatalf("unknown id status %d", res404.StatusCode)
	}
}
