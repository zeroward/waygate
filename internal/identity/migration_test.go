package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

func legacyFixture(t *testing.T) (*Service, User, []byte) {
	t.Helper()
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "Migrated", "Website123", "migration@example.invalid", "GameUser", "GamePass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := id.UnlockDEK(ctx, u.ID, "Website123")
	if err != nil {
		t.Fatal(err)
	}
	salt := bytes.Repeat([]byte{23}, 16)
	master := argon2.IDKey([]byte("Website123"), salt, 1, 65536, 4, 32)
	hash := "argon2id$v=19$m=65536,t=1,p=4$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(master)
	nonce, wrap, err := seal(master, dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.store.db.Exec(`UPDATE users SET password_hash=?,dek_wrap=?,dek_wrap_nonce=?,dek_wrap_version=0 WHERE id=?; DELETE FROM identity_meta WHERE k='security_v2'`, hash, wrap, nonce, u.ID); err != nil {
		t.Fatal(err)
	}
	return id, u, dek
}
func TestVersionedMigrationPreservesCredentials(t *testing.T) {
	id, u, dek := legacyFixture(t)
	ctx := context.Background()
	if _, err := id.store.db.Exec(`CREATE TABLE http_sessions(id TEXT); INSERT INTO http_sessions VALUES('old'); INSERT INTO pending_accounts(token_hash,username,email,salt,verifier,expansion,created_at,expires_at,password_hash,wow_username) SELECT 'pending','Pending','p@example.invalid',X'00',X'00',2,'','',password_hash,'Pending' FROM users WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := migrateSecurity(id.store.db); err != nil {
		t.Fatal(err)
	}
	got, err := id.UnlockDEK(ctx, u.ID, "Website123")
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("migration lost key: %v", err)
	}
	links, err := id.Links(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := id.OpenClientPassword(links[0], got)
	if err != nil || plain != "GamePass123" {
		t.Fatal("migration lost saved password", err)
	}
	var pending string
	if err := id.store.db.QueryRow(`SELECT password_hash FROM pending_accounts WHERE token_hash='pending'`).Scan(&pending); err != nil || !CheckPassword(pending, "Website123") {
		t.Fatal("pending hash migration", err)
	}
	current, _ := id.GetByID(ctx, u.ID)
	if current.AuthVersion != u.AuthVersion+1 {
		t.Fatal("deployment did not revoke sessions")
	}
	if err := migrateSecurity(id.store.db); err != nil {
		t.Fatal(err)
	}
	again, _ := id.GetByID(ctx, u.ID)
	if again.PasswordHash != current.PasswordHash || again.AuthVersion != current.AuthVersion {
		t.Fatal("migration not idempotent")
	}
	var n int
	if err := id.store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='http_sessions'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("legacy sessions retained")
	}
}
func TestMigrationBrokenWrapAndUnknownVersion(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "broken-wrap"}[broken], func(t *testing.T) {
			id, u, _ := legacyFixture(t)
			ctx := context.Background()
			if broken {
				_, _ = id.store.db.Exec(`UPDATE users SET dek_wrap=X'0102' WHERE id=?`, u.ID)
			} else {
				_, _ = id.store.db.Exec(`UPDATE users SET password_hash='future-v9' WHERE id=?`, u.ID)
			}
			err := migrateSecurity(id.store.db)
			if !broken {
				if err == nil {
					t.Fatal("unknown format accepted")
				}
				var marker int
				_ = id.store.db.QueryRow(`SELECT count(*) FROM identity_meta WHERE k='security_v2'`).Scan(&marker)
				if marker != 0 {
					t.Fatal("failed migration marked complete")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			links, _ := id.Links(ctx, u.ID)
			if len(links) != 1 || links[0].HasSecret() {
				t.Fatal("broken copy retained or link lost")
			}
			if _, err = id.UnlockDEK(ctx, u.ID, "Website123"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestResetPreservesAccountsAndFactorsButClearsCopies(t *testing.T) {
	id, ac := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "ResetUser", "Website123", "r@example.invalid", "ResetGame", "GamePass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := id.store.StartTOTP(ctx, u.ID, u.Username, "Test")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if _, err = id.store.ConfirmTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	before, _ := id.GetByID(ctx, u.ID)
	if err = id.SetPassword(ctx, u.Username, "NewWebsite123"); err != nil {
		t.Fatal(err)
	}
	after, _ := id.GetByID(ctx, u.ID)
	if after.AuthVersion != before.AuthVersion+1 {
		t.Fatal("reset did not revoke")
	}
	links, _ := id.Links(ctx, u.ID)
	if len(links) != 1 || links[0].HasSecret() {
		t.Fatal("reset copies/links")
	}
	if _, err = ac.Authenticate(ctx, "ResetGame", "GamePass123"); err != nil {
		t.Fatal("actual game password changed", err)
	}
	if on, err := id.TOTPEnabled(ctx, u.ID); !on || err != nil {
		t.Fatal("reset lost MFA", err)
	}
	if _, err = id.UnlockDEK(ctx, u.ID, "NewWebsite123"); err != nil {
		t.Fatal(err)
	}
	if err = id.ChangePassword(ctx, before, "Website123", "RacePassword123"); err == nil {
		t.Fatal("stale password change overwrote reset")
	}
}
func TestConcurrentUnlockUsesOneKey(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	h, err := HashPassword("Website123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := id.store.CreateUser(ctx, "KeyRace", "race@example.invalid", h, 0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	keys := make(chan []byte, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, e := id.UnlockDEK(ctx, u.ID, "Website123")
			if e != nil {
				t.Error(e)
			}
			keys <- k
		}()
	}
	wg.Wait()
	close(keys)
	var first []byte
	for k := range keys {
		if first == nil {
			first = k
		} else if !bytes.Equal(k, first) {
			t.Fatal("concurrent unlock returned different keys")
		}
	}
}
func TestPasswordWriteFailureIsAtomic(t *testing.T) {
	id, u, _ := legacyFixture(t)
	if err := migrateSecurity(id.store.db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, _ := id.GetByID(ctx, u.ID)
	if _, err := id.store.db.Exec(`CREATE TRIGGER fail_wrap BEFORE UPDATE OF dek_wrap ON users BEGIN SELECT RAISE(FAIL,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := id.ChangePassword(ctx, before, "Website123", "NewWebsite123"); err == nil {
		t.Fatal("write failure ignored")
	}
	after, _ := id.GetByID(ctx, u.ID)
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("partial password change")
	}
}
func TestMalformedPasswordRecordsRejected(t *testing.T) {
	for _, h := range []string{"", "argon2id$v=19$m=4294967295,t=1,p=4$a$b", strings.Repeat("$", 100), "waygate-argon2id-v9$v=19$m=65536,t=1,p=4$a$b"} {
		if _, ok := VerifyAndKey(h, "password"); ok {
			t.Fatal("malformed accepted")
		}
	}
}

func TestRecoveryConsumptionConcurrentAndEnrollmentDoesNotReplaceLiveFactor(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.store.CreateUser(ctx, "FactorRace", "f@example.invalid", "!migrated", 0)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := id.store.StartTOTP(ctx, u.ID, u.Username, "Test")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	codes, err := id.store.ConfirmTOTP(ctx, u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = id.store.StartTOTP(ctx, u.ID, u.Username, "Test"); err != ErrTOTPEnabled {
		t.Fatal("enabled factor replaced", err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- id.store.ValidateTOTP(ctx, u.ID, codes[0]) }()
	}
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("recovery successes %d", success)
	}
	if err = id.store.DisableTOTP(ctx, u.ID, u.AuthVersion); err == nil {
		t.Fatal("stale auth version disabled MFA")
	}
}
func TestExpiredEnrollmentCannotConfirm(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.store.CreateUser(ctx, "Expired", "e@example.invalid", "!migrated", 0)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := id.store.StartTOTP(ctx, u.ID, u.Username, "Test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.store.db.Exec(`UPDATE totp_pending SET expires_at=0 WHERE user_id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if _, err = id.store.ConfirmTOTP(ctx, u.ID, code); err == nil {
		t.Fatal("expired enrollment confirmed")
	}
}

func TestFactorChangeFencesPasswordChange(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "FenceUser", "Website123", "fence@example.invalid", "", "GamePass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := id.UnlockDEK(ctx, u.ID, "Website123")
	if err != nil {
		t.Fatal(err)
	}
	if err = id.store.BumpAuthVersion(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = id.ChangePassword(ctx, u, "Website123", "NewWebsite123"); err == nil {
		t.Fatal("stale factor authorization changed password")
	}
	if err = id.SealClientPassword(ctx, u.ID, u.Username, "GamePass123", dek, u.AuthVersion); err == nil {
		t.Fatal("stale authorization stored credential copy")
	}
}

func TestGameLoginCannotReplaceExistingWebsitePassword(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "WebsiteUser", "Website123", "separate@example.invalid", "GameLogin", "GamePass123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = id.Authenticate(ctx, "GameLogin", "GamePass123"); err == nil {
		t.Fatal("game login replaced established website authentication")
	}
	if _, err = id.Authenticate(ctx, u.Username, "Website123"); err != nil {
		t.Fatal("website password changed", err)
	}
}

func TestMigrationMissingWrapClearsUnrecoverableCopies(t *testing.T) {
	id, u, _ := legacyFixture(t)
	if _, err := id.store.db.Exec(`UPDATE users SET dek_wrap=NULL,dek_wrap_nonce=NULL WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := migrateSecurity(id.store.db); err != nil {
		t.Fatal(err)
	}
	links, err := id.Links(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].HasSecret() {
		t.Fatal("unrecoverable copy retained or link lost")
	}
}
