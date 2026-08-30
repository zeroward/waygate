package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zeroward/waygate/internal/wg"
)

func TestWGHiddenWhenDisabled(t *testing.T) {
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
	if strings.Contains(string(body), `id="vpn"`) {
		t.Fatal("vpn panel shown while disabled")
	}
}

func TestWGEnabledAccountFlow(t *testing.T) {
	ts, srv := testWGWeb(t)
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
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(page)
	if !strings.Contains(html, `id="vpn"`) {
		t.Fatal("missing vpn panel")
	}
	csrf := extractCSRF(html)
	res, err = client.PostForm(ts.URL+"/account/wg", url.Values{"csrf_token": {csrf}, "name": {"Laptop"}})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html = string(created)
	if !strings.Contains(html, "Laptop") || !strings.Contains(html, "10.8.0.2") {
		t.Fatal("missing new peer")
	}
	if !strings.Contains(html, `src="data:image/png;base64,`) {
		t.Fatal("missing qr")
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatal("qr sanitized")
	}

	zipRes, err := client.Get(ts.URL + "/account/wg/1/zip")
	if err != nil {
		t.Fatal(err)
	}
	zb, _ := io.ReadAll(zipRes.Body)
	zipRes.Body.Close()
	if zipRes.StatusCode != 200 || !strings.Contains(string(zb), "[Interface]") {
		t.Fatalf("zip %d", zipRes.StatusCode)
	}
	if !strings.Contains(string(zb), "set realmlist 10.8.0.1") {
		t.Fatal("realmlist")
	}
	if strings.Contains(string(zb), "# set realmlist") {
		t.Fatal("realmlist should only use wg0 IP")
	}
	if strings.Contains(string(zb), "0.0.0.0/0") {
		t.Fatal("full tunnel")
	}

	confRes, err := client.Get(ts.URL + "/account/wg/1/conf")
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := io.ReadAll(confRes.Body)
	confRes.Body.Close()
	if !strings.Contains(string(cb), "Endpoint =") || !strings.Contains(string(cb), "PersistentKeepalive = 25") {
		t.Fatal(string(cb))
	}

	csrf = extractCSRF(html)
	del, err := client.PostForm(ts.URL+"/account/wg/1/delete", url.Values{"csrf_token": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	gone, _ := io.ReadAll(del.Body)
	del.Body.Close()
	if strings.Contains(string(gone), "<strong>Laptop</strong>") {
		t.Fatal("still listed")
	}
}

func TestStaffSetsWGEndpoint(t *testing.T) {
	ts, srv := testWGWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	srv.accounts.GrantGM("Staffer", 2)
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	staff := &http.Client{Jar: jar}
	login(t, staff, ts.URL, "Staffer", "Abcd1234")
	res, err := staff.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	if !strings.Contains(html, `id="vpn"`) || !strings.Contains(html, "VPN endpoint") {
		t.Fatal("missing staff vpn form")
	}
	csrf := extractCSRF(html)
	res, err = staff.PostForm(ts.URL+"/staff/wg", url.Values{"csrf_token": {csrf}, "endpoint": {"203.0.113.9"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(saved), "203.0.113.9:51820") {
		t.Fatalf("endpoint not saved: %s", saved)
	}

	pjar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: pjar}
	login(t, player, ts.URL, "HeroOne", "Abcd1234")
	acc, err := player.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	accBody, _ := io.ReadAll(acc.Body)
	acc.Body.Close()
	csrf = extractCSRF(string(accBody))
	res, err = player.PostForm(ts.URL+"/account/wg", url.Values{"csrf_token": {csrf}, "name": {"Phone"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	conf, err := player.Get(ts.URL + "/account/wg/1/conf")
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := io.ReadAll(conf.Body)
	conf.Body.Close()
	if !strings.Contains(string(cb), "Endpoint = 203.0.113.9:51820") {
		t.Fatalf("conf endpoint %s", cb)
	}
	zipRes, err := player.Get(ts.URL + "/account/wg/1/zip")
	if err != nil {
		t.Fatal(err)
	}
	zb, _ := io.ReadAll(zipRes.Body)
	zipRes.Body.Close()
	if !strings.Contains(string(zb), "set realmlist 10.8.0.1") {
		t.Fatal("realmlist should be wg0 IP")
	}
	if strings.Contains(string(zb), "set realmlist 203.0.113.9") {
		t.Fatal("realmlist used public endpoint")
	}
}

func testWGWeb(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	ts, srv := testWeb(t)
	srv.cfg.WGEnabled = true
	srv.cfg.WGDir = t.TempDir()
	srv.cfg.WGPeerMax = 5
	srv.cfg.WGPort = 51820
	if _, err := wg.EnsureServerKeys(srv.cfg.WGDir); err != nil {
		t.Fatal(err)
	}
	srv.wgOK = true
	return ts, srv
}
