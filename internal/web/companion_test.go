package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestCompanionRequiresLoginAndDemoTracker(t *testing.T) {
	ts, srv := testWeb(t)
	defer ts.Close()
	if err := srv.accounts.Create(context.Background(), "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}

	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noRedir.Get(ts.URL + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon companion %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "next=") || !strings.Contains(loc, "companion") {
		t.Fatalf("anon next %s", loc)
	}

	res, err = noRedir.Get(ts.URL + "/companion/live?guid=1")
	if err != nil {
		t.Fatal(err)
	}
	liveAnon, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon live %d %s", res.StatusCode, liveAnon)
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
	if !strings.Contains(string(home), `href="/companion"`) {
		t.Fatal("nav missing Companion")
	}
	if iArm := strings.Index(string(home), `href="/armory"`); iArm < 0 {
		t.Fatal("missing armory nav")
	} else if iComp := strings.Index(string(home), `href="/companion"`); iComp < iArm {
		t.Fatal("Companion should follow Armory in the nav")
	}

	res, err = client.Get(ts.URL + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(page)
	if res.StatusCode != 200 {
		t.Fatalf("companion %d", res.StatusCode)
	}
	if !strings.Contains(html, "Frostwarden") || !strings.Contains(html, "Slaves to Saronite") {
		t.Fatal("demo page missing character or quest title")
	}
	if !strings.Contains(html, "Recommended order") || !strings.Contains(html, "Honor Above All Else") {
		t.Fatal("demo page missing region route")
	}
	if !strings.Contains(html, "Icecrown") {
		t.Fatal("missing zone")
	}
	if strings.Contains(html, "/static/maps/") || strings.Contains(html, "companion-pin") {
		t.Fatal("map should not ship in v1")
	}
	if strings.Contains(strings.ToLower(html), "rndbot") {
		t.Fatal("bots offered")
	}
	if !strings.Contains(html, "Last saved by the realm") {
		t.Fatal("missing save-interval copy")
	}
	if !strings.Contains(html, "wowhead.com/wotlk/quest=13300") {
		t.Fatal("missing wowhead quest link")
	}
	if !strings.Contains(html, "/static/js/companion.js") {
		t.Fatal("missing companion.js")
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("csp %s", csp)
	}

	res, err = client.Get(ts.URL + "/companion?guid=2")
	if err != nil {
		t.Fatal(err)
	}
	scout, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(scout), "NorthrendScout") {
		t.Fatalf("scout page %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/companion?guid=99")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other guid page %d", res.StatusCode)
	}

	res, err = client.Get(ts.URL + "/companion/live?guid=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("live %d %s", res.StatusCode, body)
	}
	var live struct {
		Online    bool    `json:"online"`
		MapID     uint32  `json:"mapId"`
		X         float32 `json:"x"`
		Y         float32 `json:"y"`
		Name      string  `json:"name"`
		RouteZone uint32  `json:"routeZone"`
		RouteName string  `json:"routeName"`
		Quests    []struct {
			Title string `json:"title"`
		} `json:"quests"`
		Route []struct {
			Step  int    `json:"step"`
			Title string `json:"title"`
			Now   bool   `json:"now"`
		} `json:"route"`
	}
	if err := json.Unmarshal(body, &live); err != nil {
		t.Fatal(err)
	}
	if !live.Online || live.MapID != 571 || live.Name != "Frostwarden" {
		t.Fatalf("live payload %+v", live)
	}
	if live.X == 0 && live.Y == 0 {
		t.Fatal("live coords")
	}
	foundQuest := false
	for _, q := range live.Quests {
		if q.Title == "Slaves to Saronite" {
			foundQuest = true
		}
	}
	if !foundQuest {
		t.Fatalf("live quests %+v", live.Quests)
	}
	if live.RouteZone != 210 || live.RouteName != "Icecrown" || len(live.Route) == 0 || live.Route[0].Step != 1 {
		t.Fatalf("live route %+v", live.Route)
	}

	res, err = client.Get(ts.URL + "/companion/live?guid=99")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other guid live %d", res.StatusCode)
	}
}
