package kb

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SettingMaintenanceMessage = "maintenance_message"
	SettingMaintenanceUntil   = "maintenance_until"
	BannerMaxRunes            = 280
)

func (s *Store) migrateSettings() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);`)
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, now)
	return err
}

func ClipBanner(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	if utf8.RuneCountInString(s) <= BannerMaxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:BannerMaxRunes]))
}

// ActiveBanner returns the message when set and not expired.
func (s *Store) ActiveBanner(ctx context.Context) string {
	msg, err := s.GetSetting(ctx, SettingMaintenanceMessage)
	if err != nil || strings.TrimSpace(msg) == "" {
		return ""
	}
	until, _ := s.GetSetting(ctx, SettingMaintenanceUntil)
	until = strings.TrimSpace(until)
	if until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return ""
		}
		if !t.After(time.Now().UTC()) {
			return ""
		}
	}
	return msg
}
