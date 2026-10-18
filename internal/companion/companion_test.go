package companion

import (
	"context"
	"testing"

	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/wow"
)

func TestSelectGUID(t *testing.T) {
	picks := []Pick{
		{GUID: 1, Name: "Frostwarden", Online: true},
		{GUID: 2, Name: "NorthrendScout", Online: false},
	}
	if got := SelectGUID(picks, 0); got != 1 {
		t.Fatalf("one online: %d", got)
	}
	if got := SelectGUID(picks, 2); got != 2 {
		t.Fatalf("requested: %d", got)
	}
	if got := SelectGUID(picks, 99); got != 0 {
		t.Fatalf("unknown: %d", got)
	}
	twoOnline := []Pick{{GUID: 1, Online: true}, {GUID: 3, Online: true}}
	if got := SelectGUID(twoOnline, 0); got != 1 {
		t.Fatalf("two online default first: %d", got)
	}
	if got := SelectGUID(nil, 0); got != 0 {
		t.Fatalf("empty: %d", got)
	}
}

func TestDemoSnapshot(t *testing.T) {
	s := New(config.Config{DemoMode: true}, nil, nil)
	picks := s.List(context.Background(), nil)
	if len(picks) != 2 || picks[0].Name != "Frostwarden" || picks[1].Name != "NorthrendScout" {
		t.Fatalf("picks %+v", picks)
	}
	for _, p := range picks {
		if len(p.Name) >= 6 && (p.Name[:6] == "Rndbot" || p.Name[:6] == "rndbot") {
			t.Fatal("bots offered")
		}
	}
	snap, ok := s.Snapshot(context.Background(), nil, 1, 0)
	if !ok {
		t.Fatal("frostwarden missing")
	}
	if snap.Name != "Frostwarden" || snap.MapID != 571 || !snap.Online {
		t.Fatalf("snap %+v", snap)
	}
	if snap.Location != wow.Location(571, 210) {
		t.Fatalf("location %q", snap.Location)
	}
	if snap.X == 0 && snap.Y == 0 {
		t.Fatal("coords")
	}
	found := false
	for _, q := range snap.Quests {
		if q.Title == "Slaves to Saronite" && q.Wowhead() == "https://www.wowhead.com/wotlk/quest=13300" {
			found = true
			if q.StatusLabel() != "In progress" {
				t.Fatalf("status %q", q.StatusLabel())
			}
		}
	}
	if !found {
		t.Fatal("missing quest title")
	}
	if snap.RouteZone != 210 || snap.RouteName != "Icecrown" || len(snap.Route) == 0 {
		t.Fatalf("route %+v", snap.Route)
	}
	if snap.Route[0].Step != 1 || snap.Route[0].Title == "" {
		t.Fatalf("first step %+v", snap.Route[0])
	}
	now := 0
	for _, st := range snap.Route {
		if st.Now {
			now++
		}
	}
	if now != 1 {
		t.Fatalf("now markers %d", now)
	}
	if _, ok := s.Snapshot(context.Background(), nil, 99, 0); ok {
		t.Fatal("unknown guid")
	}
}

func TestStatusKey(t *testing.T) {
	if StatusKey(1) != "complete" || StatusKey(3) != "incomplete" || StatusKey(5) != "failed" {
		t.Fatal(StatusKey(1), StatusKey(3), StatusKey(5))
	}
}

func TestQuestTitle(t *testing.T) {
	if QuestTitle(7, "  Kobold Camp Cleanup \n") != "Kobold Camp Cleanup" {
		t.Fatal("trim")
	}
	if QuestTitle(7, "") != "Quest 7" {
		t.Fatal("fallback")
	}
}
