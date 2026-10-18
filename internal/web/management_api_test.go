package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/status"
)

const testManagementToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func managementTestServer() *Server {
	cfg := config.Config{
		DemoMode: true, ManagementAPIToken: testManagementToken, RealmName: "Icecrown",
		CoreName: "AzerothCore", PublicHost: "realm.example", PublicAuthPort: 3724,
		PublicWorldPort: 28085, StatusCache: 20 * time.Second, DefaultExpansion: 2,
	}
	// Nil browser stores make any accidental session/identity handling fail.
	return &Server{cfg: cfg, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		captcha: captcha.New(cfg), status: status.New(cfg, nil, nil)}
}

func managementRequest(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Cookie", "wg_session=expired-or-revoked")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestManagementAPIContracts(t *testing.T) {
	s := managementTestServer()
	h := s.Handler()
	var generated string
	for _, section := range []string{"dashboard", "status", "players", "leaderboards", "modules"} {
		t.Run(section, func(t *testing.T) {
			w := managementRequest(h, "GET", "/api/v1/"+section, testManagementToken)
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body)
			}
			if w.Header().Get("Content-Type") != "application/json; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("headers: %v", w.Header())
			}
			if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("browser behavior on API: %v", w.Header())
			}
			var res struct {
				GeneratedAt   string `json:"generated_at"`
				CacheTTL      int    `json:"cache_ttl_seconds"`
				Demo, Partial bool
				Data          json.RawMessage
				Errors        []managementError
			}
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if !res.Demo || res.Partial || res.CacheTTL != 20 || res.Errors == nil || len(res.Errors) != 0 {
				t.Fatalf("metadata: %+v", res)
			}
			if _, err := time.Parse(time.RFC3339Nano, res.GeneratedAt); err != nil || !strings.HasSuffix(res.GeneratedAt, "Z") {
				t.Fatalf("timestamp: %s", res.GeneratedAt)
			}
			if generated == "" {
				generated = res.GeneratedAt
			}
			if generated != res.GeneratedAt {
				t.Fatal("endpoints did not reuse snapshot")
			}
			switch section {
			case "dashboard":
				var d managementDashboard
				if err := json.Unmarshal(res.Data, &d); err != nil {
					t.Fatal(err)
				}
				if d.Status == nil || len(d.Players) == 0 || d.Leaderboards == nil || len(d.Modules) == 0 {
					t.Fatalf("missing sections: %+v", d)
				}
			case "status":
				var d managementStatus
				if err := json.Unmarshal(res.Data, &d); err != nil {
					t.Fatal(err)
				}
				if !d.RealmUp || d.RealmName != "Icecrown" || d.PublicWorld != 28085 || d.Population.Humans != 9 || d.Population.GMs != 1 || d.Population.Total != d.Population.Humans+d.Population.Bots {
					t.Fatalf("status: %+v", d)
				}
			case "players":
				var d []managementPlayer
				if err := json.Unmarshal(res.Data, &d); err != nil {
					t.Fatal(err)
				}
				if len(d) == 0 || d[0].RaceID == 0 || d[0].ClassID == 0 || d[0].Zone == "" {
					t.Fatalf("players: %+v", d)
				}
			case "leaderboards":
				var d managementLeaderboards
				if err := json.Unmarshal(res.Data, &d); err != nil {
					t.Fatal(err)
				}
				if len(d.Playtime) == 0 || len(d.Kills) == 0 || len(d.Honor) == 0 || len(d.Arena) == 0 || len(d.Gold) == 0 || d.Gold[0].Raw != 12345678 {
					t.Fatalf("boards: %+v", d)
				}
			case "modules":
				var d []managementModule
				if err := json.Unmarshal(res.Data, &d); err != nil {
					t.Fatal(err)
				}
				if len(d) == 0 || d[0].ID == "" || d[0].Title == "" {
					t.Fatalf("modules: %+v", d)
				}
			}
			head := managementRequest(h, "HEAD", "/api/v1/"+section, testManagementToken)
			if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != w.Header().Get("Content-Length") {
				t.Fatalf("HEAD: %d %v %s", head.Code, head.Header(), head.Body)
			}
		})
	}
}

