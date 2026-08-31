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

var (
	ErrNotFound        = errors.New("user not found")
	ErrTaken           = errors.New("username is already taken")
	ErrEmailTaken      = errors.New("email is already registered")
	ErrBadPassword     = errors.New("invalid username or password")
	ErrLinkTaken       = errors.New("that client login is already linked")
	ErrTooMany         = errors.New("too many WoW client logins")
	ErrTooManyPasskeys = errors.New("too many passkeys")
	ErrTooManyWG       = errors.New("too many VPN configs")
	ErrTOTPEnabled     = errors.New("authenticator is already enabled")
)

type User struct {
	ID           uint32
	Username     string
	Email        string
	PasswordHash string
	StaffLevel   uint8
}

type Link struct {
	UserID      uint32
	AccountID   uint32
	Username    string
	SecretBlob  []byte
	SecretNonce []byte
}

func (l Link) HasSecret() bool {
	return len(l.SecretBlob) > 0 && len(l.SecretNonce) > 0
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  staff_level INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE TABLE IF NOT EXISTS wow_links (
  user_id INTEGER NOT NULL,
  account_id INTEGER NOT NULL UNIQUE,
  username TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, username)
);
CREATE TABLE IF NOT EXISTS user_totp (
  user_id INTEGER PRIMARY KEY,
  secret TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 0,
  confirmed_at TEXT NOT NULL DEFAULT '',
  recovery_hashes TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS webauthn_credentials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  credential_id TEXT NOT NULL UNIQUE,
  public_key BLOB NOT NULL,
  sign_count INTEGER NOT NULL DEFAULT 0,
  aaguid TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  cred_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS identity_meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS wg_peers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  public_key TEXT NOT NULL UNIQUE,
  private_key TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE webauthn_credentials ADD COLUMN cred_json TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_webauthn_user ON webauthn_credentials(user_id)`)
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_wg_peers_user ON wg_peers(user_id)`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN dek_wrap BLOB`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN dek_wrap_nonce BLOB`)
	_, _ = db.Exec(`ALTER TABLE wow_links ADD COLUMN secret_blob BLOB`)
	_, _ = db.Exec(`ALTER TABLE wow_links ADD COLUMN secret_nonce BLOB`)
	return nil
}

func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string, staff uint8) (User, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	email = strings.TrimSpace(email)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, email, password_hash, staff_level, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		username, email, passwordHash, int(staff), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrTaken
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: uint32(id), Username: username, Email: email, PasswordHash: passwordHash, StaffLevel: staff}, nil
}

func (s *Store) GetByUsername(ctx context.Context, username string) (User, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	return s.scanUser(s.db.QueryRowContext(ctx, `SELECT id, username, email, password_hash, staff_level FROM users WHERE username = ?`, username))
}

func (s *Store) GetByID(ctx context.Context, id uint32) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, `SELECT id, username, email, password_hash, staff_level FROM users WHERE id = ?`, id))
}

func (s *Store) GetByEmail(ctx context.Context, email string) (User, error) {
	email = strings.TrimSpace(email)
	return s.scanUser(s.db.QueryRowContext(ctx, `SELECT id, username, email, password_hash, staff_level FROM users WHERE LOWER(email) = LOWER(?) AND email <> '' LIMIT 1`, email))
}

func (s *Store) scanUser(row *sql.Row) (User, error) {
	var u User
	var staff int
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &staff)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.StaffLevel = uint8(staff)
	return u, nil
}

func (s *Store) SetPassword(ctx context.Context, id uint32, hash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, now, id)
	return err
}

func (s *Store) DEKWrap(ctx context.Context, userID uint32) (wrap, nonce []byte, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT dek_wrap, dek_wrap_nonce FROM users WHERE id = ?`, userID).Scan(&wrap, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return wrap, nonce, err
}

func (s *Store) SetDEKWrap(ctx context.Context, userID uint32, wrap, nonce []byte) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET dek_wrap = ?, dek_wrap_nonce = ?, updated_at = ? WHERE id = ?`, wrap, nonce, now, userID)
	return err
}

func (s *Store) SetLinkSecret(ctx context.Context, userID uint32, wowUser string, blob, nonce []byte) error {
	wowUser = srp6.UpperLatin(strings.TrimSpace(wowUser))
	res, err := s.db.ExecContext(ctx, `UPDATE wow_links SET secret_blob = ?, secret_nonce = ? WHERE user_id = ? AND username = ?`, blob, nonce, userID, wowUser)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetStaffLevel(ctx context.Context, id uint32, level uint8) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET staff_level = ?, updated_at = ? WHERE id = ?`, int(level), now, id)
	return err
}

func (s *Store) UsernameTaken(ctx context.Context, username string) (bool, error) {
	_, err := s.GetByUsername(ctx, username)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) EmailTaken(ctx context.Context, email string) (bool, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return false, nil
	}
	_, err := s.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) Link(ctx context.Context, userID, accountID uint32, wowUser string) error {
	wowUser = srp6.UpperLatin(strings.TrimSpace(wowUser))
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `INSERT INTO wow_links (user_id, account_id, username, created_at) VALUES (?, ?, ?, ?)`,
		userID, accountID, wowUser, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrLinkTaken
		}
		return err
	}
	return nil
}

func (s *Store) Links(ctx context.Context, userID uint32) ([]Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, account_id, username, secret_blob, secret_nonce FROM wow_links WHERE user_id = ? ORDER BY username`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.UserID, &l.AccountID, &l.Username, &l.SecretBlob, &l.SecretNonce); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) AccountIDs(ctx context.Context, userID uint32) ([]uint32, error) {
	links, err := s.Links(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.AccountID)
	}
	return ids, nil
}

