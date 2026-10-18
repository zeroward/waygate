package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func testVault() *Vault {
	return &Vault{Active: "first", keys: map[string][]byte{"first": bytes.Repeat([]byte{1}, 32)}}
}
func testSSOIdentity(subject string) SSOIdentity {
	return SSOIdentity{Issuer: "https://id.example", Subject: subject, Email: subject + "@example.test", EmailVerified: true}
}
func TestSSOLinkPreservesCredentialsAndRetiresLocalAuth(t *testing.T) {
	id, ac := testID(t)
	ctx := context.Background()
	vault := testVault()
	u, err := id.Register(ctx, "SSOHERO", "Website123", "hero@example.test", "GAMEHERO", "GamePass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = id.store.SetStaffLevel(ctx, u.ID, 2); err != nil {
		t.Fatal(err)
	}
	u, _ = id.GetByID(ctx, u.ID)
	dek, err := id.UnlockDEK(ctx, u.ID, "Website123")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = id.store.StartTOTP(ctx, u.ID, u.Username, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.store.db.Exec(`INSERT INTO webauthn_credentials(user_id,credential_id,public_key,created_at) VALUES(?,'retired',X'01','now')`, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := id.store.LinkSSO(ctx, u, testSSOIdentity("hero"), dek, vault)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || got.StaffLevel != 2 || got.AuthVersion != u.AuthVersion+1 || got.AuthMode != AuthSSO || got.PasswordHash != "!sso" {
		t.Fatalf("wrong linked user: %+v", got)
	}
	for _, table := range []string{"user_totp", "totp_pending", "webauthn_credentials"} {
		var n int
		if err = id.store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, u.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s retained: %d %v", table, n, err)
		}
	}
	unlocked, err := id.store.UnlockSSO(ctx, got, vault)
	if err != nil || !bytes.Equal(dek, unlocked) {
		t.Fatalf("DEK lost: %v", err)
	}
	links, err := id.Links(ctx, u.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("links lost: %v", err)
	}
	plain, err := id.OpenClientPassword(links[0], unlocked)
	if err != nil || plain != "GamePass123" {
		t.Fatalf("game secret lost: %v", err)
	}
	if _, err = ac.Authenticate(ctx, "GAMEHERO", "GamePass123"); err != nil {
		t.Fatalf("game login failed: %v", err)
	}
	if _, err = id.Authenticate(ctx, u.Username, "Website123"); !errors.Is(err, ErrSSOOnly) {
		t.Fatalf("local auth: %v", err)
	}
	if _, err = id.UnlockDEK(ctx, u.ID, "Website123"); !errors.Is(err, ErrSSOOnly) {
		t.Fatalf("local unlock: %v", err)
	}
	if err = id.store.SetDEKWrap(ctx, u.ID, []byte{1}, []byte{2}); err == nil {
		t.Fatal("local DEK setter modified SSO wrapper")
	}
	if err = id.SetPassword(ctx, u.Username, "Another123"); err == nil {
		t.Fatal("reset restored password")
	}
	if err = id.store.ClaimPassword(ctx, u.ID, "!migrated"); err == nil {
		t.Fatal("legacy claim restored password")
	}
	if _, err = id.store.WAUser(ctx, u.ID); !errors.Is(err, ErrSSOOnly) {
		t.Fatalf("passkey user allowed: %v", err)
	}
	if _, _, err = id.store.StartTOTP(ctx, u.ID, u.Username, "test"); !errors.Is(err, ErrSSOOnly) {
		t.Fatalf("TOTP registration allowed: %v", err)
	}
	if err = id.store.InsertPasskey(ctx, u.ID, "new", &webauthn.Credential{ID: []byte{1}, PublicKey: []byte{2}}); !errors.Is(err, ErrSSOOnly) {
		t.Fatalf("passkey registration allowed: %v", err)
	}
	if err = Migrate(id.store.db); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
	if _, err = id.store.UnlockSSO(ctx, got, vault); err != nil {
		t.Fatal("repeat migration broke key", err)
	}
	var vpnTables int
	_ = id.store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='wg_peers'`).Scan(&vpnTables)
	if vpnTables != 0 {
		t.Fatal("fresh schema creates VPN table")
	}
}
func TestSSOLinkingFailuresAreAtomic(t *testing.T) {
	for _, fault := range []string{"duplicate", "stale", "corrupt", "missing-key"} {
		t.Run(fault, func(t *testing.T) {
			id, _ := testID(t)
			ctx := context.Background()
			v := testVault()
			ident := testSSOIdentity("first")
			u, err := id.Register(ctx, "LOCALONE", "Website123", "local@example.test", "GAMEONE", "GamePass123", 2)
			if err != nil {
				t.Fatal(err)
			}
			dek, err := id.UnlockDEK(ctx, u.ID, "Website123")
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "duplicate":
				if _, err = id.store.CreateSSO(ctx, "EXISTING", ident, v); err != nil {
					t.Fatal(err)
				}
			case "stale":
				if err = id.store.BumpAuthVersion(ctx, u.ID); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				_, err = id.store.db.Exec(`UPDATE wow_links SET secret_blob=X'0001' WHERE user_id=?`, u.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				v = nil
			}
			if _, err = id.store.LinkSSO(ctx, u, ident, dek, v); err == nil {
				t.Fatal("invalid link succeeded")
			}
			current, err := id.GetByID(ctx, u.ID)
			if err != nil || current.AuthMode != AuthLocal || current.PasswordHash != u.PasswordHash {
				t.Fatalf("partial conversion: %+v %v", current, err)
			}
			if _, err = id.Authenticate(ctx, u.Username, "Website123"); err != nil {
				t.Fatal("local login lost", err)
			}
		})
	}
}
func TestSSOSignupAndKeyRotation(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	v := testVault()
	ident := testSSOIdentity("new")
	u, err := id.store.CreateSSO(ctx, "NEWPLAYER", ident, v)
	if err != nil {
		t.Fatal(err)
	}
	if u.StaffLevel != 0 || u.AuthMode != AuthSSO {
		t.Fatalf("bad signup: %+v", u)
	}
	links, err := id.Links(ctx, u.ID)
	if err != nil || len(links) != 0 {
		t.Fatal("signup created game account")
	}
	old, err := id.store.UnlockSSO(ctx, u, v)
	if err != nil {
		t.Fatal(err)
	}
	v.keys["second"] = bytes.Repeat([]byte{2}, 32)
	v.Active = "second"
	current, err := id.store.UnlockSSO(ctx, u, v)
	if err != nil || !bytes.Equal(old, current) {
		t.Fatal("rotation lost credentials", err)
	}
	delete(v.keys, "first")
	if _, err = id.store.UnlockSSO(ctx, u, v); err != nil {
		t.Fatal("wrapper did not rotate", err)
	}
	v.keys["second"] = bytes.Repeat([]byte{3}, 32)
	if _, err = id.store.UnlockSSO(ctx, u, v); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err = id.store.CreateSSO(ctx, "OTHERNAME", ident, testVault()); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: %v", err)
	}
	ident.EmailVerified = false
	ident.Email = "other@example.test"
	if _, err = id.store.CreateSSO(ctx, "OTHERNAME", ident, testVault()); err == nil {
		t.Fatal("unverified signup")
	}
}
func TestConcurrentSSOLinkHasOneWinner(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	v := testVault()
	users := make([]User, 2)
	keys := make([][]byte, 2)
	for i, name := range []string{"FIRSTONE", "SECONDONE"} {
		u, err := id.Register(ctx, name, "Website123", name+"@example.test", "", "GamePass123", 2)
		if err != nil {
			t.Fatal(err)
		}
		users[i] = u
		keys[i], err = id.UnlockDEK(ctx, u.ID, "Website123")
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := id.store.LinkSSO(ctx, users[i], testSSOIdentity("same"), keys[i], v)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners %d", winners)
	}
}
func TestVaultFileSecurity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	data := []byte(`{"active":"first","keys":{"first":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)) + `"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVault(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVault(path); err == nil {
		t.Fatal("public file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVault(link); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestSSOPortalDoesNotClaimRealmAccountsByUsername(t *testing.T) {
	id, ac := testID(t)
	ctx := context.Background()
	u, err := id.store.CreateSSO(ctx, "SAMEUSER", testSSOIdentity("new"), testVault())
	if err != nil {
		t.Fatal(err)
	}
	if err = ac.Create(ctx, "SAMEUSER", "GamePass123", "game@example.test", 2); err != nil {
		t.Fatal(err)
	}
	game, err := ac.Authenticate(ctx, "SAMEUSER", "GamePass123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.EnsureFromAC(ctx, game); err == nil {
		t.Fatal("name match gave SSO user an unowned game account")
	}
	links, err := id.Links(ctx, u.ID)
	if err != nil || len(links) != 0 {
		t.Fatal("game account captured")
	}
	if err = id.store.ValidateVault(ctx, testVault()); err != nil {
		t.Fatal(err)
	}
	wrong := testVault()
	wrong.keys["first"] = bytes.Repeat([]byte{9}, 32)
	if err = id.store.ValidateVault(ctx, wrong); err == nil {
		t.Fatal("startup accepted wrong vault key")
	}
}
