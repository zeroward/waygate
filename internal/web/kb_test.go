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
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/status"
)

func TestKBPublicIndexAndConnectRedirect(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noRedir.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon kb %d", res.StatusCode)
	}
	resArt, err := noRedir.Get(ts.URL + "/kb/how-to-connect")
	if err != nil {
		t.Fatal(err)
	}
	resArt.Body.Close()
	if resArt.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon article %d", resArt.StatusCode)
	}
	resC, err := noRedir.Get(ts.URL + "/connect")
	if err != nil {
		t.Fatal(err)
	}
	resC.Body.Close()
	if resC.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("connect redirect %d", resC.StatusCode)
	}
	if loc := resC.Header.Get("Location"); loc != "/kb/how-to-connect" {
		t.Fatalf("location %s", loc)
	}
	resSlash, err := noRedir.Get(ts.URL + "/connect/")
	if err != nil {
		t.Fatal(err)
	}
	resSlash.Body.Close()
	if resSlash.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("connect slash redirect %d", resSlash.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")

	res, err = client.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("kb index %d", res.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, "How to connect") {
		t.Fatal("missing seeded article")
	}
	if !strings.Contains(html, "Knowledge Base") {
		t.Fatal("missing nav/title")
	}
	if strings.Contains(html, ">Connect<") {
		t.Fatal("Connect nav should be gone")
	}
	if !strings.Contains(html, "Getting started") {
		t.Fatal("missing category")
	}
	if strings.Contains(html, "New article") {
		t.Fatal("players must not see the editor controls")
	}

	art, err := client.Get(ts.URL + "/kb/how-to-connect")
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := io.ReadAll(art.Body)
	art.Body.Close()
	if art.StatusCode != 200 {
		t.Fatalf("article %d", art.StatusCode)
	}
	apage := string(ab)
	if !strings.Contains(apage, "set realmlist") || !strings.Contains(apage, "Wow.exe") {
		t.Fatal("missing connect copy")
	}
	if !strings.Contains(apage, "/downloads") {
		t.Fatal("article should link to Downloads")
	}
	if !strings.Contains(apage, `class="kb-copy"`) {
		t.Fatal("missing copy button on realmlist")
	}
	if !strings.Contains(apage, "28085") {
		t.Fatal("missing world port")
	}
	if strings.Contains(apage, "<textarea") {
		t.Fatal("editor must not appear on the article page")
	}
	if !strings.Contains(apage, `href="/realmlist.wtf"`) {
		t.Fatal("article missing realmlist download")
	}

	rlAnon, err := noRedir.Get(ts.URL + "/realmlist.wtf")
	if err != nil {
		t.Fatal(err)
	}
	rlAnon.Body.Close()
	if rlAnon.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon realmlist %d", rlAnon.StatusCode)
	}
	rl, err := client.Get(ts.URL + "/realmlist.wtf")
	if err != nil {
		t.Fatal(err)
	}
	rlBody, _ := io.ReadAll(rl.Body)
	rl.Body.Close()
	if rl.StatusCode != 200 {
		t.Fatalf("realmlist %d", rl.StatusCode)
	}
	if disp := rl.Header.Get("Content-Disposition"); !strings.Contains(disp, "realmlist.wtf") {
		t.Fatalf("disposition %s", disp)
	}
	if !strings.Contains(string(rlBody), "set realmlist 127.0.0.1") {
		t.Fatalf("body %q", rlBody)
	}
}

