package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/descope/virtualwebauthn"
)

func TestRegressionPasskeyWithoutUserVerification(t *testing.T) {
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
	authenticator := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{UserNotVerified: true})
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	proof := jsonPOST(t, client, ts.URL+"/account/reauth/password", csrf, map[string]string{"purpose": "passkey-add", "password": "Abcd1234"})
	if proof["ok"] != true {
		t.Fatal(proof)
	}
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
	if finish["ok"] == true || finish["_status"].(int) < 400 {
		t.Fatal("UV=false registration accepted", finish)
	}
	keys, err := srv.id.Store().ListPasskeys(context.Background(), mustUserID(t, srv, "HEROONE"))
	if err != nil || len(keys) != 0 {
		t.Fatal("unverified passkey persisted", err)
	}
}
