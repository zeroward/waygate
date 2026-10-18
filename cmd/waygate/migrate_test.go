package main

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/kb"
)

func TestOfflineMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "kb.sqlite")
	db, err := kb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewStore(db.SQL())
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("synthetic-salt16")
	master := argon2.IDKey([]byte("Password123"), salt, 1, 65536, 4, 32)
	hash := "argon2id$v=19$m=65536,t=1,p=4$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(master)
	u, err := ids.CreateUser(context.Background(), "Offline", "fixture@example.invalid", hash, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL().Exec(`DELETE FROM identity_meta WHERE k='security_v2'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = migrateDatabase(path); err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(path); err != nil {
		t.Fatal("idempotent migration", err)
	}
	db, err = kb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids, err = identity.NewStore(db.SQL())
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := ids.GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.CheckPassword(migrated.PasswordHash, "Password123") || migrated.AuthVersion != 1 {
		t.Fatal("offline migration did not preserve password and revoke once")
	}
}