func TestManagementAPIAccessAndErrors(t *testing.T) {
	s := managementTestServer()
	h := s.Handler()
	for _, tc := range []struct {
		method, path, token string
		status              int
		code                string
	}{
		{"GET", "/api/v1/status", "", 401, "unauthorized"},
		{"GET", "/api/v1/status", "wrong", 401, "unauthorized"},
		{"GET", "/api/v1/status?token=" + testManagementToken, "", 401, "unauthorized"},
		{"GET", "/api/v1/nope", testManagementToken, 404, "not_found"},
		{"GET", "/api/v2/status", testManagementToken, 404, "not_found"},
		{"GET", "/api/v1/status/", testManagementToken, 404, "not_found"},
		{"POST", "/api/v1/status", testManagementToken, 405, "method_not_allowed"},
		{"PUT", "/api/v1/status", testManagementToken, 405, "method_not_allowed"},
		{"PATCH", "/api/v1/players", testManagementToken, 405, "method_not_allowed"},
		{"DELETE", "/api/v1/modules", testManagementToken, 405, "method_not_allowed"},
		{"OPTIONS", "/api/v1/status", testManagementToken, 405, "method_not_allowed"},
	} {
		w := managementRequest(h, tc.method, tc.path, tc.token)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
		if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
			t.Fatal("API error touched session")
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal("missing Allow")
		}
		if tc.status == 401 && w.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("missing challenge")
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/status", nil)
	r.Header.Add("Authorization", "Bearer "+testManagementToken)
	r.Header.Add("Authorization", "Bearer "+testManagementToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("duplicate credentials accepted")
	}
	head := managementRequest(h, "HEAD", "/api/v1/status", "")
	if head.Code != 401 || head.Body.Len() != 0 {
		t.Fatal("HEAD error has body")
	}
	s.cfg.ManagementAPIToken = ""
	w = managementRequest(h, "GET", "/api/v1/status", testManagementToken)
	if w.Code != 404 {
		t.Fatal("disabled API accessible")
	}
}

func TestManagementAPIFailures(t *testing.T) {
	s := managementTestServer()
	cfg := s.cfg
	cfg.DemoMode = false
	s.status = status.New(cfg, nil, nil)
	h := s.Handler()
	w := managementRequest(h, "GET", "/api/v1/dashboard", testManagementToken)
	var res struct {
		Demo, Partial bool
		Data          managementDashboard
		Errors        []managementError
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || res.Demo || !res.Partial || len(res.Errors) != 4 || res.Data.Status != nil || res.Data.Players != nil || res.Data.Leaderboards != nil || res.Data.Modules != nil {
		t.Fatalf("failure misreported: %d %s", w.Code, w.Body)
	}
	for _, section := range []string{"status", "players", "leaderboards", "modules"} {
		w := managementRequest(h, "GET", "/api/v1/"+section, testManagementToken)
		if w.Code != 503 || !strings.Contains(w.Body.String(), section+"_unavailable") {
			t.Fatalf("%s: %d %s", section, w.Code, w.Body)
		}
	}
	// Unexpected failures must remain JSON, without exposing panic details.
	s.status = nil
	w = managementRequest(h, "GET", "/api/v1/status", testManagementToken)
	if w.Code != 500 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("panic: %d %s", w.Code, w.Body)
	}
}

func TestManagementDataEmptyAndPartial(t *testing.T) {
	d := managementData(status.Snapshot{})
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") || d.Status.RealmUp {
		t.Fatalf("empty/down snapshot: %s", b)
	}
	d = managementData(status.Snapshot{Unavailable: map[string]bool{"players": true, "leaderboards": true}})
	if d.Status == nil || d.Modules == nil || d.Players != nil || d.Leaderboards != nil {
		t.Fatalf("partial data: %+v", d)
	}
}

func TestManagementAPINoWritesOrCredentialLeak(t *testing.T) {
	ts, s := testWebKB(t, t.TempDir()+"/api.sqlite")
	ts.Close()
	t.Cleanup(func() { _ = s.kb.Close() })
	s.cfg.ManagementAPIToken = testManagementToken
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	h := s.Handler()
	changes := func() int {
		var n int
		if err := s.kb.SQL().QueryRow("SELECT total_changes()").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	for _, method := range []string{"GET", "HEAD", "POST", "DELETE"} {
		for _, token := range []string{"", testManagementToken} {
			w := managementRequest(h, method, "/api/v1/dashboard", token)
			if w.Header().Get("Set-Cookie") != "" {
				t.Fatal("API created session cookie")
			}
		}
	}
	if changes() != before {
		t.Fatal("API wrote application data")
	}
	if strings.Contains(logs.String(), testManagementToken) {
		t.Fatal("API logged credential")
	}
	for _, route := range []struct{ method, path string }{{"GET", "/staff"}, {"POST", "/staff/create"}, {"POST", "/staff/reset"}} {
		w := managementRequest(h, route.method, route.path, testManagementToken)
		if w.Code != 303 && w.Code != 401 && w.Code != 403 {
			t.Fatalf("bearer authorized website action %s: %d", route.path, w.Code)
		}
	}
}
