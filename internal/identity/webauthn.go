package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"
)

const MaxPasskeys = 8

type Passkey struct {
	ID      int64
	Name    string
	Created string
}

type WAUser struct {
	User
	Creds []webauthn.Credential
}

func UserHandle(id uint32) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}

func ParseUserHandle(b []byte) (uint32, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("invalid user handle")
	}
	n := binary.BigEndian.Uint64(b)
	if n == 0 || n > uint64(^uint32(0)) {
		return 0, fmt.Errorf("invalid user handle")
	}
	return uint32(n), nil
}

func (u WAUser) WebAuthnID() []byte {
	return UserHandle(u.ID)
}

func (u WAUser) WebAuthnName() string {
	return u.Username
}

func (u WAUser) WebAuthnDisplayName() string {
	return u.Username
}

func (u WAUser) WebAuthnCredentials() []webauthn.Credential {
	return u.Creds
}

func SanitizePasskeyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Passkey"
	}
	if utf8.RuneCountInString(name) > 40 {
		r := []rune(name)
		name = string(r[:40])
	}
	return name
}

func credIDString(id []byte) string {
	return base64.RawURLEncoding.EncodeToString(id)
}

func (s *Store) CountPasskeys(ctx context.Context, userID uint32) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (s *Store) ListPasskeys(ctx context.Context, userID uint32) ([]Passkey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM webauthn_credentials WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		var p Passkey
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			p.Created = t.Format("2 Jan 2006")
		} else {
			p.Created = created
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) WAUser(ctx context.Context, userID uint32) (WAUser, error) {
	u, err := s.GetByID(ctx, userID)
	if err != nil {
		return WAUser{}, err
	}
	creds, err := s.webAuthnCreds(ctx, userID)
	if err != nil {
		return WAUser{}, err
	}
	return WAUser{User: u, Creds: creds}, nil
}

func (s *Store) webAuthnCreds(ctx context.Context, userID uint32) ([]webauthn.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT credential_id, public_key, sign_count, aaguid, cred_json FROM webauthn_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []webauthn.Credential
	for rows.Next() {
		var credID string
		var pub []byte
		var sign int
		var aaguid, credJSON string
		if err := rows.Scan(&credID, &pub, &sign, &aaguid, &credJSON); err != nil {
			return nil, err
		}
		if c, ok := parseStoredCred(credJSON, credID, pub, uint32(sign), aaguid); ok {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

func parseStoredCred(credJSON, credID string, pub []byte, sign uint32, aaguid string) (webauthn.Credential, bool) {
	if strings.TrimSpace(credJSON) != "" {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(credJSON), &c); err == nil && len(c.ID) > 0 {
			c.Authenticator.SignCount = sign
			if len(c.PublicKey) == 0 {
				c.PublicKey = pub
			}
			return c, true
		}
	}
	id, err := base64.RawURLEncoding.DecodeString(credID)
	if err != nil || len(id) == 0 {
		return webauthn.Credential{}, false
	}
	ag, _ := hex.DecodeString(aaguid)
	return webauthn.Credential{
		ID:        id,
		PublicKey: pub,
		Authenticator: webauthn.Authenticator{
			AAGUID:    ag,
			SignCount: sign,
		},
	}, true
}

func (s *Store) InsertPasskey(ctx context.Context, userID uint32, name string, cred *webauthn.Credential) error {
	if cred == nil || len(cred.ID) == 0 || len(cred.PublicKey) == 0 {
		return fmt.Errorf("invalid passkey")
	}
	n, err := s.CountPasskeys(ctx, userID)
	if err != nil {
		return err
	}
	if n >= MaxPasskeys {
		return ErrTooManyPasskeys
	}
	name = SanitizePasskeyName(name)
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `INSERT INTO webauthn_credentials (user_id, credential_id, public_key, sign_count, aaguid, name, cred_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, credIDString(cred.ID), cred.PublicKey, int(cred.Authenticator.SignCount),
		hex.EncodeToString(cred.Authenticator.AAGUID), name, string(raw), now)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("that passkey is already registered")
	}
	return err
}

func (s *Store) UpdatePasskey(ctx context.Context, cred *webauthn.Credential) error {
	if cred == nil || len(cred.ID) == 0 {
		return fmt.Errorf("invalid passkey")
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE webauthn_credentials SET public_key = ?, sign_count = ?, cred_json = ? WHERE credential_id = ?`,
		cred.PublicKey, int(cred.Authenticator.SignCount), string(raw), credIDString(cred.ID))
	return err
}

func (s *Store) DeletePasskey(ctx context.Context, userID uint32, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserIDByPasskey(ctx context.Context, credID []byte) (uint32, error) {
	var userID uint32
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM webauthn_credentials WHERE credential_id = ?`, credIDString(credID)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return userID, err
}

func (s *Store) DiscoverableUser(ctx context.Context, rawID, userHandle []byte) (webauthn.User, error) {
	var userID uint32
	var err error
	if len(userHandle) > 0 {
		userID, err = ParseUserHandle(userHandle)
		if err != nil {
			return nil, err
		}
	} else {
		userID, err = s.UserIDByPasskey(ctx, rawID)
		if err != nil {
			return nil, err
		}
	}
	wu, err := s.WAUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(rawID) > 0 {
		ok := false
		for _, c := range wu.Creds {
			if string(c.ID) == string(rawID) {
				ok = true
				break
			}
		}
		if !ok {
			return nil, ErrNotFound
		}
	}
	return wu, nil
}
