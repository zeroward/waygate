package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type WGPeer struct {
	ID         int64
	UserID     uint32
	Name       string
	PublicKey  string
	PrivateKey string
	Address    string
	Created    string
}

func SanitizeWGName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Device"
	}
	if utf8.RuneCountInString(name) > 40 {
		name = string([]rune(name)[:40])
	}
	return name
}

func (s *Store) CountWGPeers(ctx context.Context, userID uint32) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wg_peers WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (s *Store) ListWGPeers(ctx context.Context, userID uint32) ([]WGPeer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, public_key, private_key, address, created_at FROM wg_peers WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WGPeer
	for rows.Next() {
		p, err := scanWGPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetWGPeer(ctx context.Context, userID uint32, id int64) (WGPeer, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, user_id, name, public_key, private_key, address, created_at FROM wg_peers WHERE id = ? AND user_id = ?`, id, userID)
	p, err := scanWGPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WGPeer{}, ErrNotFound
	}
	return p, err
}

func (s *Store) UsedWGAddresses(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT address FROM wg_peers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) InsertWGPeer(ctx context.Context, userID uint32, name, pub, priv, address string, max int) (WGPeer, error) {
	if max < 1 {
		max = 5
	}
	n, err := s.CountWGPeers(ctx, userID)
	if err != nil {
		return WGPeer{}, err
	}
	if n >= max {
		return WGPeer{}, ErrTooManyWG
	}
	name = SanitizeWGName(name)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `INSERT INTO wg_peers (user_id, name, public_key, private_key, address, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, name, pub, priv, address, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return WGPeer{}, errors.New("that VPN key or address is already registered")
		}
		return WGPeer{}, err
	}
	id, _ := res.LastInsertId()
	return WGPeer{ID: id, UserID: userID, Name: name, PublicKey: pub, PrivateKey: priv, Address: address, Created: now}, nil
}

func (s *Store) DeleteWGPeer(ctx context.Context, userID uint32, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM wg_peers WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type wgScanner interface {
	Scan(dest ...any) error
}

func scanWGPeer(row wgScanner) (WGPeer, error) {
	var p WGPeer
	var created string
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.PublicKey, &p.PrivateKey, &p.Address, &created)
	if err != nil {
		return WGPeer{}, err
	}
	if t, err := time.Parse(time.RFC3339, created); err == nil {
		p.Created = t.Format("2 Jan 2006")
	} else {
		p.Created = created
	}
	return p, nil
}
