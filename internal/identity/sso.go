package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/srp6"
)

const AuthLocal = "local"
const AuthSSO = "sso"

var ErrSSOOnly = errors.New("use single sign-on for this account")
var ErrSSOLinked = errors.New("SSO identity or account is already linked")

// SSOIdentity is obtained only from a verified ID token, never from form fields.
type SSOIdentity struct {
	Issuer, Subject, Email string
	EmailVerified          bool
}

func migrateSSO(db *sql.DB) error {
	for _, c := range [][2]string{{"auth_mode", "TEXT NOT NULL DEFAULT 'local' CHECK(auth_mode IN ('local','sso'))"}, {"sso_dek_key_id", "TEXT NOT NULL DEFAULT ''"}} {
		if err := addColumn(db, "users", c[0], c[1]); err != nil {
			return err
		}
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS sso_identities (
 user_id INTEGER PRIMARY KEY REFERENCES users(id), issuer TEXT NOT NULL, subject TEXT NOT NULL,
 created_at TEXT NOT NULL, UNIQUE(issuer,subject));`)
	return err
}

func checkLocal(ctx context.Context, tx *sql.Tx, id uint32) error {
	var mode string
	if err := tx.QueryRowContext(ctx, `SELECT auth_mode FROM users WHERE id=?`, id).Scan(&mode); err != nil {
		return err
	}
	if mode != AuthLocal {
		return ErrSSOOnly
	}
	return nil
}

func (s *Store) HasSSO(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE auth_mode='sso'`).Scan(&count)
	return count > 0, err
}

func (s *Store) SSOUser(ctx context.Context, identity SSOIdentity) (User, error) {
	var id uint32
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM sso_identities WHERE issuer=? AND subject=?`, identity.Issuer, identity.Subject).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u, err := s.GetByID(ctx, id)
	if err == nil && u.AuthMode != AuthSSO {
		return User{}, ErrSSOLinked
	}
	return u, err
}

func insertSSO(ctx context.Context, tx *sql.Tx, id uint32, identity SSOIdentity) error {
	if identity.Issuer == "" || identity.Subject == "" {
		return fmt.Errorf("missing SSO identity")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sso_identities(user_id,issuer,subject,created_at) VALUES(?,?,?,?)`, id, identity.Issuer, identity.Subject, time.Now().UTC().Format(time.RFC3339))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrSSOLinked
	}
	return err
}

