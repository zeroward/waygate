package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pquerna/otp/totp"
)

type TOTPStatus struct {
	Enabled bool
	Secret  string // only during enroll
	URL     string
}

func (s *Store) TOTPStatus(ctx context.Context, userID uint32) (enabled bool, err error) {
	var en int
	err = s.db.QueryRowContext(ctx, `SELECT enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&en)
	if err != nil {
		return false, nil
	}
	return en != 0, nil
}

func (s *Store) StartTOTP(ctx context.Context, userID uint32, username, issuer string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: username,
	})
	if err != nil {
		return "", "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_totp (user_id, secret, enabled, confirmed_at, recovery_hashes) VALUES (?, ?, 0, '', '[]')
		ON CONFLICT(user_id) DO UPDATE SET secret = excluded.secret, enabled = 0, confirmed_at = '', recovery_hashes = '[]'`,
		userID, key.Secret())
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func (s *Store) ConfirmTOTP(ctx context.Context, userID uint32, code string) ([]string, error) {
	var secret string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT secret, enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&secret, &enabled)
	if err != nil {
		return nil, fmt.Errorf("authenticator is not set up")
	}
	if !totp.Validate(strings.TrimSpace(code), secret) {
		return nil, fmt.Errorf("invalid authenticator code")
	}
	codes, hashes := newRecoveryCodes()
	hjson, _ := json.Marshal(hashes)
	_, err = s.db.ExecContext(ctx, `UPDATE user_totp SET enabled = 1, confirmed_at = datetime('now'), recovery_hashes = ? WHERE user_id = ?`, string(hjson), userID)
	return codes, err
}

func (s *Store) ValidateTOTP(ctx context.Context, userID uint32, code string) error {
	var secret string
	var enabled int
	var recJSON string
	err := s.db.QueryRowContext(ctx, `SELECT secret, enabled, recovery_hashes FROM user_totp WHERE user_id = ?`, userID).Scan(&secret, &enabled, &recJSON)
	if err != nil || enabled == 0 {
		return fmt.Errorf("authenticator is not enabled")
	}
	code = strings.TrimSpace(code)
	if totp.Validate(code, secret) {
		return nil
	}
	var hashes []string
	_ = json.Unmarshal([]byte(recJSON), &hashes)
	sum := sha256.Sum256([]byte(strings.ToUpper(code)))
	want := hex.EncodeToString(sum[:])
	for i, h := range hashes {
		if h == want {
			hashes[i] = hashes[len(hashes)-1]
			hashes = hashes[:len(hashes)-1]
			b, _ := json.Marshal(hashes)
			_, _ = s.db.ExecContext(ctx, `UPDATE user_totp SET recovery_hashes = ? WHERE user_id = ?`, string(b), userID)
			return nil
		}
	}
	return fmt.Errorf("invalid authenticator code")
}

func (s *Store) DisableTOTP(ctx context.Context, userID uint32) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = ?`, userID)
	return err
}

func newRecoveryCodes() (plain, hashes []string) {
	plain = make([]string, 8)
	hashes = make([]string, 8)
	for i := 0; i < 8; i++ {
		b := make([]byte, 5)
		_, _ = rand.Read(b)
		p := strings.ToUpper(hex.EncodeToString(b))
		plain[i] = p
		sum := sha256.Sum256([]byte(p))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	return plain, hashes
}

func (s *Service) TOTPEnabled(ctx context.Context, userID uint32) bool {
	on, _ := s.store.TOTPStatus(ctx, userID)
	return on
}