func TestKBHidesDraftsAndXSS(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	ctx := context.Background()
	if err := srv.accounts.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	_, err := srv.kb.Create(ctx, kb.Article{
		Title:        "Secret draft",
		Slug:         "secret-draft",
		Category:     "Staff",
		BodyMarkdown: `<script>alert(1)</script> [bad](javascript:alert(1))`,
		CreatedBy:    "ADMIN",
		UpdatedBy:    "ADMIN",
	})
	if err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	anon, err := noRedir.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon kb %d", anon.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	player := &http.Client{Jar: jar}
	login(t, player, ts.URL, "HeroOne", "Abcd1234")

	res, err := player.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), "Secret draft") {
		t.Fatal("draft listed for players")
	}
	res2, err := player.Get(ts.URL + "/kb/secret-draft")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 404 {
		t.Fatalf("player draft %d", res2.StatusCode)
	}
	resPrev, err := player.Get(ts.URL + "/kb/secret-draft?preview=1")
	if err != nil {
		t.Fatal(err)
	}
	resPrev.Body.Close()
	if resPrev.StatusCode != 404 {
		t.Fatalf("player preview %d", resPrev.StatusCode)
	}

	_, err = srv.kb.Create(ctx, kb.Article{
		Title:        "XSS pub",
		Slug:         "xss-pub",
		Category:     "Getting started",
		BodyMarkdown: `<script>alert(1)</script> [ok](/downloads)`,
		Published:    true,
		CreatedBy:    "ADMIN",
		UpdatedBy:    "ADMIN",
	})
	if err != nil {
		t.Fatal(err)
	}
	res3, err := player.Get(ts.URL + "/kb/xss-pub")
	if err != nil {
		t.Fatal(err)
	}
	b3, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	page := string(b3)
	if strings.Contains(page, "<script>alert") {
		t.Fatal("raw script in article")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("script should be escaped")
	}
	if strings.Contains(page, `href="javascript:`) {
		t.Fatal("javascript link")
	}
}

