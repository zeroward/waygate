package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/status"
)

func staffTestServer(t *testing.T) (*httptest.Server, *account.Service) {
	t.Helper()
	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20,
		BotPrefixes: []string{"RNDBOT"}, GMMinLevel: 1,
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50, RateKB: 50, RateTickets: 50,
		DownloadsDir: t.TempDir(), AccountMode: "sql",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler()), acc
}

func TestStaffForbiddenForPlayers(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	res, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestStaffCreateAndReset(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")

	res, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || (!strings.Contains(string(body), "STAFFER") && !strings.Contains(string(body), "Staffer")) {
		t.Fatalf("staff page %d %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "Admin panel") {
		t.Fatal("missing Admin panel title")
	}
	if strings.Contains(string(body), ">Staff<") {
		t.Fatal("staff label should be Admin panel")
	}
	if !strings.Contains(string(body), "Create account") || !strings.Contains(string(body), "Reset password") {
		t.Fatal("missing staff forms")
	}
	if !strings.Contains(string(body), "Registration key") {
		t.Fatal("missing registration key form")
	}
	if !strings.Contains(string(body), "Set rank") {
		t.Fatal("missing rank form")
	}
	if !strings.Contains(string(body), "Select an account to manage it") {
		t.Fatal("missing empty selection state")
	}
	if !strings.Contains(string(body), "/static/js/staff.js") {
		t.Fatal("missing staff.js")
	}
	if strings.Count(string(body), "<label>Username") != 1 {
		t.Fatal("reset must not ask for a username")
	}
	csrf := extractCSRF(string(body))

	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"NewPlayer"},
		"email":            {"n@example.com"},
		"password":         {"Abcd1234"},
		"password_confirm": {"Abcd1234"},
	}
	res, err = client.PostForm(ts.URL+"/staff/create", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("create %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/staff?select=NEWPLAYER")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	csrf = extractCSRF(string(body))
	if !strings.Contains(string(body), "NEWPLAYER") {
		t.Fatalf("new account missing: %s", body)
	}
	if !strings.Contains(string(body), "Recent actions") || !strings.Contains(string(body), ">create<") {
		t.Fatal("staff action log missing create event")
	}
	if !strings.Contains(string(body), `name="username" value="NEWPLAYER"`) {
		t.Fatalf("created row not selected: %s", body)
	}
	if !strings.Contains(string(body), "is-selected") {
		t.Fatal("created row should stay selected")
	}

	res, err = client.Get(ts.URL + "/staff?q=NEW&select=NEWPLAYER")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "is-selected") || !strings.Contains(string(body), `value="NEWPLAYER"`) {
		t.Fatal("search cleared a still-visible selection")
	}

	res, err = client.Get(ts.URL + "/staff?q=NOPE&select=NEWPLAYER")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), "is-selected") {
		t.Fatal("selection should clear when the row is filtered out")
	}

	form = url.Values{
		"csrf_token":           {csrf},
		"username":             {"NewPlayer"},
		"new_password":         {"Newpass99"},
		"new_password_confirm": {"Newpass99"},
	}
	res, err = client.PostForm(ts.URL+"/staff/reset", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("reset %d", res.StatusCode)
	}
	if _, err := acc.Authenticate(ctx, "NewPlayer", "Newpass99"); err != nil {
		t.Fatal(err)
	}

	js, err := client.Get(ts.URL + "/static/js/staff.js")
	if err != nil {
		t.Fatal(err)
	}
	defer js.Body.Close()
	if js.StatusCode != 200 {
		t.Fatalf("staff.js %d", js.StatusCode)
	}
}

func TestStaffCannotResetHigherGM(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)
	if err := acc.Create(ctx, "AdminHigh", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("AdminHigh", 4)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")

	res, err := client.Get(ts.URL + "/staff?select=ADMINHIGH")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	if res.StatusCode != 200 {
		t.Fatalf("staff %d", res.StatusCode)
	}
	if !strings.Contains(html, "Cannot modify Super GM") {
		t.Fatalf("expected blocked copy: %s", html)
	}

	csrf := extractCSRF(html)
	form := url.Values{
		"csrf_token":           {csrf},
		"username":             {"AdminHigh"},
		"new_password":         {"Newpass99"},
		"new_password_confirm": {"Newpass99"},
	}
	res, err = client.PostForm(ts.URL+"/staff/reset", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "Cannot modify Super GM") {
		t.Fatalf("reset higher GM: %s", body)
	}
	if _, err := acc.Authenticate(ctx, "AdminHigh", "Abcd1234"); err != nil {
		t.Fatal("higher GM password should be unchanged")
	}
}

func TestStaffSetRank(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Admin", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Admin", 3)
	if err := acc.Create(ctx, "PlayerA", "Abcd1234", "p@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Admin", "Abcd1234")

	res, err := client.Get(ts.URL + "/staff?select=PLAYERA")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))

	form := url.Values{"csrf_token": {csrf}, "username": {"PlayerA"}, "rank": {"2"}}
	res, err = client.PostForm(ts.URL+"/staff/rank", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	listed, err := acc.GetListed(ctx, "PlayerA")
	if err != nil || listed.GMLevel != 2 {
		t.Fatalf("want GM 2, got %+v %v", listed, err)
	}

	res, err = client.Get(ts.URL + "/staff?select=PLAYERA")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	csrf = extractCSRF(string(body))
	form = url.Values{"csrf_token": {csrf}, "username": {"PlayerA"}, "rank": {"3"}}
	res, err = client.PostForm(ts.URL+"/staff/rank", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "rank below your own") && !strings.Contains(string(body), "cannot") {
		t.Fatalf("expected reject granting admin: %s", body)
	}
	listed, err = acc.GetListed(ctx, "PlayerA")
	if err != nil || listed.GMLevel != 2 {
		t.Fatalf("rank should stay GM, got %+v %v", listed, err)
	}

	form = url.Values{"csrf_token": {csrf}, "username": {"PlayerA"}, "rank": {"4"}}
	res, err = client.PostForm(ts.URL+"/staff/rank", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "Super GM") {
		t.Fatalf("expected super GM reject: %s", body)
	}
}

