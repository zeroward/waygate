package kb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrPendingNotFound = errors.New("verification link is invalid or expired")

const pendingTTL = 24 * time.Hour

type PendingSignup struct {
	Username     string
	Email        string
	Salt         []byte
	Verifier     []byte
	Expansion    uint8
	ExpiresAt    time.Time
	PasswordHash string
	WowUsername  string
}

func (s *Store) migratePending() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS pending_accounts (
  token_hash TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL,
  salt BLOB NOT NULL,
  verifier BLOB NOT NULL,
  expansion INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_email ON pending_accounts(email);
CREATE INDEX IF NOT EXISTS idx_pending_exp ON pending_accounts(expires_at);
`)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`ALTER TABLE pending_accounts ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE pending_accounts ADD COLUMN wow_username TEXT NOT NULL DEFAULT ''`)
	return nil
}

func (s *Store) prunePending(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM pending_accounts WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
}

func (s *Store) PutPending(ctx context.Context, p PendingSignup) (string, error) {
	p.Username = strings.ToUpper(strings.TrimSpace(p.Username))
	p.Email = strings.TrimSpace(p.Email)
	if p.Username == "" || p.Email == "" || len(p.Salt) == 0 || len(p.Verifier) == 0 {
		return "", fmt.Errorf("%w: pending", ErrInvalid)
	}
	s.prunePending(ctx)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	plain := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	exp := now.Add(pendingTTL)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM pending_accounts WHERE username = ? OR LOWER(email) = LOWER(?)`, p.Username, p.Email)
	wowUser := strings.ToUpper(strings.TrimSpace(p.WowUsername))
	if wowUser == "" {
		wowUser = p.Username
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pending_accounts (token_hash, username, email, salt, verifier, expansion, created_at, expires_at, password_hash, wow_username) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, p.Username, p.Email, p.Salt, p.Verifier, int(p.Expansion), now.Format(time.RFC3339), exp.Format(time.RFC3339), p.PasswordHash, wowUser)
	if err != nil {
		return "", err
	}
	return plain, nil
}

func (s *Store) ConsumePending(ctx context.Context, token string) (PendingSignup, error) {
	s.prunePending(ctx)
	token = strings.TrimSpace(token)
	if token == "" {
		return PendingSignup{}, ErrPendingNotFound
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	row := s.db.QueryRowContext(ctx, `SELECT username, email, salt, verifier, expansion, expires_at, password_hash, wow_username FROM pending_accounts WHERE token_hash = ?`, hash)
	var p PendingSignup
	var exp string
	var expn int
	err := row.Scan(&p.Username, &p.Email, &p.Salt, &p.Verifier, &expn, &exp, &p.PasswordHash, &p.WowUsername)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingSignup{}, ErrPendingNotFound
	}
	if err != nil {
		return PendingSignup{}, err
	}
	p.Expansion = uint8(expn)
	p.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
	if time.Now().UTC().After(p.ExpiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM pending_accounts WHERE token_hash = ?`, hash)
		return PendingSignup{}, ErrPendingNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pending_accounts WHERE token_hash = ?`, hash); err != nil {
		return PendingSignup{}, err
	}
	return p, nil
}

func (s *Store) DeletePendingToken(ctx context.Context, token string) {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	hash := hex.EncodeToString(sum[:])
	_, _ = s.db.ExecContext(ctx, `DELETE FROM pending_accounts WHERE token_hash = ?`, hash)
}

func (s *Store) HasPendingUsername(ctx context.Context, username string) bool {
	s.prunePending(ctx)
	username = strings.ToUpper(strings.TrimSpace(username))
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM pending_accounts WHERE username = ? LIMIT 1`, username).Scan(&one)
	return err == nil
}

func (s *Store) HasPendingEmail(ctx context.Context, email string) bool {
	s.prunePending(ctx)
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM pending_accounts WHERE LOWER(email) = LOWER(?) LIMIT 1`, email).Scan(&one)
	return err == nil
}
