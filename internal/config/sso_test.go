package config

import "testing"

func TestSSOConfigValidation(t *testing.T) {
	valid := Config{SSOEnabled: true, OIDCIssuer: "https://id.example/realm", OIDCClientID: "client", OIDCClientSecret: "secret", SSOVaultKeyFile: "data/keys.json", SiteURL: "https://portal.example", SessionSecure: true}
	if err := valid.ValidateSSO(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"no issuer", func(c *Config) { c.OIDCIssuer = "" }},
		{"no client", func(c *Config) { c.OIDCClientID = "" }},
		{"no secret", func(c *Config) { c.OIDCClientSecret = "" }},
		{"no key file", func(c *Config) { c.SSOVaultKeyFile = "" }},
		{"insecure provider", func(c *Config) { c.OIDCIssuer = "http://id.example" }},
		{"insecure site", func(c *Config) { c.SiteURL = "http://portal.example" }},
		{"insecure cookie", func(c *Config) { c.SessionSecure = false }},
		{"query in issuer", func(c *Config) { c.OIDCIssuer = "https://id.example/?x=1" }},
		{"userinfo", func(c *Config) { c.OIDCIssuer = "https://secret@id.example" }},
		{"path in site", func(c *Config) { c.SiteURL = "https://portal.example/subpath" }},
		{"unsafe NetBird link", func(c *Config) { c.NetBirdSetupURL = "javascript:alert(1)" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.edit(&c)
			if c.ValidateSSO() == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	demo := valid
	demo.DemoMode = true
	demo.OIDCIssuer = "http://127.0.0.1:5556"
	demo.SiteURL = "http://localhost:3080"
	demo.SessionSecure = false
	if err := demo.ValidateSSO(); err != nil {
		t.Fatal(err)
	}
	demo.OIDCIssuer = "http://id.example"
	if demo.ValidateSSO() == nil {
		t.Fatal("non-loopback demo HTTP accepted")
	}
	if err := (Config{}).ValidateSSO(); err != nil {
		t.Fatal("disabled SSO changed default", err)
	}
}
