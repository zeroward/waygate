package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// Check complete rendering as well as status: a template error can happen after
// the server has already written a 200 response.
func TestPortalNavigationAndSections(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	defer srv.kb.Close()
	srv.cfg.GMMinLevel, srv.cfg.GMModLevel = 3, 1
	read := func(t *testing.T, client *http.Client, path string) string {
		t.Helper()
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || !strings.Contains(string(body), "</html>") {
			t.Fatalf("incomplete HTML on %s (status %d)", path, res.StatusCode)
		}
		return string(body)
	}
	for _, path := range []string{"/", "/account", "/register", "/online", "/leaderboards", "/armory", "/contact"} {
		body := read(t, http.DefaultClient, path)
		if strings.Contains(body, `href="/staff"`) || strings.Contains(body, `href="/staff/tickets"`) {
			t.Fatalf("staff navigation for visitor on %s", path)
		}
	}
	for _, role := range []struct {
		name  string
		level uint8
	}{{"PlayerUI", 0}, {"ModeratorUI", 1}, {"AdminUI", 3}} {
		t.Run(role.name, func(t *testing.T) {
			if err := srv.accounts.Create(context.Background(), role.name, "Abcd1234", role.name+"@example.test", 2); err != nil {
				t.Fatal(err)
			}
			srv.accounts.GrantGM(role.name, role.level)
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar}
			login(t, client, ts.URL, role.name, "Abcd1234")
			body := read(t, client, "/account")
			if strings.Contains(body, `href="/staff"`) != (role.level >= 3) || strings.Contains(body, `href="/staff/tickets"`) != (role.level >= 1) {
				t.Fatal("incorrect role navigation")
			}
			for _, id := range []string{"overview", "wow", "characters", "security", "password", "totp"} {
				if !strings.Contains(body, `id="`+id+`"`) {
					t.Fatalf("missing account section %s", id)
				}
			}
			if !strings.Contains(body, `action="/account/password"`) {
				t.Fatal("local password form missing")
			}
			for _, path := range []string{"/downloads", "/kb", "/kb/how-to-connect", "/tickets", "/tickets/new", "/companion", "/armory/Frostwarden"} {
				read(t, client, path)
			}
			if role.level >= 1 {
				read(t, client, "/staff/tickets")
			}
			if role.level >= 3 {
				body = read(t, client, "/staff")
				for _, id := range []string{"accounts", "downloads", "realm-settings", "banner", "calendar", "register-key"} {
					if !strings.Contains(body, `id="`+id+`"`) {
						t.Fatalf("missing staff section %s", id)
					}
				}
				read(t, client, "/staff/kb")
			}
		})
	}
}

func TestSSOSecurityPresentation(t *testing.T) {
	b := newSSOBrowser(t)
	u := b.registerLocal("SSOUI")
	b.localLogin(u.Username)
	b.link(u.Username)
	body := b.get("/account").body
	if !strings.Contains(body, "</html>") || !strings.Contains(body, `id="security"`) || !strings.Contains(body, "Single sign-on connected") {
		t.Fatal("incomplete SSO security section")
	}
	for _, forbidden := range []string{`action="/account/password"`, `href="/account#password"`, `id="totp"`, `id="passkeys"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("local-only control exposed: %s", forbidden)
		}
	}
	for _, purpose := range []string{"wow-reveal", "wow-password", "wow-create"} {
		if !strings.Contains(body, `name="purpose" value="`+purpose+`"`) {
			t.Fatalf("missing separate verification for %s", purpose)
		}
	}
}
