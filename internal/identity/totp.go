package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
)

type TOTPStatus struct {
	Enabled bool
	Secret  string // only during enroll
	URL     string
}

func (s *Store) TOTPStatus(ctx context.Context, userID uint32) (bool, error) {
	var en int
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM user_totp WHERE user_id=?`, userID).Scan(&en)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return en != 0, err
}
func (s *Store) StartTOTP(ctx context.Context, userID uint32, username, issuer string, expected ...uint64) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: username})
	if err != nil {
		return "", "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if err := checkLocal(ctx, tx, userID); err != nil {
		return "", "", err
	}
	if err := checkVersion(ctx, tx, userID, expected); err != nil {
		return "", "", err
	}
	var on int
	err = tx.QueryRowContext(ctx, `SELECT enabled FROM user_totp WHERE user_id=?`, userID).Scan(&on)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	if on != 0 {
		return "", "", ErrTOTPEnabled
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO totp_pending(user_id,secret,expires_at,auth_version) SELECT ?,?,?,auth_version FROM users WHERE id=? ON CONFLICT(user_id) DO UPDATE SET secret=excluded.secret,expires_at=excluded.expires_at,auth_version=excluded.auth_version`, userID, key.Secret(), time.Now().Add(10*time.Minute).Unix(), userID)
	if err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}
func (s *Store) PendingTOTP(ctx context.Context, userID uint32, username, issuer string) (string, string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM totp_pending WHERE user_id=? AND expires_at>? AND auth_version=(SELECT auth_version FROM users WHERE id=totp_pending.user_id)`, userID, time.Now().Unix()).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return secret, totpAuthURL(issuer, username, secret), nil
}

func totpAuthURL(issuer, username, secret string) string {
	iss := url.QueryEscape(issuer)
	return "otpauth://totp/" + url.PathEscape(issuer) + ":" + url.PathEscape(username) + "?secret=" + secret + "&issuer=" + iss
}

// QRDataURI returns a PNG data URI for an otpauth URL (CSP allows img-src data:).
func QRDataURI(otpauth string) (string, error) {
	if otpauth == "" {
		return "", fmt.Errorf("empty otpauth url")
	}
	png, err := qrcode.Encode(otpauth, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// acceptedStep identifies the exact timestep that will be consumed atomically.
func acceptedStep(secret, code string) (int64, bool) {
	now := time.Now().Unix() / 30
	for _, delta := range []int64{0, -1, 1} {
		step := now + delta
		want, err := totp.GenerateCode(secret, time.Unix(step*30, 0))
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(code))) == 1 {
			return step, true
		}
	}
	return 0, false
}
func (s *Store) ConfirmTOTP(ctx context.Context, userID uint32, code string, expected ...uint64) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkLocal(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := checkVersion(ctx, tx, userID, expected); err != nil {
		return nil, err
	}
	var secret string
	err = tx.QueryRowContext(ctx, `SELECT secret FROM totp_pending WHERE user_id=? AND expires_at>? AND auth_version=(SELECT auth_version FROM users WHERE id=totp_pending.user_id)`, userID, time.Now().Unix()).Scan(&secret)
	if err != nil {
		return nil, fmt.Errorf("authenticator setup expired")
	}
	step, ok := acceptedStep(secret, code)
	if !ok {
		return nil, fmt.Errorf("invalid authenticator code")
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(hashes)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_totp(user_id,secret,enabled,confirmed_at,recovery_hashes,last_step) VALUES(?,?,1,datetime('now'),?,?)`, userID, secret, string(raw), step)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM totp_pending WHERE user_id=?`, userID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET auth_version=auth_version+1,auth_changed_at=? WHERE id=?`, time.Now().UnixNano(), userID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}
func (s *Store) ValidateTOTP(ctx context.Context, userID uint32, code string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkLocal(ctx, tx, userID); err != nil {
		return err
	}
	var secret, raw string
	var enabled int
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT secret,enabled,recovery_hashes,last_step FROM user_totp WHERE user_id=?`, userID).Scan(&secret, &enabled, &raw, &last)
	if err != nil {
		return err
	}
	if enabled == 0 {
		return fmt.Errorf("authenticator not enabled")
	}
	if step, ok := acceptedStep(secret, code); ok {
		if step <= last {
			return fmt.Errorf("authenticator code already used; wait for a new code")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_totp SET last_step=? WHERE user_id=?`, step, userID); err != nil {
			return err
		}
	} else {
		var hashes []string
		if err = json.Unmarshal([]byte(raw), &hashes); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
		want := hex.EncodeToString(sum[:])
		match := -1
		for i, h := range hashes {
			if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
				match = i
			}
		}
		if match < 0 {
			return fmt.Errorf("invalid authenticator code")
		}
		hashes = append(hashes[:match], hashes[match+1:]...)
		b, err := json.Marshal(hashes)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_totp SET recovery_hashes=? WHERE user_id=?`, string(b), userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) DisableTOTP(ctx context.Context, userID uint32, expected ...uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkLocal(ctx, tx, userID); err != nil {
		return err
	}
	if err := checkVersion(ctx, tx, userID, expected); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM user_totp WHERE user_id=?`, `DELETE FROM totp_pending WHERE user_id=?`} {
		if _, err = tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET auth_version=auth_version+1,auth_changed_at=? WHERE id=?`, time.Now().UnixNano(), userID); err != nil {
		return err
	}
	return tx.Commit()
}
func newRecoveryCodes() ([]string, []string, error) {
	plain := make([]string, 8)
	hashes := make([]string, 8)
	for i := range plain {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		plain[i] = strings.ToUpper(hex.EncodeToString(b))
		sum := sha256.Sum256([]byte(plain[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	return plain, hashes, nil
}
func (s *Service) TOTPEnabled(ctx context.Context, userID uint32) (bool, error) {
	return s.store.TOTPStatus(ctx, userID)
}
