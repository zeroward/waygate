package sso

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zeroward/waygate/internal/ssotest"
)

func TestOIDCValidation(t *testing.T) {
	p := ssotest.New(t)
	cases := []struct {
		name   string
		claims map[string]any
		fault  string
		fresh  bool
		valid  bool
	}{
		{name: "login", valid: true}, {name: "fresh verification", fresh: true, valid: true},
		{name: "issuer", claims: map[string]any{"iss": "https://wrong.example"}},
		{name: "audience", claims: map[string]any{"aud": "other-client"}},
		{name: "multiple audiences without authorized party", claims: map[string]any{"aud": []string{"test-client", "other"}}},
		{name: "authorized party", claims: map[string]any{"azp": "other"}},
		{name: "nonce", claims: map[string]any{"nonce": "wrong"}},
		{name: "subject", claims: map[string]any{"sub": ""}},
		{name: "expiry", claims: map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}},
		{name: "future issued", claims: map[string]any{"iat": time.Now().Add(time.Hour).Unix()}},
		{name: "fresh without auth time", claims: map[string]any{"auth_time": nil}, fresh: true},
		{name: "stale authentication", claims: map[string]any{"auth_time": time.Now().Add(-time.Hour).Unix()}, fresh: true},
		{name: "signature", fault: "signature"}, {name: "provider token outage", fault: "token-error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p.Override(tc.claims)
			p.Fault(tc.fault)
			c := New(p.URL(), "test-client", "test-secret", "http://127.0.0.1/callback")
			tx, auth, err := c.Begin(context.Background(), tc.fresh)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := url.Parse(auth)
			if tc.fresh && (a.Query().Get("max_age") != "0" || a.Query().Get("prompt") != "login") {
				t.Fatal("missing fresh authentication request")
			}
			callback, _ := url.Parse(ssotest.Authorize(t, auth))
			q := callback.Query()
			claims, err := c.Finish(context.Background(), tx, q.Get("state"), q.Get("code"))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (claims.Subject != "subject-1" || !claims.EmailVerified) {
				t.Fatalf("bad claims: %+v", claims)
			}
		})
	}
}
func TestOIDCStateExpiryAndPKCE(t *testing.T) {
	p := ssotest.New(t)
	c := New(p.URL(), "test-client", "test-secret", "http://127.0.0.1/callback")
	for _, fault := range []string{"state", "expiry", "pkce"} {
		t.Run(fault, func(t *testing.T) {
			tx, auth, err := c.Begin(context.Background(), false)
			if err != nil {
				t.Fatal(err)
			}
			callback, _ := url.Parse(ssotest.Authorize(t, auth))
			q := callback.Query()
			switch fault {
			case "state":
				tx.State = "wrong"
			case "expiry":
				tx.Expires = time.Now().Add(-time.Second)
			case "pkce":
				tx.Verifier = strings.Repeat("x", 43)
			}
			before := p.TokenCount()
			if _, err = c.Finish(context.Background(), tx, q.Get("state"), q.Get("code")); err == nil {
				t.Fatal("invalid transaction accepted")
			}
			if fault != "pkce" && p.TokenCount() != before {
				t.Fatal("invalid browser transaction reached token endpoint")
			}
		})
	}
}
func TestOIDCDiscoveryOutageRetries(t *testing.T) {
	p := ssotest.New(t)
	c := New(p.URL(), "test-client", "test-secret", "http://127.0.0.1/callback")
	p.Fault("offline")
	if _, _, err := c.Begin(context.Background(), false); err == nil {
		t.Fatal("outage accepted")
	}
	p.Fault("")
	if _, _, err := c.Begin(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}
