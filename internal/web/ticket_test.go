package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/status"
)

func TestTicketsRequireLoginAndOwnership(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	if err := srv.accounts.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.Create(ctx, "OtherTwo", "Abcd1234", "o@example.com", 2); err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noRedir.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon tickets %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "/account?next=") || !strings.Contains(loc, "tickets") {
		t.Fatalf("anon next %s", loc)
	}

	jar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: jar}
	login(t, player, ts.URL, "HeroOne", "Abcd1234")

	res, err = player.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(listBody), "New ticket") {
		t.Fatalf("list %d %s", res.StatusCode, listBody)
	}
	if !strings.Contains(string(listBody), `href="/tickets"`) {
		t.Fatal("user menu missing Tickets")
	}

	res, err = player.Get(ts.URL + "/tickets/new")
	if err != nil {
		t.Fatal(err)
	}
	newBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("new %d", res.StatusCode)
	}
	html := string(newBody)
	if !strings.Contains(html, "Name change") || !strings.Contains(html, "Frostwarden") {
		t.Fatal("missing category or character picker")
	}
	csrf := extractCSRF(html)
	if csrf == "" {
		t.Fatal("no csrf")
	}

	bad := url.Values{"title": {"Nope"}, "category": {"Name change"}, "body": {"x"}}
	res, err = player.PostForm(ts.URL+"/tickets", bad)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf %d", res.StatusCode)
	}

	form := url.Values{
		"csrf_token":     {csrf},
		"category":       {"Name change"},
		"title":          {"Please rename me"},
		"character_guid": {"1"},
		"body":           {"I want a new name. <script>alert(1)</script>"},
	}
	res, err = player.PostForm(ts.URL+"/tickets", form)
	if err != nil {
		t.Fatal(err)
	}
	created, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("create %d %s", res.StatusCode, created)
	}
	page := string(created)
	if !strings.Contains(page, "Please rename me") || !strings.Contains(page, "Frostwarden") {
		t.Fatal("ticket view missing title or character")
	}
	if strings.Contains(page, "<script>alert(1)") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("ticket body must be escaped")
	}
	if !strings.Contains(page, "T-") {
		t.Fatal("missing public ref")
	}

	res, err = player.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := io.ReadAll(res.Body)
	res.Body.Close()
	id := ticketIDFromHTML(string(mine))
	if id == "" {
		t.Fatalf("missing ticket link %s", mine)
	}

	otherJar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: otherJar}
	login(t, other, ts.URL, "OtherTwo", "Abcd1234")
	res, err = other.Get(ts.URL + "/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other player view %d", res.StatusCode)
	}
	res, err = other.PostForm(ts.URL+"/tickets/"+id+"/comment", url.Values{
		"csrf_token": {extractCSRFFromClient(t, other, ts.URL+"/tickets")},
		"body":       {"not yours"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other player comment %d", res.StatusCode)
	}

	res, err = player.Get(ts.URL + "/staff/tickets")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("player staff tickets %d", res.StatusCode)
	}
}

