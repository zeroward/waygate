package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArmoryRequiresLoginAndDemoInspect(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noRedir.Get(ts.URL + "/armory")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon armory %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "next=") || !strings.Contains(loc, "armory") {
		t.Fatalf("anon next %s", loc)
	}
	res, err = noRedir.Get(ts.URL + "/armory/Frostwarden")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon inspect %d", res.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")

	res, err = client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(home), `href="/armory"`) {
		t.Fatal("nav missing Armory")
	}
	if iLead := strings.Index(string(home), `href="/leaderboards"`); iLead < 0 {
		t.Fatal("missing leaderboards nav")
	} else if iArm := strings.Index(string(home), `href="/armory"`); iArm < iLead {
		t.Fatal("Armory should follow Leaderboards in the nav")
	}

	res, err = client.Get(ts.URL + "/armory")
	if err != nil {
		t.Fatal(err)
	}
	idx, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(idx), `action="/armory"`) {
		t.Fatalf("search page %d", res.StatusCode)
	}
	if !strings.Contains(string(idx), "Your characters") || !strings.Contains(string(idx), "/armory/Frostwarden") || !strings.Contains(string(idx), "NorthrendScout") {
		t.Fatal("armory should list the account's characters by default")
	}
	if !strings.Contains(string(idx), "script-src 'self'") && !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		// CSP is a response header
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("csp %s", csp)
	}

	res, err = client.Get(ts.URL + "/armory?q=Frost")
	if err != nil {
		t.Fatal(err)
	}
	search, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(search), "/armory/Frostwarden") {
		t.Fatalf("search %s", search)
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden")
	if err != nil {
		t.Fatal(err)
	}
	sheet, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("inspect %d", res.StatusCode)
	}
	html := string(sheet)
	if !strings.Contains(html, "Frostwarden") || !strings.Contains(html, "Paladin") || !strings.Contains(html, "Ashen Verdict") {
		t.Fatal("sheet missing name/class/guild")
	}
	if !strings.Contains(html, "1234g") {
		t.Fatal("sheet missing gold")
	}
	if !strings.Contains(html, `id="armory-model"`) || !strings.Contains(html, `data-model="`) {
		t.Fatal("missing 3d model payload")
	}
	if !strings.Contains(html, "3D models from Wowhead") {
		t.Fatal("missing wowhead credit")
	}
	if !strings.Contains(html, "/static/js/armory-model.js") || !strings.Contains(html, "jquery-3.5.1.min.js") {
		t.Fatal("missing model scripts")
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=gear")
	if err != nil {
		t.Fatal(err)
	}
	gear, _ := io.ReadAll(res.Body)
	res.Body.Close()
	ghtml := string(gear)
	if !strings.Contains(ghtml, "Glorenzelg") || !strings.Contains(ghtml, "wowhead.com/wotlk/item=50730") {
		t.Fatal("gear missing item or wowhead link")
	}
	if strings.Contains(ghtml, "bag") && strings.Contains(strings.ToLower(ghtml), "bank") {
		// allowed in copy "Bags and bank are not shown"
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=talents")
	if err != nil {
		t.Fatal(err)
	}
	tal, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(tal), "Crusader Strike") || !strings.Contains(string(tal), "Glyph of") {
		t.Fatal("talents/glyphs")
	}
	if strings.Contains(string(tal), "Talent 12292") || strings.Contains(string(tal), "Talent 35395") {
		t.Fatal("unmapped talent id")
	}
	if !strings.Contains(string(tal), "wowhead.com/wotlk/spell=35395") {
		t.Fatal("missing talent wowhead link")
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=professions")
	if err != nil {
		t.Fatal(err)
	}
	prof, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(prof), "Enchanting") || !strings.Contains(string(prof), "450") {
		t.Fatalf("professions %s", prof)
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=reputations")
	if err != nil {
		t.Fatal(err)
	}
	reps, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(reps), "Argent Crusade") || !strings.Contains(string(reps), "Exalted") {
		t.Fatalf("reputations %s", reps)
	}

	res, err = client.Get(ts.URL + "/armory/guild/Ashen%20Verdict")
	if err != nil {
		t.Fatal(err)
	}
	gpageFull, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(gpageFull), "Ashen Verdict") || !strings.Contains(string(gpageFull), "NorthrendScout") || !strings.Contains(string(gpageFull), "Guild Master") {
		t.Fatalf("guild page %d %s", res.StatusCode, gpageFull)
	}

	res, err = noRedir.Get(ts.URL + "/armory/guild/Ashen%20Verdict")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon guild %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=guild")
	if err != nil {
		t.Fatal(err)
	}
	guild, _ := io.ReadAll(res.Body)
	res.Body.Close()
	gpage := string(guild)
	if res.StatusCode != 200 || !strings.Contains(gpage, "Ashen Verdict") || !strings.Contains(gpage, "NorthrendScout") {
		t.Fatalf("guild tab %d %s", res.StatusCode, gpage)
	}
	if strings.Contains(strings.ToLower(gpage), "rndbot") {
		t.Fatal("bot on guild roster")
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=achievements")
	if err != nil {
		t.Fatal(err)
	}
	ach, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(ach), "Level 80") {
		t.Fatal("achievements")
	}

	res, err = client.Get(ts.URL + "/armory/Frostwarden?tab=pvp")
	if err != nil {
		t.Fatal(err)
	}
	pvp, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(pvp), "2v2") || !strings.Contains(string(pvp), "3v3") {
		t.Fatal("arena brackets")
	}

	res, err = client.Get(ts.URL + "/armory/NoSuchHero")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/online")
	if err != nil {
		t.Fatal(err)
	}
	on, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(on), "/armory/Frostwarden") {
		t.Fatal("online should link names")
	}

	res, err = client.Get(ts.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	acc, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(acc), "/armory/Frostwarden") {
		t.Fatal("account should link names")
	}

	res, err = noRedir.Get(ts.URL + "/armory/mv/viewer/viewer.min.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon model proxy %d", res.StatusCode)
	}
}

func TestArmoryModelProxy(t *testing.T) {
	if !mvPathOK("viewer/viewer.min.js") || !mvPathOK("meta/armor/1/123.json") {
		t.Fatal("ok paths")
	}
	if mvPathOK("../etc/passwd") || mvPathOK("foo//bar") || mvPathOK("") || mvPathOK("a?x=1") {
		t.Fatal("bad paths")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/meta/charactercustomization/2.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()
	prev := mvUpstream
	mvUpstream = up.URL + "/"
	defer func() { mvUpstream = prev }()

	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login(t, client, ts.URL, "HeroOne", "Abcd1234")

	res, err := client.Get(ts.URL + "/armory/mv/../secret")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("dotdot %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/armory/mv/meta/charactercustomization/2.json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}
}