func TestStaffBanAndUnban(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)
	if err := acc.Create(ctx, "PlayerA", "Abcd1234", "p@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")

	res, err := client.Get(ts.URL + "/staff?select=PLAYERA")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "/staff/ban") || !strings.Contains(string(body), "Suspend") {
		t.Fatal("missing suspend form")
	}
	csrf := extractCSRF(string(body))
	res, err = client.PostForm(ts.URL+"/staff/ban", url.Values{
		"csrf_token": {csrf}, "username": {"PlayerA"}, "duration": {"perm"}, "reason": {"botting"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	listed, err := acc.GetListed(ctx, "PlayerA")
	if err != nil || !listed.Banned {
		t.Fatalf("want banned %+v %v", listed, err)
	}

	pjar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: pjar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	login(t, player, ts.URL, "PlayerA", "Abcd1234")
	accPage, err := player.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	accBody, _ := io.ReadAll(accPage.Body)
	accPage.Body.Close()
	if !strings.Contains(string(accBody), "suspended") {
		t.Fatalf("player login should be blocked: %s", accBody)
	}

	res, err = client.Get(ts.URL + "/staff?select=PLAYERA")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "suspended") || !strings.Contains(string(body), "/staff/unban") {
		t.Fatalf("staff list should show suspended: %s", body)
	}
	csrf = extractCSRF(string(body))
	res, err = client.PostForm(ts.URL+"/staff/unban", url.Values{
		"csrf_token": {csrf}, "username": {"PlayerA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	listed, err = acc.GetListed(ctx, "PlayerA")
	if err != nil || listed.Banned {
		t.Fatalf("want unbanned %+v %v", listed, err)
	}

	res, err = client.PostForm(ts.URL+"/staff/ban", url.Values{
		"csrf_token": {csrf}, "username": {"Staffer"}, "duration": {"perm"}, "reason": {"nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(out), "your own") && !strings.Contains(string(out), "Cannot") {
		t.Fatalf("self ban %s", out)
	}
}

func TestStaffUploadKeepsCategoryWhenFileComesFirst(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")
	res, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf_token", csrf)
	part, err := mw.CreateFormFile("file", "WotLK.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("PK client zip")); err != nil {
		t.Fatal(err)
	}
	_ = mw.WriteField("category", "client")
	_ = mw.WriteField("title", "WotLK Client")
	_ = mw.WriteField("version", "3.3.5a")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/staff/downloads", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	list, err := client.Get(ts.URL + "/downloads?tab=client")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(list.Body)
	list.Body.Close()
	html := string(body)
	if !strings.Contains(html, "WotLK Client") {
		t.Fatalf("client tab missing upload: %s", html)
	}
	patches, err := client.Get(ts.URL + "/downloads?tab=patches")
	if err != nil {
		t.Fatal(err)
	}
	pbody, _ := io.ReadAll(patches.Body)
	patches.Body.Close()
	if strings.Contains(string(pbody), "WotLK Client") {
		t.Fatal("client upload landed in patches")
	}
}

func TestStaffDownloadUpload(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "Staffer", "Abcd1234")

	res, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	if res.StatusCode != 200 || !strings.Contains(html, `action="/staff/downloads"`) {
		t.Fatalf("staff downloads form: %d %s", res.StatusCode, html)
	}
	csrf := extractCSRF(html)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf_token", csrf)
	_ = mw.WriteField("category", "mods")
	_ = mw.WriteField("title", "Test Mod")
	part, err := mw.CreateFormFile("file", "Test-Mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("PK staff upload")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/staff/downloads", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("upload %d %s", res.StatusCode, body)
	}

	list, err := client.Get(ts.URL + "/downloads")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(list.Body)
	list.Body.Close()
	if !strings.Contains(string(body), "Test Mod") || !strings.Contains(string(body), "/downloads/mods-test-mod") {
		t.Fatalf("downloads listing: %s", body)
	}

	file, err := client.Get(ts.URL + "/downloads/mods-test-mod")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(file.Body)
	file.Body.Close()
	if file.StatusCode != 200 || string(got) != "PK staff upload" {
		t.Fatalf("download %d %q", file.StatusCode, got)
	}

	staff, err := client.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(staff.Body)
	staff.Body.Close()
	csrf = extractCSRF(string(body))
	form := url.Values{"csrf_token": {csrf}, "id": {"mods-test-mod"}}
	res, err = client.PostForm(ts.URL+"/staff/downloads/delete", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("delete %d", res.StatusCode)
	}

	gone, err := client.Get(ts.URL + "/downloads/mods-test-mod")
	if err != nil {
		t.Fatal(err)
	}
	gone.Body.Close()
	if gone.StatusCode != 404 {
		t.Fatalf("deleted file status %d", gone.StatusCode)
	}
}

func TestStaffDownloadForbiddenForPlayers(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf_token", "x")
	_ = mw.WriteField("category", "mods")
	part, _ := mw.CreateFormFile("file", "x.zip")
	_, _ = part.Write([]byte("PK"))
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/staff/downloads", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func login(t *testing.T, client *http.Client, base, user, pass string) {
	t.Helper()
	res, err := client.Get(base + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))
	form := url.Values{"csrf_token": {csrf}, "username": {user}, "password": {pass}}
	res, err = client.PostForm(base+"/account/login", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		t.Fatalf("login %d", res.StatusCode)
	}
}