// LinkSSO commits the identity, credential retirement and DEK rewrap together.
// The caller supplies a fresh, fully verified local user and its unlocked key.
func (s *Store) LinkSSO(ctx context.Context, u User, identity SSOIdentity, dek []byte, vault *Vault) (User, error) {
	if vault == nil || len(dek) != dekLen {
		return User{}, fmt.Errorf("credential key unavailable")
	}
	nonce, wrap, err := vault.seal(u.ID, dek)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	// Recheck the actual wrapped key so a stale or substituted DEK cannot lose saved secrets.
	if err = checkLocal(ctx, tx, u.ID); err != nil {
		return User{}, err
	}
	if err = checkVersion(ctx, tx, u.ID, []uint64{u.AuthVersion}); err != nil {
		return User{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT secret_blob,secret_nonce FROM wow_links WHERE user_id=? AND length(secret_blob)>0`, u.ID)
	if err != nil {
		return User{}, err
	}
	for rows.Next() {
		var b, n []byte
		if err = rows.Scan(&b, &n); err != nil {
			rows.Close()
			return User{}, err
		}
		plain, e := open(dek, n, b)
		clear(plain)
		if e != nil {
			rows.Close()
			return User{}, fmt.Errorf("saved game credentials could not be migrated")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return User{}, err
	}
	if err = insertSSO(ctx, tx, u.ID, identity); err != nil {
		return User{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET auth_mode='sso',password_hash='!sso',dek_wrap=?,dek_wrap_nonce=?,dek_wrap_version=3,sso_dek_key_id=?,auth_version=auth_version+1,auth_changed_at=? WHERE id=? AND auth_mode='local' AND auth_version=?`, wrap, nonce, vault.Active, time.Now().UnixNano(), u.ID, u.AuthVersion)
	if err != nil {
		return User{}, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return User{}, ErrSSOLinked
	}
	for _, table := range []string{"user_totp", "totp_pending", "webauthn_credentials"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=?`, u.ID); err != nil {
			return User{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetByID(ctx, u.ID)
}

func (s *Store) CreateSSO(ctx context.Context, username string, identity SSOIdentity, vault *Vault) (User, error) {
	if vault == nil || !identity.EmailVerified || identity.Email == "" {
		return User{}, fmt.Errorf("verified email and credential key required")
	}
	username = srp6.UpperLatin(strings.TrimSpace(username))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE lower(email)=lower(?)`, identity.Email).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrEmailTaken
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `INSERT INTO users(username,email,password_hash,staff_level,created_at,updated_at,auth_mode) VALUES(?,?,'!sso',0,?,?,'sso')`, username, identity.Email, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrTaken
		}
		return User{}, err
	}
	rawID, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	id := uint32(rawID)
	if err = insertSSO(ctx, tx, id, identity); err != nil {
		return User{}, err
	}
	dek, err := newDEK()
	if err != nil {
		return User{}, err
	}
	defer clear(dek)
	nonce, wrap, err := vault.seal(id, dek)
	if err != nil {
		return User{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET dek_wrap=?,dek_wrap_nonce=?,dek_wrap_version=3,sso_dek_key_id=? WHERE id=?`, wrap, nonce, vault.Active, id); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetByID(ctx, id)
}

func (s *Store) UnlockSSO(ctx context.Context, u User, vault *Vault) ([]byte, error) {
	if vault == nil || u.AuthMode != AuthSSO {
		return nil, fmt.Errorf("SSO credential key unavailable")
	}
	var wrap, nonce []byte
	var keyID string
	err := s.db.QueryRowContext(ctx, `SELECT dek_wrap,dek_wrap_nonce,sso_dek_key_id FROM users WHERE id=? AND auth_version=? AND auth_mode='sso' AND dek_wrap_version=3`, u.ID, u.AuthVersion).Scan(&wrap, &nonce, &keyID)
	if err != nil {
		return nil, err
	}
	dek, err := vault.open(u.ID, keyID, nonce, wrap)
	if err != nil {
		return nil, fmt.Errorf("SSO credential key unavailable")
	}
	if keyID != vault.Active {
		n, b, e := vault.seal(u.ID, dek)
		if e != nil {
			clear(dek)
			return nil, e
		}
		_, e = s.db.ExecContext(ctx, `UPDATE users SET dek_wrap=?,dek_wrap_nonce=?,sso_dek_key_id=? WHERE id=? AND auth_version=? AND sso_dek_key_id=?`, b, n, vault.Active, u.ID, u.AuthVersion, keyID)
		if e != nil {
			clear(dek)
			return nil, e
		}
	}
	return dek, nil
}

// ValidateVault fails startup if existing SSO accounts cannot be decrypted with
// the configured keyring. It does not rewrite wrappers or require provider access.
func (s *Store) ValidateVault(ctx context.Context, vault *Vault) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,sso_dek_key_id,dek_wrap_nonce,dek_wrap,dek_wrap_version FROM users WHERE auth_mode='sso'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint32
		var key string
		var nonce, wrap []byte
		var version int
		if err = rows.Scan(&id, &key, &nonce, &wrap, &version); err != nil {
			return err
		}
		if vault == nil || version != 3 {
			return fmt.Errorf("SSO credential key configuration is incomplete")
		}
		dek, e := vault.open(id, key, nonce, wrap)
		valid := e == nil && len(dek) == dekLen
		clear(dek)
		if !valid {
			return fmt.Errorf("SSO vault cannot decrypt credentials for user %d; restore the matching keyring", id)
		}
	}
	return rows.Err()
}
