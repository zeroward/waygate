package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/soap"
	"github.com/zeroward/waygate/internal/status"
)

const testAdminToken = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

func adminTestServer(t *testing.T, soapClient *soap.Client) *Server {
	t.Helper()
	cfg := config.Config{
		DemoMode:           true,
		ManagementAPIToken: testManagementToken,
		AdminAPIToken:      testAdminToken,
		RealmName:          "Icecrown",
		CoreName:           "AzerothCore WotLK 3.3.5a",
		PublicHost:         "127.0.0.1",
		PublicAuthPort:     3724,
		PublicWorldPort:    28085,
		DefaultExpansion:   2,
		PasswordMinLength:  8,
		CaptchaProvider:    "none",
		StatusCache:        20 * time.Second,
		LeaderboardSize:    20,
		BotPrefixes:        []string{"RNDBOT"},
		GMMinLevel:         3,
		GMModLevel:         1,
		RateWindow:         15 * time.Minute,
		RateRegister:       50,
		RateLogin:          50,
		RateContact:        50,
		RateReset:          50,
		RateKB:             50,
		RateTickets:        50,
		KBPath:             t.TempDir() + "/kb.sqlite",
		DownloadsDir:       t.TempDir() + "/downloads",
		AccountMode:        "sql",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, soapClient)
	st := status.New(cfg, nil, soapClient)
	srv, err := New(cfg, log, acc, st, captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.kb.Close() })
	return srv
}

func adminJSONRequest(h http.Handler, method, path, token, actor string, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		rdr = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rdr)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if actor != "" {
		r.Header.Set("X-Admin-Actor", actor)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Cookie", "wg_session=expired-or-revoked")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminAPIAuthAndScopeIsolation(t *testing.T) {
	srv := adminTestServer(t, nil)
	h := srv.Handler()

	// 1. Missing token -> 401
	w := adminJSONRequest(h, "GET", "/api/v1/admin/accounts", "", "", nil)
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected 401 with challenge, got %d: %s", w.Code, w.Body)
	}

	// 2. Read-only MANAGEMENT_API_TOKEN on admin route -> 403 insufficient_scope
	w = adminJSONRequest(h, "GET", "/api/v1/admin/accounts", testManagementToken, "", nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"insufficient_scope"`) {
		t.Fatalf("expected 403 insufficient_scope, got %d: %s", w.Code, w.Body)
	}

	// 3. ADMIN_API_TOKEN on read-only dashboard route -> 200 OK
	w = adminJSONRequest(h, "GET", "/api/v1/status", testAdminToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin token on read-only route failed: %d %s", w.Code, w.Body)
	}

	// 4. Invalid X-Admin-Actor -> 400 invalid_actor
	w = adminJSONRequest(h, "GET", "/api/v1/admin/accounts", testAdminToken, "bad actor!", nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid_actor"`) {
		t.Fatalf("expected 400 invalid_actor, got %d: %s", w.Code, w.Body)
	}

	// 5. Disabled ADMIN_API_TOKEN -> 404 not_found
	srv.cfg.AdminAPIToken = ""
	w = adminJSONRequest(h, "GET", "/api/v1/admin/accounts", testAdminToken, "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when disabled, got %d: %s", w.Code, w.Body)
	}
}

