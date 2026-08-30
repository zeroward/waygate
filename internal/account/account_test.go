package account

import (
	"context"
	"testing"

	"github.com/zeroward/waygate/internal/config"
)

func TestDemoCreateAndAuth(t *testing.T) {
	cfg := config.Config{DemoMode: true, AccountMode: "sql", RequireUniqueEmail: true}
	s := New(cfg, nil, nil)
	ctx := context.Background()
	if err := s.Create(ctx, "HeroOne", "Abcd1234", "h@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, "heroone", "Abcd1234", "other@example.com", 2); err != ErrTaken {
		t.Fatalf("expected ErrTaken, got %v", err)
	}
	if err := s.Create(ctx, "HeroTwo", "Abcd1234", "h@example.com", 2); err != ErrEmailTaken {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
	acc, err := s.Authenticate(ctx, "heroone", "Abcd1234")
	if err != nil || acc.Username != "HEROONE" {
		t.Fatalf("auth: %+v %v", acc, err)
	}
	if _, err := s.Authenticate(ctx, "HeroOne", "wrongpass1"); err != ErrBadPassword {
		t.Fatalf("wrong pass: %v", err)
	}
	if err := s.ChangePassword(ctx, "HeroOne", "Abcd1234", "Newpass99"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "HeroOne", "Newpass99"); err != nil {
		t.Fatal(err)
	}
}

func TestGMListAndReset(t *testing.T) {
	cfg := config.Config{DemoMode: true, AccountMode: "sql", BotPrefixes: []string{"RNDBOT"}}
	s := New(cfg, nil, nil)
	ctx := context.Background()
	if err := s.Create(ctx, "Staffer", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, "PlayerA", "Abcd1234", "p@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, "Rndbot01", "Abcd1234", "", 2); err != nil {
		t.Fatal(err)
	}
	s.GrantGM("Staffer", 2)
	s.GrantGM("AdminHigh", 3) // missing account: no-op
	if err := s.Create(ctx, "AdminHigh", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	s.GrantGM("AdminHigh", 3)

	rows, total, err := s.ListAccounts(ctx, ListFilter{Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("expected 3 without bots, got %d %+v", total, rows)
	}
	_, totalBots, err := s.ListAccounts(ctx, ListFilter{Limit: 40, IncludeBots: true})
	if err != nil || totalBots != 4 {
		t.Fatalf("with bots: %d %v", totalBots, err)
	}

	if err := s.ResetPasswordByGM(ctx, 2, "PlayerA", "Newpass99"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "PlayerA", "Newpass99"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPasswordByGM(ctx, 2, "AdminHigh", "Newpass99"); err != ErrForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestSetGMLevelRules(t *testing.T) {
	cfg := config.Config{DemoMode: true, AccountMode: "sql"}
	s := New(cfg, nil, nil)
	ctx := context.Background()
	for _, u := range []string{"PlayerA", "Admin", "Boss"} {
		if err := s.Create(ctx, u, "Abcd1234", u+"@example.com", 2); err != nil {
			t.Fatal(err)
		}
	}
	s.GrantGM("Admin", RankAdmin)
	s.GrantGM("Boss", RankSuperGM)

	if err := s.SetGMLevel(ctx, RankAdmin, "Admin", "PlayerA", RankGM); err != nil {
		t.Fatal(err)
	}
	acc, err := s.Authenticate(ctx, "PlayerA", "Abcd1234")
	if err != nil || acc.GMLevel != RankGM {
		t.Fatalf("want GM, got %+v %v", acc, err)
	}

	if err := s.SetGMLevel(ctx, RankAdmin, "Admin", "PlayerA", RankAdmin); err != ErrBadRank {
		t.Fatalf("admin cannot grant admin: %v", err)
	}
	if err := s.SetGMLevel(ctx, RankAdmin, "Admin", "PlayerA", RankSuperGM); err != ErrBadRank {
		t.Fatalf("cannot grant super GM: %v", err)
	}
	if err := s.SetGMLevel(ctx, RankAdmin, "Admin", "Boss", RankGM); err != ErrForbidden {
		t.Fatalf("cannot modify super GM: %v", err)
	}
	if err := s.SetGMLevel(ctx, RankAdmin, "Admin", "Admin", RankGM); err != ErrForbidden {
		t.Fatalf("cannot change own rank: %v", err)
	}
	if err := s.SetGMLevel(ctx, RankSuperGM, "Boss", "PlayerA", RankAdmin); err != nil {
		t.Fatal(err)
	}
	acc, err = s.Authenticate(ctx, "PlayerA", "Abcd1234")
	if err != nil || acc.GMLevel != RankAdmin {
		t.Fatalf("want Admin, got %+v %v", acc, err)
	}
	if err := s.SetGMLevel(ctx, RankSuperGM, "Boss", "PlayerA", RankPlayer); err != nil {
		t.Fatal(err)
	}
	acc, err = s.Authenticate(ctx, "PlayerA", "Abcd1234")
	if err != nil || acc.GMLevel != RankPlayer {
		t.Fatalf("want Player, got %+v %v", acc, err)
	}
}

func TestSOAPCreateCommandWrapper(t *testing.T) {
	// Construction of the SOAP payload is tested in package soap.
	// This guards the auto/sql demo path used when SOAP is not configured.
	cfg := config.Config{DemoMode: false, AccountMode: "sql"}
	s := New(cfg, nil, nil)
	if err := s.Create(context.Background(), "Soapless", "Abcd1234", "s@example.com", 2); err != nil {
		t.Fatal(err)
	}
}
