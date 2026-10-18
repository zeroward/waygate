package identity

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestStoredVerifierCannotDecryptCredentials(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "AuditHero", "Website-Only123", "audit@example.invalid", "AuditGame", "Game-Only456", 2)
	if err != nil {
		t.Fatal(err)
	}
	u, err = id.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(u.PasswordHash, "$")
	stored, err := base64.RawStdEncoding.DecodeString(parts[len(parts)-1])
	if err != nil {
		t.Fatal(err)
	}
	wrap, nonce, err := id.Store().DEKWrap(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openAAD(stored, nonce, wrap, wrapAAD(u.ID)); err == nil {
		t.Fatal("stored verifier decrypted credential key")
	}
	if _, err := openAAD(derive(stored, "dek-wrap"), nonce, wrap, wrapAAD(u.ID)); err == nil {
		t.Fatal("derived stored verifier decrypted credential key")
	}
	if _, err := id.UnlockDEK(ctx, u.ID, "Website-Only123"); err != nil {
		t.Fatal(err)
	}
}