func TestAdminAPIAccountsLifecycle(t *testing.T) {
	srv := adminTestServer(t, nil)
	h := srv.Handler()

	// Create account
	w := adminJSONRequest(h, "POST", "/api/v1/admin/accounts", testAdminToken, "cli-bot", map[string]any{
		"username":  "Arthas",
		"password":  "Frostmourne99",
		"email":     "arthas@lordaeron.example",
		"expansion": 2,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create account: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("admin API set a session cookie")
	}

	// Duplicate account -> 409 Conflict
	w = adminJSONRequest(h, "POST", "/api/v1/admin/accounts", testAdminToken, "cli-bot", map[string]any{
		"username": "Arthas",
		"password": "Frostmourne99",
		"email":    "other@lordaeron.example",
	})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"username_taken"`) {
		t.Fatalf("expected 409 username_taken, got %d: %s", w.Code, w.Body)
	}

	// List accounts
	w = adminJSONRequest(h, "GET", "/api/v1/admin/accounts?q=ARTHAS", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ARTHAS"`) {
		t.Fatalf("list accounts: %d %s", w.Code, w.Body)
	}

	// Inspect account
	w = adminJSONRequest(h, "GET", "/api/v1/admin/accounts/Arthas", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"gatehouse":"ARTHAS"`) {
		t.Fatalf("inspect account: %d %s", w.Code, w.Body)
	}

	// Promote to Admin (rank 3) - allowed because Admin API has effective Super GM (4) ceiling
	w = adminJSONRequest(h, "PUT", "/api/v1/admin/accounts/Arthas/rank", testAdminToken, "cli-bot", map[string]any{
		"rank": 3,
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"rank_name":"Admin"`) {
		t.Fatalf("set rank 3: %d %s", w.Code, w.Body)
	}

	// Super GM (rank 4) -> rejected with 400 invalid_rank
	w = adminJSONRequest(h, "PUT", "/api/v1/admin/accounts/Arthas/rank", testAdminToken, "cli-bot", map[string]any{
		"rank": 4,
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid_rank"`) {
		t.Fatalf("expected 400 for rank 4, got %d: %s", w.Code, w.Body)
	}

	// Reset password
	w = adminJSONRequest(h, "POST", "/api/v1/admin/accounts/Arthas/password", testAdminToken, "cli-bot", map[string]any{
		"password": "NewFrozenPass99",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", w.Code, w.Body)
	}
	if _, err := srv.accounts.Authenticate(context.Background(), "Arthas", "NewFrozenPass99"); err != nil {
		t.Fatalf("new game password did not authenticate: %v", err)
	}
	if _, err := srv.id.Authenticate(context.Background(), "Arthas", "NewFrozenPass99"); err != nil {
		t.Fatalf("new gatehouse password did not authenticate: %v", err)
	}

	// Suspend account
	w = adminJSONRequest(h, "POST", "/api/v1/admin/accounts/Arthas/ban", testAdminToken, "cli-bot", map[string]any{
		"duration": "7d",
		"reason":   "Attacking Stratholme",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"7 days"`) {
		t.Fatalf("ban account: %d %s", w.Code, w.Body)
	}
	if _, err := srv.accounts.Authenticate(context.Background(), "Arthas", "NewFrozenPass99"); err != account.ErrBanned {
		t.Fatalf("expected ErrBanned, got %v", err)
	}

	// Unban account
	w = adminJSONRequest(h, "DELETE", "/api/v1/admin/accounts/Arthas/ban", testAdminToken, "cli-bot", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unban account: %d %s", w.Code, w.Body)
	}
	if _, err := srv.accounts.Authenticate(context.Background(), "Arthas", "NewFrozenPass99"); err != nil {
		t.Fatalf("expected unbanned login to succeed, got %v", err)
	}
}

func TestAdminAPIPortalOperations(t *testing.T) {
	srv := adminTestServer(t, nil)
	h := srv.Handler()

	// Banner PUT / GET / DELETE
	until := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	w := adminJSONRequest(h, "PUT", "/api/v1/admin/banner", testAdminToken, "ops", map[string]any{
		"message": "Scheduled maintenance at 02:00 UTC",
		"until":   until,
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Scheduled maintenance") {
		t.Fatalf("put banner: %d %s", w.Code, w.Body)
	}
	w = adminJSONRequest(h, "GET", "/api/v1/admin/banner", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Scheduled maintenance") {
		t.Fatalf("get banner: %d %s", w.Code, w.Body)
	}
	w = adminJSONRequest(h, "DELETE", "/api/v1/admin/banner", testAdminToken, "ops", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"active":""`) {
		t.Fatalf("delete banner: %d %s", w.Code, w.Body)
	}

	// Register Key PUT / GET / DELETE
	w = adminJSONRequest(h, "PUT", "/api/v1/admin/register-key", testAdminToken, "ops", map[string]any{
		"register_key": "icetime-2026",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"required":true`) {
		t.Fatalf("put register-key: %d %s", w.Code, w.Body)
	}
	w = adminJSONRequest(h, "DELETE", "/api/v1/admin/register-key", testAdminToken, "ops", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"required":false`) {
		t.Fatalf("delete register-key: %d %s", w.Code, w.Body)
	}

	// Calendar POST / GET / DELETE
	w = adminJSONRequest(h, "POST", "/api/v1/admin/calendar", testAdminToken, "ops", map[string]any{
		"date":   "2026-11-01",
		"title":  "Ulduar Timewalking",
		"detail": "25-man hard modes at 19:00 UTC",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create calendar event: %d %s", w.Code, w.Body)
	}
	var calCreated struct {
		Data adminCalendarEventDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &calCreated); err != nil {
		t.Fatal(err)
	}
	w = adminJSONRequest(h, "GET", "/api/v1/admin/calendar", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Ulduar Timewalking") {
		t.Fatalf("list calendar: %d %s", w.Code, w.Body)
	}
	w = adminJSONRequest(h, "DELETE", "/api/v1/admin/calendar/"+strconv.FormatInt(calCreated.Data.ID, 10), testAdminToken, "ops", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete calendar event: %d %s", w.Code, w.Body)
	}

	// Audit Log GET - verify "api:ops" was recorded
	w = adminJSONRequest(h, "GET", "/api/v1/admin/audit?limit=20", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"actor":"api:ops"`) {
		t.Fatalf("audit log: %d %s", w.Code, w.Body)
	}
}

func TestAdminAPITicketsKBAndDownloads(t *testing.T) {
	srv := adminTestServer(t, nil)
	h := srv.Handler()

	// Create a player ticket in the store
	tk, err := srv.kb.CreateTicket(context.Background(), kb.Ticket{
		AccountID: 1,
		Username:  "HEROONE",
		Category:  "Items",
		Title:     "Missing Ashbringer",
	}, "Lost my sword in Naxxramas.")
	if err != nil {
		t.Fatal(err)
	}

	// List tickets
	w := adminJSONRequest(h, "GET", "/api/v1/admin/tickets", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tk.PublicRef) {
		t.Fatalf("list tickets: %d %s", w.Code, w.Body)
	}

	// Reply & close ticket via PATCH
	ticketPath := "/api/v1/admin/tickets/" + strconv.FormatInt(tk.ID, 10)
	w = adminJSONRequest(h, "PATCH", ticketPath, testAdminToken, "gm-bot", map[string]any{
		"status": "done",
		"body":   "Restored via mail.",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"done"`) || !strings.Contains(w.Body.String(), "Restored via mail.") {
		t.Fatalf("patch ticket: %d %s", w.Code, w.Body)
	}

	// KB CRUD
	w = adminJSONRequest(h, "POST", "/api/v1/admin/kb", testAdminToken, "docs-ci", map[string]any{
		"title":         "Raid Rules",
		"slug":          "raid-rules",
		"category":      "Policies",
		"summary":       "Loot and lockout rules.",
		"body_markdown": "## Loot\nBe respectful.",
		"sort_order":    1,
		"published":     true,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create kb: %d %s", w.Code, w.Body)
	}
	var kbCreated struct {
		Data adminArticleDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &kbCreated); err != nil {
		t.Fatal(err)
	}

	// Get KB by slug
	w = adminJSONRequest(h, "GET", "/api/v1/admin/kb/raid-rules", testAdminToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get kb by slug: %d %s", w.Code, w.Body)
	}
	var kbFetched struct {
		Data adminArticleDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &kbFetched); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kbFetched.Data.HTML, "<h2>Loot</h2>") {
		t.Fatalf("expected rendered HTML, got: %s", kbFetched.Data.HTML)
	}

	// Update KB by ID
	kbIDPath := "/api/v1/admin/kb/" + strconv.FormatInt(kbCreated.Data.ID, 10)
	w = adminJSONRequest(h, "PUT", kbIDPath, testAdminToken, "docs-ci", map[string]any{
		"title":         "Updated Raid Rules",
		"slug":          "raid-rules",
		"category":      "Policies",
		"summary":       "Updated summary.",
		"body_markdown": "## Loot\nUpdated rules.",
		"sort_order":    2,
		"published":     true,
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Updated Raid Rules") {
		t.Fatalf("update kb: %d %s", w.Code, w.Body)
	}

	// Delete KB by ID
	w = adminJSONRequest(h, "DELETE", kbIDPath, testAdminToken, "docs-ci", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete kb: %d %s", w.Code, w.Body)
	}

	// Downloads Multipart Upload / List / Delete
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("category", "mods")
	_ = mw.WriteField("title", "Deadly Boss Mods")
	_ = mw.WriteField("version", "3.3.5a")
	part, err := mw.CreateFormFile("file", "DBM-WotLK.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("PK-archive-bytes"))
	_ = mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/admin/downloads", &buf)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload download: %d %s", rec.Code, rec.Body)
	}
	var dlCreated struct {
		Data adminDownloadDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dlCreated); err != nil {
		t.Fatal(err)
	}

	w = adminJSONRequest(h, "GET", "/api/v1/admin/downloads", testAdminToken, "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), dlCreated.Data.ID) {
		t.Fatalf("list downloads: %d %s", w.Code, w.Body)
	}

	w = adminJSONRequest(h, "DELETE", "/api/v1/admin/downloads/"+dlCreated.Data.ID, testAdminToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete download: %d %s", w.Code, w.Body)
	}
}

func TestAdminAPIGameAndSOAPActions(t *testing.T) {
	var executed []string
	soapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		executed = append(executed, string(raw))
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns1="urn:AC"><SOAP-ENV:Body><ns1:executeCommandResponse><result>AzerothCore rev. 2026</result></ns1:executeCommandResponse></SOAP-ENV:Body></SOAP-ENV:Envelope>`))
	}))
	defer soapSrv.Close()

	u, err := url.Parse(soapSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	soapClient := soap.New(u.Hostname(), port, "soapadmin", "soappass", "urn:AC", 5*time.Second)

	srv := adminTestServer(t, soapClient)
	h := srv.Handler()

	// 1. Unstuck offline demo character (GUID 2 is NorthrendScout, offline in demoCharacters)
	w := adminJSONRequest(h, "POST", "/api/v1/admin/game/unstuck", testAdminToken, "gm", map[string]any{
		"name": "NorthrendScout",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"NorthrendScout"`) {
		t.Fatalf("unstuck offline char: %d %s", w.Code, w.Body)
	}

	// Online demo character (GUID 1 is Frostwarden, online in demoCharacters) -> 409 character_online
	w = adminJSONRequest(h, "POST", "/api/v1/admin/game/unstuck", testAdminToken, "gm", map[string]any{
		"guid": 1,
	})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"character_online"`) {
		t.Fatalf("expected 409 character_online, got %d: %s", w.Code, w.Body)
	}

	// 2. Announce via SOAP
	w = adminJSONRequest(h, "POST", "/api/v1/admin/game/announce", testAdminToken, "gm", map[string]any{
		"message": "Server restart in 15 minutes",
		"mode":    "both",
	})
	if w.Code != http.StatusOK || len(executed) != 2 {
		t.Fatalf("announce failed: %d %s (executed=%d)", w.Code, w.Body, len(executed))
	}

	// 3. Raw SOAP safe command
	w = adminJSONRequest(h, "POST", "/api/v1/admin/game/soap", testAdminToken, "gm", map[string]any{
		"command": "server info",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "AzerothCore rev. 2026") {
		t.Fatalf("soap command failed: %d %s", w.Code, w.Body)
	}

	// 4. Raw SOAP credential command must be rejected
	for _, forbiddenCmd := range []string{
		"account create FOO BAR",
		"account set password FOO BAR BAR",
		"account set gmlevel FOO 3 -1",
		".account password FOO BAR BAR",
	} {
		w = adminJSONRequest(h, "POST", "/api/v1/admin/game/soap", testAdminToken, "gm", map[string]any{
			"command": forbiddenCmd,
		})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid_command"`) {
			t.Fatalf("expected 400 invalid_command for %q, got %d: %s", forbiddenCmd, w.Code, w.Body)
		}
	}
}