func TestKBEditorRequiresGM3(t *testing.T) {
	ts, acc := staffTestServer(t)
	defer ts.Close()
	ctx := context.Background()
	if err := acc.Create(ctx, "ModUser", "Abcd1234", "m@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("ModUser", 2)
	if err := acc.Create(ctx, "AdminUser", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("AdminUser", 3)

	jar, _ := cookiejar.New(nil)
	mod := &http.Client{Jar: jar}
	login(t, mod, ts.URL, "ModUser", "Abcd1234")
	res, err := mod.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	staffBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("gm2 staff %d", res.StatusCode)
	}
	if strings.Contains(string(staffBody), `href="/staff/kb"`) {
		t.Fatal("gm2 admin panel must not link to KB editor")
	}
	res, err = mod.Get(ts.URL + "/staff/kb")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("gm2 staff kb %d", res.StatusCode)
	}
	res, err = mod.Get(ts.URL + "/kb/how-to-connect?preview=1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("gm2 can read published %d", res.StatusCode)
	}

	ajar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: ajar}
	login(t, admin, ts.URL, "AdminUser", "Abcd1234")
	res, err = admin.Get(ts.URL + "/staff/kb")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("gm3 staff kb %d", res.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, "New article") || !strings.Contains(html, "how-to-connect") {
		t.Fatal("missing staff table")
	}
	res, err = admin.Get(ts.URL + "/staff")
	if err != nil {
		t.Fatal(err)
	}
	adminStaff, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(adminStaff), `href="/staff/kb"`) {
		t.Fatal("gm3 admin panel missing Knowledge Base link")
	}

	res, err = admin.Get(ts.URL + "/staff/kb/new")
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(nb), `data-kb-body`) || !strings.Contains(string(nb), "data-kb-preview") {
		t.Fatal("missing editor panes")
	}
	csrf := extractCSRF(string(nb))

	form := url.Values{
		"csrf_token":    {csrf},
		"title":         {"Patch notes"},
		"slug":          {"patch-notes"},
		"category":      {"News"},
		"summary":       {"This week's patches."},
		"body_markdown": {"## Hello\n\nUse [Downloads](/downloads).\n\n```\nset realmlist test\n```\n"},
		"sort_order":    {"5"},
	}
	res, err = admin.PostForm(ts.URL+"/staff/kb", form)
	if err != nil {
		t.Fatal(err)
	}
	createdBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create %d %s", res.StatusCode, createdBody)
	}

	res, err = mod.Get(ts.URL + "/kb/patch-notes")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unpublished visible %d", res.StatusCode)
	}
	res, err = mod.Get(ts.URL + "/kb/patch-notes?preview=1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("gm2 draft preview %d", res.StatusCode)
	}
	res, err = mod.Get(ts.URL + "/kb")
	if err != nil {
		t.Fatal(err)
	}
	modIndex, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(modIndex), "New article") || strings.Contains(string(modIndex), "Patch notes") {
		t.Fatal("gm2 must not see editor controls or drafts on the public index")
	}

	res, err = admin.Get(ts.URL + "/kb/patch-notes?preview=1")
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("preview %d", res.StatusCode)
	}
	if !strings.Contains(string(pb), "Draft preview") || !strings.Contains(string(pb), "set realmlist test") {
		t.Fatal("preview content")
	}
	if strings.Contains(string(pb), `name="body_markdown"`) {
		t.Fatal("preview must not include the editor")
	}

	res, err = admin.PostForm(ts.URL+"/staff/kb/preview", url.Values{
		"csrf_token":    {csrf},
		"body_markdown": {"**bold** <script>x</script>\n\n```\nset realmlist test\n```\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prevHTML, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("preview post %d", res.StatusCode)
	}
	ph := string(prevHTML)
	if !strings.Contains(ph, "<strong>bold</strong>") || !strings.Contains(ph, `class="kb-copy"`) {
		t.Fatalf("preview html %s", ph)
	}
	if strings.Contains(ph, "<script>x") || !strings.Contains(ph, "&lt;script&gt;") {
		t.Fatal("preview must escape raw HTML")
	}
	res, err = mod.PostForm(ts.URL+"/staff/kb/preview", url.Values{
		"csrf_token":    {extractCSRF(string(staffBody))},
		"body_markdown": {"nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("gm2 preview post %d", res.StatusCode)
	}

	bad := url.Values{"title": {"no csrf"}, "slug": {"no-csrf"}, "body_markdown": {"x"}}
	res, err = admin.PostForm(ts.URL+"/staff/kb", bad)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf %d", res.StatusCode)
	}

	list, err := admin.Get(ts.URL + "/staff/kb")
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := io.ReadAll(list.Body)
	list.Body.Close()
	editHref := findKBEditID(string(lb), "patch-notes")
	if editHref == "" {
		t.Fatal("missing edit link for patch-notes")
	}
	form.Set("published", "1")
	form.Set("body_markdown", "## Hello\n\nPublished now.\n")
	res, err = admin.PostForm(ts.URL+editHref, form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res, err = mod.Get(ts.URL + "/kb/patch-notes")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(pub), "Published now") {
		t.Fatalf("published article %d %s", res.StatusCode, pub)
	}

	del := url.Values{"csrf_token": {csrf}}
	res, err = admin.PostForm(ts.URL+editHref+"/delete", del)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = mod.Get(ts.URL + "/kb/patch-notes")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("deleted %d", res.StatusCode)
	}
}

func TestKBSaveRateLimit(t *testing.T) {
	cfg := config.Config{
		DemoMode: true, RealmName: "Icecrown", CoreName: "AzerothCore WotLK 3.3.5a",
		PublicHost: "127.0.0.1", PublicAuthPort: 3724, PublicWorldPort: 28085,
		DefaultExpansion: 2, PasswordMinLength: 8, CaptchaProvider: "none",
		StatusCache: 20 * time.Second, LeaderboardSize: 20,
		BotPrefixes: []string{"RNDBOT"}, GMMinLevel: 1,
		RateWindow: 15 * time.Minute, RateRegister: 50, RateLogin: 50, RateContact: 50, RateReset: 50,
		RateKB: 1, DownloadsDir: t.TempDir(), AccountMode: "sql",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acc := account.New(cfg, nil, nil)
	srv, err := New(cfg, log, acc, status.New(cfg, nil, nil), captcha.New(cfg), mail.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if err := acc.Create(context.Background(), "AdminUser", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	acc.GrantGM("AdminUser", 3)
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	login(t, admin, ts.URL, "AdminUser", "Abcd1234")
	res, err := admin.Get(ts.URL + "/staff/kb/new")
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := extractCSRF(string(nb))
	form := url.Values{
		"csrf_token": {csrf}, "title": {"One"}, "slug": {"one"},
		"category": {"News"}, "body_markdown": {"a"},
	}
	res, err = admin.PostForm(ts.URL+"/staff/kb", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("first save %d", res.StatusCode)
	}
	form.Set("title", "Two")
	form.Set("slug", "two")
	res, err = admin.PostForm(ts.URL+"/staff/kb", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("second save status %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/staff/kb/new" {
		t.Fatalf("rate limit should bounce to new, got %s", loc)
	}
}

func findKBEditID(html, slug string) string {
	idx := strings.Index(html, "<code>"+slug+"</code>")
	if idx < 0 {
		return ""
	}
	chunk := html[:idx]
	i := strings.LastIndex(chunk, `href="/staff/kb/`)
	if i < 0 {
		return ""
	}
	rest := chunk[i+len(`href="`):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
