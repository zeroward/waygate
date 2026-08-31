package status

import (
	"context"
	"strings"
	"testing"

	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
)

func TestDemoAccountCharacters(t *testing.T) {
	c := New(config.Config{
		DemoMode:    true,
		RealmName:   "Icecrown",
		BotPrefixes: []string{"RNDBOT"},
		StatusCache: 1,
	}, nil, nil)
	chars, err := c.AccountCharacters(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(chars) < 1 {
		t.Fatal("demo characters empty")
	}
	if chars[0].Name == "" || chars[0].Gold == "" || chars[0].Location == "" {
		t.Fatalf("incomplete: %+v", chars[0])
	}
	if chars[0].Login != "HEROONE" {
		t.Fatalf("login %q", chars[0].Login)
	}
}

func TestDemoUnstuck(t *testing.T) {
	c := New(config.Config{DemoMode: true, StatusCache: 1}, nil, nil)
	ctx := context.Background()
	if _, err := c.Unstuck(ctx, 1, 1); err != ErrCharOnline {
		t.Fatalf("online: %v", err)
	}
	res, err := c.Unstuck(ctx, 1, 2)
	if err != nil || res.Name != "NorthrendScout" || res.Via != "demo" {
		t.Fatalf("offline %+v %v", res, err)
	}
	if _, err := c.Unstuck(ctx, 1, 99); err != ErrCharNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestDemoOnlineBreakdown(t *testing.T) {
	c := New(config.Config{
		DemoMode:    true,
		RealmName:   "Icecrown",
		BotPrefixes: []string{"RNDBOT"},
		StatusCache: 1,
	}, nil, nil)
	s := c.Get(context.Background())
	if s.Online != 9 {
		t.Fatalf("players %d", s.Online)
	}
	if s.OnlineBots != 24 {
		t.Fatalf("bots %d", s.OnlineBots)
	}
	if s.OnlineGMs != 1 {
		t.Fatalf("gms %d", s.OnlineGMs)
	}
	if s.OnlineTotal != 33 {
		t.Fatalf("total %d", s.OnlineTotal)
	}
	if len(s.Gold) < 1 || s.Gold[0].Name != "Frostwarden" || !strings.Contains(s.Gold[0].Value, "g") {
		t.Fatalf("demo gold board %+v", s.Gold)
	}
	for _, row := range s.Gold {
		if strings.HasPrefix(strings.ToUpper(row.Name), "RNDBOT") {
			t.Fatalf("bot on gold board %s", row.Name)
		}
	}
	if len(s.Modules) < 3 {
		t.Fatalf("demo modules %d", len(s.Modules))
	}
}

func TestFiltersHideGM(t *testing.T) {
	c := New(config.Config{BotPrefixes: []string{"RNDBOT"}}, &db.DB{AuthDB: "acore_auth"}, nil)
	show, _ := c.filters(false, false)
	if strings.Contains(show, "gmlevel") || strings.Contains(show, "extra_flags") {
		t.Fatalf("boards should include GMs: %s", show)
	}
	hide, _ := c.filters(false, true)
	if !strings.Contains(hide, "gmlevel") || !strings.Contains(hide, "extra_flags") {
		t.Fatalf("HIDE_GM should omit GMs: %s", hide)
	}
	if !strings.Contains(show, "UPPER(c.`name`)") {
		t.Fatalf("AH-bot denylist missing from board filter: %s", show)
	}
}

func TestBotPredicatePlaceholders(t *testing.T) {
	c := New(config.Config{BotPrefixes: []string{"RNDBOT", "PLAYERBOT"}}, nil, nil)
	sql, args := c.botPredicate()
	if sql != "(a.`username` LIKE ? OR a.`username` LIKE ?)" {
		t.Fatalf("sql %s", sql)
	}
	if len(args) != 2 || args[0] != "RNDBOT%" || args[1] != "PLAYERBOT%" {
		t.Fatalf("args %#v", args)
	}
	c2 := New(config.Config{}, nil, nil)
	sql2, args2 := c2.botPredicate()
	if sql2 != "0" || args2 != nil {
		t.Fatalf("empty prefixes: %s %#v", sql2, args2)
	}
}