func (s *Store) LinkByAccount(ctx context.Context, accountID uint32) (Link, error) {
	var l Link
	err := s.db.QueryRowContext(ctx, `SELECT user_id, account_id, username, secret_blob, secret_nonce FROM wow_links WHERE account_id = ?`, accountID).
		Scan(&l.UserID, &l.AccountID, &l.Username, &l.SecretBlob, &l.SecretNonce)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

func (s *Store) CountLinks(ctx context.Context, userID uint32) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wow_links WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// AccountOwner is the Gatehouse user that owns a Wow.exe login, plus every
// client username on that identity.
type AccountOwner struct {
	UserID     uint32
	Username   string
	StaffLevel uint8
	Linked     []string
}

func (s *Store) GatehouseByAccountIDs(ctx context.Context, ids []uint32) map[uint32]AccountOwner {
	out := map[uint32]AccountOwner{}
	if len(ids) == 0 {
		return out
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	q := `SELECT l.account_id, u.id, u.username, u.staff_level, sib.username
		FROM wow_links l
		INNER JOIN users u ON u.id = l.user_id
		INNER JOIN wow_links sib ON sib.user_id = u.id
		WHERE l.account_id IN (` + strings.Join(ph, ",") + `)
		ORDER BY sib.username`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	seen := map[uint32]map[string]struct{}{}
	for rows.Next() {
		var accID, userID uint32
		var gh, sib string
		var staff int
		if err := rows.Scan(&accID, &userID, &gh, &staff, &sib); err != nil {
			return out
		}
		o, ok := out[accID]
		if !ok {
			o = AccountOwner{UserID: userID, Username: gh, StaffLevel: uint8(staff)}
			seen[accID] = map[string]struct{}{}
		}
		if _, dup := seen[accID][sib]; !dup {
			seen[accID][sib] = struct{}{}
			o.Linked = append(o.Linked, sib)
		}
		out[accID] = o
	}
	return out
}

func (s *Store) OwnsAccount(ctx context.Context, userID, accountID uint32) bool {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM wow_links WHERE user_id = ? AND account_id = ?`, userID, accountID).Scan(&n)
	return err == nil
}

func (s *Store) ListUsers(ctx context.Context, q string, limit, offset int) ([]User, int, error) {
	if limit < 1 {
		limit = 40
	}
	q = strings.ToUpper(strings.TrimSpace(q))
	var total int
	var rows *sql.Rows
	var err error
	if q == "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = s.db.QueryContext(ctx, `SELECT id, username, email, password_hash, staff_level FROM users ORDER BY username LIMIT ? OFFSET ?`, limit, offset)
	} else {
		like := "%" + q + "%"
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username LIKE ? OR UPPER(email) LIKE ?`, like, like).Scan(&total); err != nil {
			return nil, 0, err
		}
		rows, err = s.db.QueryContext(ctx, `SELECT id, username, email, password_hash, staff_level FROM users WHERE username LIKE ? OR UPPER(email) LIKE ? ORDER BY username LIMIT ? OFFSET ?`, like, like, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var staff int
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &staff); err != nil {
			return nil, 0, err
		}
		u.StaffLevel = uint8(staff)
		out = append(out, u)
	}
	return out, total, rows.Err()
}

const (
	metaWGEndpoint  = "wg_endpoint"
	metaRegisterKey = "register_key"
	metaRegisterSet = "register_key_set"
)

func (s *Store) WGEndpoint() string {
	return s.meta(context.Background(), metaWGEndpoint)
}

func (s *Store) SetWGEndpoint(ctx context.Context, v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM identity_meta WHERE k = ?`, metaWGEndpoint)
		return err
	}
	return s.setMeta(ctx, metaWGEndpoint, v)
}

func (s *Store) RegisterKeyOverride() (key string, set bool) {
	if s.meta(context.Background(), metaRegisterSet) != "1" {
		return "", false
	}
	return s.meta(context.Background(), metaRegisterKey), true
}

func (s *Store) SetRegisterKey(ctx context.Context, v string) error {
	if err := s.setMeta(ctx, metaRegisterSet, "1"); err != nil {
		return err
	}
	return s.setMeta(ctx, metaRegisterKey, strings.TrimSpace(v))
}

func (s *Store) meta(ctx context.Context, k string) string {
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT v FROM identity_meta WHERE k = ?`, k).Scan(&v)
	return v
}

func (s *Store) setMeta(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO identity_meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

func (s *Store) RemapTickets(ctx context.Context) error {
	if s.meta(ctx, "tickets_remapped") == "1" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE tickets SET account_id = (
  SELECT user_id FROM wow_links WHERE wow_links.account_id = tickets.account_id
)
WHERE EXISTS (SELECT 1 FROM wow_links WHERE wow_links.account_id = tickets.account_id)
`)
	if err != nil {
		return fmt.Errorf("remap tickets: %w", err)
	}
	return s.setMeta(ctx, "tickets_remapped", "1")
}