func TestStaffTicketsReplyAndStatus(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 1)

	pjar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: pjar}
	login(t, player, ts.URL, "HeroOne", "Abcd1234")
	res, err := player.Get(ts.URL + "/tickets/new")
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	form := url.Values{
		"csrf_token": {extractCSRF(string(nb))},
		"category":   {"Items"},
		"title":      {"Missing sword"},
		"body":       {"It vanished after a crash."},
	}
	res, err = player.PostForm(ts.URL+"/tickets", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res, err = player.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := io.ReadAll(res.Body)
	res.Body.Close()
	id := ticketIDFromHTML(string(mine))
	if id == "" {
		t.Fatal("missing ticket id")
	}

	sjar, _ := cookiejar.New(nil)
	staff := &http.Client{Jar: sjar}
	login(t, staff, ts.URL, "Staffer", "Abcd1234")

	res, err = staff.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	staffHome, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(staffHome), `href="/staff/tickets"`) {
		t.Fatal("admin panel missing tickets link")
	}
	if !strings.Contains(string(staffHome), "Open tickets") || !strings.Contains(string(staffHome), "Missing sword") {
		t.Fatal("admin panel should list open tickets")
	}

	res, err = staff.Get(ts.URL + "/staff/tickets")
	if err != nil {
		t.Fatal(err)
	}
	queue, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(queue), "Missing sword") || !strings.Contains(string(queue), "HEROONE") && !strings.Contains(string(queue), "HeroOne") {
		t.Fatalf("queue %s", queue)
	}

	res, err = staff.Get(ts.URL + "/staff/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("staff view %d", res.StatusCode)
	}
	csrf := extractCSRF(string(view))
	res, err = staff.PostForm(ts.URL+"/staff/tickets/"+id, url.Values{
		"csrf_token": {csrf},
		"status":     {"in-progress"},
		"body":       {"Looking into it."},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("staff update %d", res.StatusCode)
	}
	up := string(updated)
	if !strings.Contains(up, "Looking into it.") || !strings.Contains(up, "In progress") {
		t.Fatal("missing staff reply or status")
	}

	res, err = player.Get(ts.URL + "/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	playerView, _ := io.ReadAll(res.Body)
	res.Body.Close()
	pv := string(playerView)
	if !strings.Contains(pv, "Looking into it.") || !strings.Contains(pv, "In progress") {
		t.Fatal("player should see staff reply")
	}
	pc := extractCSRF(pv)
	res, err = player.PostForm(ts.URL+"/tickets/"+id+"/comment", url.Values{
		"csrf_token": {pc},
		"body":       {"Thanks, still waiting."},
	})
	if err != nil {
		t.Fatal(err)
	}
	commented, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(commented), "Thanks, still waiting.") {
		t.Fatalf("player comment %d %s", res.StatusCode, commented)
	}

	res, err = staff.PostForm(ts.URL+"/staff/tickets/"+id, url.Values{
		"csrf_token": {csrf},
		"status":     {"done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res, err = staff.Get(ts.URL + "/staff/tickets")
	if err != nil {
		t.Fatal(err)
	}
	closedQ, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(closedQ), "Missing sword") {
		t.Fatal("done ticket should leave the open queue")
	}

	res, err = player.Get(ts.URL + "/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	donePage, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(donePage), "This ticket is closed") {
		t.Fatal("closed ticket should not offer comments")
	}
	res, err = player.PostForm(ts.URL+"/tickets/"+id+"/comment", url.Values{
		"csrf_token": {extractCSRF(string(donePage))},
		"body":       {"too late"},
	})
	if err != nil {
		t.Fatal(err)
	}
	closedBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(closedBody), "too late") {
		t.Fatal("player must not comment after close")
	}
}

func TestTicketOpenRateLimit(t *testing.T) {
	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20,
		BotPrefixes: []string{"RNDBOT"}, GMMinLevel: 1,
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50,
		RateKB: 50, RateTickets: 1, DownloadsDir: t.TempDir(), AccountMode: "sql",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if err := acc.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")
	res, err := client.Get(ts.URL + "/tickets/new")
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(nb))
	form := url.Values{
		"csrf_token": {csrf}, "category": {"Other"}, "title": {"One"}, "body": {"first"},
	}
	res, err = client.PostForm(ts.URL+"/tickets", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("first open %d", res.StatusCode)
	}
	form.Set("title", "Two")
	res, err = client.PostForm(ts.URL+"/tickets", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("second open %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/tickets/new" {
		t.Fatalf("rate limit should bounce to new, got %s", loc)
	}
}

func TestModTicketsNotAdmin(t *testing.T) {
	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20,
		BotPrefixes: []string{"RNDBOT"}, GMMinLevel: 3, GMModLevel: 1,
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50,
		RateKB: 50, RateTickets: 50, DownloadsDir: t.TempDir(), AccountMode: "sql",
		SiteURL: "http://localhost",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "ModOne", "Abcd1234", "m@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := acc.Create(ctx, "AdminOne", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("ModOne", 2)
	acc.GrantGM("AdminOne", 3)

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	mjar, _ := cookiejar.New(nil)
	mod := &http.Client{Jar: mjar, CheckRedirect: noRedir.CheckRedirect}
	login(t, mod, ts.URL, "ModOne", "Abcd1234")

	res, err := mod.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("mod /staff %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/staff/tickets" {
		t.Fatalf("mod redirect %s", loc)
	}

	follow := &http.Client{Jar: mjar}
	res, err = follow.Get(ts.URL + "/staff/tickets")
	if err != nil {
		t.Fatal(err)
	}
	queue, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(queue), "Tickets") {
		t.Fatalf("mod tickets %d %s", res.StatusCode, queue)
	}

	res, err = follow.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(home), "Admin panel") {
		t.Fatal("mod should not see Admin panel")
	}
	if !strings.Contains(string(home), `href="/staff/tickets"`) {
		t.Fatal("mod should see staff tickets")
	}

	res, err = mod.PostForm(ts.URL+"/staff/create", url.Values{
		"csrf_token": {"x"}, "username": {"Newacct"}, "password": {"Abcd1234"}, "password_confirm": {"Abcd1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("mod create %d", res.StatusCode)
	}

	ajar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: ajar}
	login(t, admin, ts.URL, "AdminOne", "Abcd1234")
	res, err = admin.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	adminHome, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(adminHome), "Admin panel") {
		t.Fatalf("admin panel %d", res.StatusCode)
	}
}

func TestTicketNotifyWebhookAndMail(t *testing.T) {
	var gotHook string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotHook = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	var mails []string
	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20,
		BotPrefixes: []string{"RNDBOT"}, GMMinLevel: 1,
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50,
		RateKB: 50, RateTickets: 50, DownloadsDir: t.TempDir(), AccountMode: "sql",
		SiteURL: "http://portal.example", TicketWebhookURL: hook.URL,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	ml := mail.New(cfg)
	ml.Intercept = func(to, subject, body string) error {
		mails = append(mails, to+"|"+subject+"|"+body)
		return nil
	}
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), ml)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := acc.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("Staffer", 2)

	pjar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: pjar}
	login(t, player, ts.URL, "HeroOne", "Abcd1234")
	res, err := player.Get(ts.URL + "/tickets/new")
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	res, err = player.PostForm(ts.URL+"/tickets", url.Values{
		"csrf_token": {extractCSRF(string(nb))},
		"category":   {"Items"},
		"title":      {"Missing sword"},
		"body":       {"It vanished after a crash."},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if !strings.Contains(gotHook, "Missing sword") || !strings.Contains(gotHook, "HEROONE") {
		t.Fatalf("webhook %s", gotHook)
	}
	if len(mails) != 0 {
		t.Fatalf("player open should not mail %v", mails)
	}

	res, err = player.Get(ts.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := io.ReadAll(res.Body)
	res.Body.Close()
	id := ticketIDFromHTML(string(mine))
	if id == "" {
		t.Fatal("missing ticket id")
	}

	sjar, _ := cookiejar.New(nil)
	staff := &http.Client{Jar: sjar}
	login(t, staff, ts.URL, "Staffer", "Abcd1234")
	res, err = staff.Get(ts.URL + "/staff/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	res, err = staff.PostForm(ts.URL+"/staff/tickets/"+id, url.Values{
		"csrf_token": {extractCSRF(string(page))},
		"status":     {"in-progress"},
		"body":       {"Looking into it."},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(mails) != 1 || !strings.Contains(mails[0], "h@example.com") || !strings.Contains(mails[0], "Staff replied") {
		t.Fatalf("staff mail %v", mails)
	}

	res, err = player.Get(ts.URL + "/tickets/" + id)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := io.ReadAll(res.Body)
	res.Body.Close()
	nMail := len(mails)
	res, err = player.PostForm(ts.URL+"/tickets/"+id+"/comment", url.Values{
		"csrf_token": {extractCSRF(string(view))},
		"body":       {"Thanks"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(mails) != nMail {
		t.Fatalf("player comment mailed %v", mails)
	}
}

func ticketIDFromHTML(html string) string {
	const needle = `href="/tickets/`
	i := strings.Index(html, needle)
	if i < 0 {
		return ""
	}
	rest := html[i+len(needle):]
	j := strings.IndexAny(rest, `"`)
	if j < 0 {
		return ""
	}
	id := rest[:j]
	if id == "new" {
		return ticketIDFromHTML(rest[j:])
	}
	return id
}

func extractCSRFFromClient(t *testing.T, client *http.Client, page string) string {
	t.Helper()
	res, err := client.Get(page)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return extractCSRF(string(b))
}
