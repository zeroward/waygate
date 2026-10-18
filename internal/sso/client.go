// Package sso implements the provider-facing OIDC protocol. Account ownership
// and authorization stay in identity and web; tokens never become sessions.
package sso

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Claims struct {
	Issuer, Subject, Email string
	EmailVerified          bool
}
type Transaction struct {
	State, Nonce, Verifier string
	Started, Expires       time.Time
	Fresh                  bool
}
type Client struct {
	issuer, id, secret, callback string
	http                         *http.Client
	mu                           sync.Mutex
	provider                     *oidc.Provider
}

func New(issuer, id, secret, callback string) *Client {
	c := &Client{issuer: issuer, id: id, secret: secret, callback: callback}
	c.http = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !c.safeEndpoint(req.URL.String()) {
			return errors.New("unsafe SSO redirect")
		}
		return nil
	}}
	return c
}
func (c *Client) providerFor(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, c.http), c.issuer)
	if err != nil {
		return nil, errors.New("SSO provider unavailable")
	}
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if p.Claims(&metadata) != nil {
		return nil, errors.New("invalid SSO discovery")
	}
	for _, raw := range []string{p.Endpoint().AuthURL, p.Endpoint().TokenURL, metadata.JWKS} {
		if !c.safeEndpoint(raw) {
			return nil, errors.New("invalid SSO endpoint")
		}
	}
	c.provider = p
	return p, nil
}
func random() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func (c *Client) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: c.id, ClientSecret: c.secret, Endpoint: p.Endpoint(), RedirectURL: c.callback, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
}
func (c *Client) Begin(ctx context.Context, fresh bool) (Transaction, string, error) {
	p, err := c.providerFor(ctx)
	if err != nil {
		return Transaction{}, "", err
	}
	t := Transaction{Started: time.Now(), Expires: time.Now().Add(5 * time.Minute), Fresh: fresh}
	for _, s := range []*string{&t.State, &t.Nonce, &t.Verifier} {
		*s, err = random()
		if err != nil {
			return Transaction{}, "", err
		}
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(t.Verifier), oidc.Nonce(t.Nonce)}
	if fresh {
		opts = append(opts, oauth2.SetAuthURLParam("max_age", "0"), oauth2.SetAuthURLParam("prompt", "login"))
	}
	return t, c.oauth(p).AuthCodeURL(t.State, opts...), nil
}
func (c *Client) Finish(ctx context.Context, t Transaction, state, code string) (Claims, error) {
	fail := errors.New("SSO verification failed; start again")
	if t.State == "" || code == "" || !time.Now().Before(t.Expires) || subtle.ConstantTimeCompare([]byte(state), []byte(t.State)) != 1 {
		return Claims{}, fail
	}
	p, err := c.providerFor(ctx)
	if err != nil {
		return Claims{}, err
	}
	ctx = oidc.ClientContext(ctx, c.http)
	token, err := c.oauth(p).Exchange(ctx, code, oauth2.VerifierOption(t.Verifier))
	if err != nil {
		return Claims{}, fail
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return Claims{}, fail
	}
	id, err := p.Verifier(&oidc.Config{ClientID: c.id}).Verify(ctx, raw)
	if err != nil {
		return Claims{}, fail
	}
	if id.Subject == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(t.Nonce)) != 1 {
		return Claims{}, fail
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		AuthTime      int64  `json:"auth_time"`
		AZP           string `json:"azp"`
	}
	if id.Claims(&claims) != nil {
		return Claims{}, fail
	}
	if (len(id.Audience) > 1 && claims.AZP != c.id) || (claims.AZP != "" && claims.AZP != c.id) {
		return Claims{}, fail
	}
	if id.IssuedAt.IsZero() || id.IssuedAt.After(time.Now().Add(30*time.Second)) {
		return Claims{}, fail
	}
	if t.Fresh && (claims.AuthTime == 0 || claims.AuthTime < t.Started.Add(-30*time.Second).Unix() || claims.AuthTime > time.Now().Add(30*time.Second).Unix()) {
		return Claims{}, fail
	}
	return Claims{Issuer: id.Issuer, Subject: id.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified}, nil
}

func (c *Client) safeEndpoint(raw string) bool {
	u, e := url.Parse(raw)
	base, _ := url.Parse(c.issuer)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || (base != nil && base.Scheme == "http" && u.Scheme == "http" && u.Host == base.Host))
}
