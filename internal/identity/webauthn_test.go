package identity

import (
	"context"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestPasskeyStore(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	cred := &webauthn.Credential{
		ID:        []byte("cred-one"),
		PublicKey: []byte("pubkey-bytes"),
		Authenticator: webauthn.Authenticator{
			AAGUID:    []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			SignCount: 1,
		},
	}
	if err := id.Store().InsertPasskey(ctx, u.ID, "Laptop", cred); err != nil {
		t.Fatal(err)
	}
	list, err := id.Store().ListPasskeys(ctx, u.ID)
	if err != nil || len(list) != 1 || list[0].Name != "Laptop" {
		t.Fatalf("list %v %v", err, list)
	}
	wu, err := id.Store().WAUser(ctx, u.ID)
	if err != nil || len(wu.Creds) != 1 {
		t.Fatalf("wa %v %+v", err, wu)
	}
	if string(wu.WebAuthnID()) != string(UserHandle(u.ID)) {
		t.Fatal("handle")
	}
	got, err := id.Store().DiscoverableUser(ctx, cred.ID, UserHandle(u.ID))
	if err != nil || got.WebAuthnName() != "HEROONE" {
		t.Fatalf("discover %v %+v", err, got)
	}
	if err := id.Store().DeletePasskey(ctx, u.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := id.Store().DeletePasskey(ctx, u.ID, list[0].ID); err != ErrNotFound {
		t.Fatalf("missing delete %v", err)
	}
}

func TestPasskeyCap(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPasskeys; i++ {
		cred := &webauthn.Credential{
			ID:        []byte{byte(i + 1)},
			PublicKey: []byte("pk"),
		}
		if err := id.Store().InsertPasskey(ctx, u.ID, "k", cred); err != nil {
			t.Fatal(err)
		}
	}
	if err := id.Store().InsertPasskey(ctx, u.ID, "k", &webauthn.Credential{ID: []byte("x"), PublicKey: []byte("pk")}); err != ErrTooManyPasskeys {
		t.Fatalf("cap %v", err)
	}
}

func TestUserHandleRoundTrip(t *testing.T) {
	h := UserHandle(42)
	got, err := ParseUserHandle(h)
	if err != nil || got != 42 {
		t.Fatalf("%v %d", err, got)
	}
	if _, err := ParseUserHandle([]byte{1, 2}); err == nil {
		t.Fatal("short")
	}
}
