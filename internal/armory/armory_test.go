package armory

import (
	"context"
	"strings"
	"testing"

	"github.com/zeroward/waygate/internal/config"
)

func TestDemoSearchAndInspect(t *testing.T) {
	s := New(config.Config{DemoMode: true}, nil, nil)
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
	if p.Guild != "Ashen Verdict" || len(p.GuildRoster) < 2 {
		t.Fatalf("guild roster %+v", p.GuildRoster)
	}
	foundScout := false
	for _, m := range p.GuildRoster {
		if m.Name == "NorthrendScout" {
			foundScout = true
		}
		if strings.EqualFold(m.Name, "rndbot") {
			t.Fatal("bot on roster")
		}
	}
	if !foundScout {
		t.Fatal("NorthrendScout should be in Ashen Verdict")
	}
	if len(p.Professions) < 2 || p.Professions[0].Name != "Enchanting" {
		t.Fatalf("professions %+v", p.Professions)
	}
	foundArgent := false
	for _, r := range p.Reputations {
		if r.Name == "Argent Crusade" && r.Rank == "Exalted" {
			foundArgent = true
		}
	}
	if !foundArgent {
		t.Fatalf("reps %+v", p.Reputations)
	}
	g, ok := s.Guild(context.Background(), "Ashen Verdict")
	if !ok || g.Name != "Ashen Verdict" || g.Leader != "Frostwarden" || len(g.Roster) < 2 {
		t.Fatalf("guild page %+v ok=%v", g, ok)
	}
	if _, ok := s.Guild(context.Background(), "No Such Guild"); ok {
		t.Fatal("missing guild")
	}
	if _, ok := s.Inspect(context.Background(), "NoSuchHero"); ok {
		t.Fatal("missing char should 404")
	}
	if s.Search(context.Background(), "rndbot") != nil {
		t.Fatal("prefix that is not a demo name")
	}
}

func TestTalentAndGlyphNames(t *testing.T) {
	if TalentName(12292) != "Death Wish" {
		t.Fatalf("warrior %q", TalentName(12292))
	}
	if TalentName(35395) != "Crusader Strike" {
		t.Fatalf("paladin %q", TalentName(35395))
	}
	if strings.HasPrefix(TalentName(12292), "Talent ") {
		t.Fatal("mapped talent should not fall back")
	}
	if TalentName(9_999_999) != "Talent 9999999" {
		t.Fatalf("fallback %q", TalentName(9_999_999))
	}
	if GlyphName(183) != "Glyph of Judgement" {
		t.Fatalf("glyph 183 %q", GlyphName(183))
	}
	if GlyphName(9_999_999) != "Glyph 9999999" {
		t.Fatalf("glyph fallback %q", GlyphName(9_999_999))
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

func TestValidGuildName(t *testing.T) {
	if !ValidGuildName("Ashen Verdict") || !ValidGuildName("Kul'Tiras") || !ValidGuildName("A-OK") {
		t.Fatal("valid guild")
	}
	if ValidGuildName("x") || ValidGuildName("") || ValidGuildName("no_underscore") {
		t.Fatal("invalid guild")
	}
}

func TestStandingRank(t *testing.T) {
	if StandingRank(42000) != "Exalted" || StandingRank(0) != "Neutral" || StandingRank(-9000) != "Hated" {
		t.Fatal("bands")
	}
}
