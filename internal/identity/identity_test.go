package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/kb"
)

func testID(t *testing.T) (*Service, *account.Service) {
	t.Helper()
	st, err := kb.Open(filepath.Join(t.TempDir(), "kb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ac := account.New(config.Config{DemoMode: true, AccountMode: "sql", RequireUniqueEmail: true}, nil, nil)
	idStore, err := NewStore(st.SQL())
	if err != nil {
		t.Fatal(err)
	}
	return New(idStore, ac, 5), ac
}

func TestRegisterAndAuth(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil || u.Username != "HEROONE" {
		t.Fatalf("%v %+v", err, u)
	}
	got, err := id.Authenticate(ctx, "heroone", "Abcd1234")
	if err != nil || got.ID != u.ID {
		t.Fatalf("auth %v %+v", err, got)
	}
	if _, err := id.Authenticate(ctx, "heroone", "Wrong999"); err != ErrBadPassword {
		t.Fatalf("bad %v", err)
	}
	ids, err := id.AccountIDs(ctx, u.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("links %v %v", err, ids)
	}
}

func TestLegacyClaim(t *testing.T) {
	id, ac := testID(t)
	ctx := context.Background()
	if err := ac.Create(ctx, "OldOne", "Abcd1234", "o@example.com", 2); err != nil {
		t.Fatal(err)
	}
	ac.GrantGM("OldOne", 2)
	u, err := id.Authenticate(ctx, "OldOne", "Abcd1234")
	if err != nil || u.StaffLevel != 2 {
		t.Fatalf("claim %v %+v", err, u)
	}
	u2, err := id.Authenticate(ctx, "OldOne", "Abcd1234")
	if err != nil || u2.ID != u.ID {
		t.Fatalf("second %v %+v", err, u2)
	}
}

func TestAuthRejectsBannedWowAccount(t *testing.T) {
	id, ac := testID(t)
	ctx := context.Background()
	if err := ac.Create(ctx, "ADMIN", "Abcd1234", "a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	ac.GrantGM("ADMIN", 3)
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := ac.Ban(ctx, 3, "ADMIN", "HEROONE", "perm", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := id.Authenticate(ctx, "HeroOne", "Abcd1234"); !errors.Is(err, account.ErrBanned) {
		t.Fatalf("want banned, got %v", err)
	}
	if err := id.RejectIfBanned(ctx, u); !errors.Is(err, account.ErrBanned) {
		t.Fatalf("reject %v", err)
	}
}

func TestWowSecretRoundTrip(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "ClientPass1", 2)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := id.UnlockDEK(ctx, u.ID, "Abcd1234")
	if err != nil || len(dek) != 32 {
		t.Fatalf("dek %v %d", err, len(dek))
	}
	links, err := id.Links(ctx, u.ID)
	if err != nil || len(links) != 1 || !links[0].HasSecret() {
		t.Fatalf("links %v %+v", err, links)
	}
	got, err := id.OpenClientPassword(links[0], dek)
	if err != nil || got != "ClientPass1" {
		t.Fatalf("open %q %v", got, err)
	}
	dek2, err := id.UnlockDEK(ctx, u.ID, "Abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	got2, err := id.OpenClientPassword(links[0], dek2)
	if err != nil || got2 != "ClientPass1" {
		t.Fatalf("reopen %q %v", got2, err)
	}
	if err := id.ChangePassword(ctx, u, "Abcd1234", "NewPass12"); err != nil {
		t.Fatal(err)
	}
	if _, err := id.UnlockDEK(ctx, u.ID, "Abcd1234"); err != ErrBadPassword {
		t.Fatalf("old kek %v", err)
	}
	dek3, err := id.UnlockDEK(ctx, u.ID, "NewPass12")
	if err != nil {
		t.Fatal(err)
	}
	got3, err := id.OpenClientPassword(links[0], dek3)
	if err != nil || got3 != "ClientPass1" {
		t.Fatalf("after site password change %q %v", got3, err)
	}
}

func TestAddCredentialCap(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "HeroOne", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := id.AddCredential(ctx, u.ID, "HeroAlt", "Abcd1234", "", 2); err != nil {
		t.Fatal(err)
	}
	n, _ := id.store.CountLinks(ctx, u.ID)
	if n != 2 {
		t.Fatalf("n %d", n)
	}
}

func TestHashRoundTrip(t *testing.T) {
	h, err := HashPassword("Abcd1234")
	if err != nil || NeedsLegacy(h) || !CheckPassword(h, "Abcd1234") || CheckPassword(h, "nope") {
		t.Fatalf("hash %v %s", err, h)
	}
}
