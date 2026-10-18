package config

import (
	"fmt"
	"net/url"
)

func (c Config) ValidateSSO() error {
	if c.NetBirdSetupURL != "" {
		u, e := url.Parse(c.NetBirdSetupURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("NETBIRD_SETUP_URL must be an HTTPS URL")
		}
	}
	if !c.SSOEnabled {
		return nil
	}
	if c.OIDCClientID == "" || c.OIDCClientSecret == "" || c.SSOVaultKeyFile == "" {
		return fmt.Errorf("SSO requires OIDC_CLIENT_ID, OIDC_CLIENT_SECRET and SSO_VAULT_KEY_FILE")
	}
	for name, raw := range map[string]string{"OIDC_ISSUER": c.OIDCIssuer, "SITE_URL": c.SiteURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s must be an absolute URL without credentials, query or fragment", name)
		}
		loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
		if u.Scheme != "https" && !(c.DemoMode && loopback && u.Scheme == "http") {
			return fmt.Errorf("%s requires HTTPS (HTTP loopback is allowed only in demo mode)", name)
		}
		if name == "SITE_URL" && (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("SITE_URL must be an origin")
		}
	}
	if !c.DemoMode && !c.SessionSecure {
		return fmt.Errorf("SSO requires SESSION_SECURE_COOKIE=true")
	}
	return nil
}
