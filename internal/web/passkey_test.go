package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/descope/virtualwebauthn"
	"github.com/zeroward/waygate/internal/identity"
)

func TestPasskeyRegisterBeginRequiresLogin(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()
	res, err := http.Post(ts.URL+"/account/passkey/register/begin", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestPasskeyRegisterAndLogin(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if srv.wa == nil {
		t.Fatal("webauthn disabled")
	}
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")

	acc, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(acc.Body)
	acc.Body.Close()
	html := string(body)
	if !strings.Contains(html, `data-passkey-register`) {
		t.Fatal("missing register button")
	}
	if !strings.Contains(html, "/static/js/passkey.js") {
		t.Fatal("missing passkey.js")
	}
	csrf := extractCSRF(html)

	rp := virtualwebauthn.RelyingParty{Name: "Icecrown", ID: "localhost", Origin: "http://localhost"}
	authenticator := virtualwebauthn.NewAuthenticator()
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	begin := jsonPOST(t, client, ts.URL+"/account/passkey/register/begin", csrf, map[string]string{"name": "Laptop"})
	if begin["_status"].(int) != 200 {
		t.Fatalf("begin %v", begin)
	}
	optsJSON, err := json.Marshal(beginWithoutStatus(begin))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := virtualwebauthn.ParseAttestationOptions(string(optsJSON))
	if err != nil {
		t.Fatalf("parse options %v %s", err, optsJSON)
	}
	authenticator.Options.UserHandle = []byte(parsed.UserID)
	attestation := virtualwebauthn.CreateAttestationResponse(rp, authenticator, credential, *parsed)
	finish := jsonPOSTRaw(t, client, ts.URL+"/account/passkey/register/finish", csrf, attestation)
	if finish["_status"].(int) != 200 || finish["ok"] != true {
		t.Fatalf("finish %v", finish)
	}
	authenticator.AddCredential(credential)

	page, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	listed, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(listed), "<strong>Laptop</strong>") {
		t.Fatalf("missing passkey name")
	}

	// Log out, then sign in with the passkey (skips TOTP).
	csrf = extractCSRF(string(listed))
	res, err := client.PostForm(ts.URL+"/account/logout", url.Values{"csrf_token": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	loginPage, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	loginBody, _ := io.ReadAll(loginPage.Body)
	loginPage.Body.Close()
	csrf = extractCSRF(string(loginBody))
	if !strings.Contains(string(loginBody), `data-passkey-login`) {
		t.Fatal("missing login button")
	}

	assertBegin := jsonPOST(t, client, ts.URL+"/account/passkey/login/begin", csrf, map[string]string{"next": "/tickets"})
	if assertBegin["_status"].(int) != 200 {
		t.Fatalf("login begin %v", assertBegin)
	}
	assertJSON, _ := json.Marshal(beginWithoutStatus(assertBegin))
	assertOpts, err := virtualwebauthn.ParseAssertionOptions(string(assertJSON))
	if err != nil {
		t.Fatalf("parse assertion %v %s", err, assertJSON)
	}
	assertion := virtualwebauthn.CreateAssertionResponse(rp, authenticator, credential, *assertOpts)
	logged := jsonPOSTRaw(t, client, ts.URL+"/account/passkey/login/finish", csrf, assertion)
	if logged["_status"].(int) != 200 || logged["ok"] != true {
		t.Fatalf("login finish %v", logged)
	}
	if logged["next"] != "/tickets" {
		t.Fatalf("next %v", logged["next"])
	}

	home, err := client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	homeBody, _ := io.ReadAll(home.Body)
	home.Body.Close()
	if !strings.Contains(string(homeBody), "HEROONE") || strings.Contains(string(homeBody), `name="password"`) {
		t.Fatalf("not logged in: %s", homeBody)
	}

	keys, err := srv.id.Store().ListPasskeys(context.Background(), mustUserID(t, srv, "HEROONE"))
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys %v %v", err, keys)
	}
	csrf = extractCSRF(string(homeBody))
	del, err := client.PostForm(ts.URL+"/account/passkey/delete", url.Values{
		"csrf_token": {csrf},
		"id":         {strconv.FormatInt(keys[0].ID, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	delBody, _ := io.ReadAll(del.Body)
	del.Body.Close()
	if strings.Contains(string(delBody), "<strong>Laptop</strong>") {
		t.Fatal("passkey still listed")
	}
	left, err := srv.id.Store().ListPasskeys(context.Background(), mustUserID(t, srv, "HEROONE"))
	if err != nil || len(left) != 0 {
		t.Fatalf("store after delete %v %v", err, left)
	}
}

func jsonPOST(t *testing.T, client *http.Client, url, csrf string, body any) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return jsonPOSTRaw(t, client, url, csrf, string(b))
}

func jsonPOSTRaw(t *testing.T, client *http.Client, url, csrf, raw string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json %v %s", err, b)
	}
	out["_status"] = res.StatusCode
	return out
}

func beginWithoutStatus(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k == "_status" {
			continue
		}
		out[k] = v
	}
	return out
}

func mustUserID(t *testing.T, srv *Server, username string) uint32 {
	t.Helper()
	u, err := srv.id.GetByUsername(context.Background(), username)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestPasskeySanitizeAndHandle(t *testing.T) {
	if identity.SanitizePasskeyName("  ") != "Passkey" {
		t.Fatal("default name")
	}
	if identity.SanitizePasskeyName(strings.Repeat("a", 50)) != strings.Repeat("a", 40) {
		t.Fatal("truncate")
	}
}
