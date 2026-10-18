// Package ssotest supplies a local OIDC provider for integration tests. It is
// never imported by application code and never contacts an external provider.
package ssotest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Provider struct {
	Server         *httptest.Server
	mu             sync.Mutex
	key, wrongKey  *rsa.PrivateKey
	subject, email string
	verified       bool
	overrides      map[string]any
	fault          string
	codes          map[string]url.Values
	tokens         int
}

func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{key: key, wrongKey: wrong, subject: "subject-1", email: "sso@example.test", verified: true, codes: map[string]url.Values{}}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	return p
}
func (p *Provider) URL() string { return p.Server.URL }
func (p *Provider) SetIdentity(subject, email string, verified bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subject, p.email, p.verified = subject, email, verified
}
func (p *Provider) Override(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.overrides = claims
}
func (p *Provider) Fault(f string)  { p.mu.Lock(); defer p.mu.Unlock(); p.fault = f }
func (p *Provider) TokenCount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.tokens }
func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fault == "offline" {
		http.Error(w, "offline", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.URL(), "authorization_endpoint": p.URL() + "/authorize", "token_endpoint": p.URL() + "/token", "jwks_uri": p.URL() + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
	case "/keys":
		enc := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test", "n": enc(p.key.N.Bytes()), "e": enc(big.NewInt(int64(p.key.E)).Bytes())}}})
	case "/authorize":
		q := r.URL.Query()
		dest, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect", 400)
			return
		}
		values := dest.Query()
		values.Set("state", q.Get("state"))
		if p.fault == "cancel" {
			values.Set("error", "access_denied")
		} else {
			if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
				http.Error(w, "missing security parameters", 400)
				return
			}
			b := make([]byte, 24)
			_, _ = rand.Read(b)
			code := base64.RawURLEncoding.EncodeToString(b)
			p.codes[code] = q
			values.Set("code", code)
		}
		dest.RawQuery = values.Encode()
		http.Redirect(w, r, dest.String(), 302)
	case "/token":
		p.tokens++
		if p.fault == "token-error" {
			http.Error(w, "unavailable", 503)
			return
		}
		_ = r.ParseForm()
		q, ok := p.codes[r.FormValue("code")]
		delete(p.codes, r.FormValue("code"))
		client, secret, hasBasic := r.BasicAuth()
		if !hasBasic {
			client, secret = r.FormValue("client_id"), r.FormValue("client_secret")
		}
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if !ok || client != "test-client" || secret != "test-secret" || r.FormValue("grant_type") != "authorization_code" || r.FormValue("redirect_uri") != q.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
			http.Error(w, "invalid_grant", 400)
			return
		}
		claims := jwt.MapClaims{"iss": p.URL(), "sub": p.subject, "aud": "test-client", "nonce": q.Get("nonce"), "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "auth_time": time.Now().Unix(), "email": p.email, "email_verified": p.verified}
		for k, v := range p.overrides {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test"
		key := p.key
		if p.fault == "signature" {
			key = p.wrongKey
		}
		raw, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, "signing error", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access-token-never-persist", "token_type": "Bearer", "id_token": raw})
	default:
		http.NotFound(w, r)
	}
}

// Authorize simulates only the provider browser redirect, leaving callback
// processing to the test's application client.
func Authorize(t testing.TB, authorizationURL string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 302 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("authorize %d: %s", res.StatusCode, b)
	}
	return res.Header.Get("Location")
}
