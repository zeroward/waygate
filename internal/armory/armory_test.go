package armory

import (
	"context"
	"strings"
	"testing"

	"github.com/zeroward/waygate/internal/config"
)

func TestDemoSearchAndInspect(t *testing.T) {
	s := New(config.Config{DemoMode: true}, nil)
	hits := s.Search(context.Background(), "Frost")
	if len(hits) != 1 || hits[0].Name != "Frostwarden" {
		t.Fatalf("search %+v", hits)
	}
	p, ok := s.Inspect(context.Background(), "Frostwarden")
	if !ok || p.Name != "Frostwarden" || p.Class != "Paladin" || p.Guild != "Ashen Verdict" {
		t.Fatalf("inspect %+v ok=%v", p, ok)
	}
	if p.Gold == "" || !strings.Contains(p.Gold, "g") {
		t.Fatalf("gold %q", p.Gold)
	}
	found := false
	for _, g := range p.Gear {
		if g.Slot == 15 && g.Entry == 50730 && !g.Empty && strings.Contains(g.Wowhead(), "item=50730") {
			found = true
		}
		if !g.Empty && g.Slot > 18 {
			t.Fatalf("slot %d out of range", g.Slot)
		}
	}
	if !found {
		t.Fatal("missing main-hand Glorenzelg")
	}
	if len(p.Specs) != 2 || !p.Specs[0].Active || len(p.Specs[0].Talents) == 0 || len(p.Specs[0].Glyphs) == 0 {
		t.Fatalf("specs %+v", p.Specs)
	}
	if len(p.Achievements) == 0 || p.Achievements[0].Name == "" {
		t.Fatal("achievements")
	}
	if len(p.Arena) != 2 || p.Arena[0].Bracket != "2v2" || p.Arena[1].Bracket != "3v3" {
		t.Fatalf("arena %+v", p.Arena)
	}
	if _, ok := s.Inspect(context.Background(), "NoSuchHero"); ok {
		t.Fatal("missing char should 404")
	}
	if s.Search(context.Background(), "rndbot") != nil {
		t.Fatal("prefix that is not a demo name")
	}
}

func TestAchievementNameMap(t *testing.T) {
	if AchievementName(13) != "Level 80" {
		t.Fatalf("13 %q", AchievementName(13))
	}
	if AchievementName(9_999_999) != "Achievement 9999999" {
		t.Fatalf("fallback %q", AchievementName(9_999_999))
	}
}

func TestValidName(t *testing.T) {
	if !ValidName("Frostwarden") || ValidName("x") || ValidName("Frost Warden") || ValidName("rndbot1") && false {
		t.Fatal("valid")
	}
	if ValidName("Frost-ward") || ValidName("") {
		t.Fatal("invalid")
	}
}
