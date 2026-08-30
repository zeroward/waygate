package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestAccountListsCharacters(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	anon, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(anon), "Frostwarden") {
		t.Fatal("logged-out account must not list characters")
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	res, err = client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, "Frostwarden") || !strings.Contains(html, "NorthrendScout") {
		t.Fatal("missing demo characters")
	}
	if !strings.Contains(html, "Paladin") || !strings.Contains(html, "Hunter") {
		t.Fatal("missing class")
	}
	if !strings.Contains(html, "1234g") || !strings.Contains(html, "Icecrown") {
		t.Fatal("missing gold or location")
	}
	if !strings.Contains(html, "Change password") {
		t.Fatal("password form missing")
	}
	if strings.Contains(html, "<textarea") {
		t.Fatal("unexpected editor")
	}
	if !strings.Contains(html, "Unstuck") || !strings.Contains(html, "Log out first") {
		t.Fatal("missing unstuck controls")
	}
	if !strings.Contains(html, `name="guid" value="2"`) {
		t.Fatal("offline character missing guid")
	}
}

func TestAccountUnstuck(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	anon, err := noRedir.PostForm(ts.URL+"/account/unstuck", url.Values{"guid": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon unstuck %d", anon.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	res, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(body))

	res, err = client.PostForm(ts.URL+"/account/unstuck", url.Values{"guid": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf %d", res.StatusCode)
	}

	res, err = client.PostForm(ts.URL+"/account/unstuck", url.Values{"csrf_token": {csrf}, "guid": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	onlineBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(onlineBody), "Log out of the game first") {
		t.Fatalf("online: %s", onlineBody)
	}

	res, err = client.PostForm(ts.URL+"/account/unstuck", url.Values{"csrf_token": {csrf}, "guid": {"99"}})
	if err != nil {
		t.Fatal(err)
	}
	miss, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(miss), "Character not found") {
		t.Fatalf("missing: %s", miss)
	}

	res, err = client.PostForm(ts.URL+"/account/unstuck", url.Values{"csrf_token": {csrf}, "guid": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(ok), "NorthrendScout was sent to their hearth") {
		t.Fatalf("unstuck %d %s", res.StatusCode, ok)
	}
}

func TestLoginSurvivesServerRestart(t *testing.T) {
	dir := t.TempDir()
	kbPath := dir + "/kb.sqlite"
	ts1, srv1 := testWebKB(t, kbPath)
	if err := srv1.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts1.URL, "HeroOne", "Abcd1234")
	res, err := client.Get(ts1.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "HEROONE") {
		t.Fatal("not logged in")
	}
	var cookie *http.Cookie
	for _, c := range jar.Cookies(res.Request.URL) {
		if c.Name == "waygate_session" {
			cc := *c
			cookie = &cc
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	ts1.Close()

	ts2, _ := testWebKB(t, kbPath)
	defer ts2.Close()
	req, err := http.NewRequest(http.MethodGet, ts2.URL+"/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "HEROONE") || strings.Contains(string(body), `name="password"`) {
		t.Fatalf("session lost after restart: %s", body)
	}
}

func TestTOTPSetupShowsQR(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	res, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	if strings.Contains(html, `class="totp-qr"`) {
		t.Fatal("qr shown before setup")
	}
	csrf := extractCSRF(html)
	res, err = client.PostForm(ts.URL+"/account/totp/start", url.Values{"csrf_token": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html = string(page)
	if !strings.Contains(html, `src="data:image/png;base64,`) {
		t.Fatal("missing qr data uri")
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatal("template sanitized the qr")
	}
	if !strings.Contains(html, `alt="Authenticator QR code"`) {
		t.Fatal("missing qr alt")
	}
	if !strings.Contains(html, "otpauth://totp/") {
		t.Fatal("missing otpauth fallback")
	}
}
