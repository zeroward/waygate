package kb

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	EventTitleMax  = 80
	EventDetailMax = 280
	eventListCap   = 20
	eventHomeCap   = 8
)

type RealmEvent struct {
	ID        int64
	Date      string
	Title     string
	Detail    string
	CreatedBy string
	CreatedAt time.Time
}

func (e RealmEvent) DisplayDate() string {
	t, err := time.Parse("2006-01-02", e.Date)
	if err != nil {
		return e.Date
	}
	return t.Format("Jan 2")
}

func (s *Store) migrateEvents() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS realm_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_date TEXT NOT NULL,
  title TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_realm_events_date ON realm_events(event_date, id);
`)
	return err
}

func ClipEventTitle(s string) string {
	return clipRunes(s, EventTitleMax)
}

func ClipEventDetail(s string) string {
	return clipRunes(s, EventDetailMax)
}

func clipRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:max]))
}

func ValidEventDate(s string) bool {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

func (s *Store) CreateEvent(ctx context.Context, date, title, detail, by string) (RealmEvent, error) {
	title = ClipEventTitle(title)
	detail = ClipEventDetail(detail)
	date = strings.TrimSpace(date)
	if !ValidEventDate(date) || title == "" {
		return RealmEvent{}, fmt.Errorf("%w: event", ErrInvalid)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `INSERT INTO realm_events (event_date, title, detail, created_by, created_at) VALUES (?, ?, ?, ?, ?)`,
		date, title, detail, strings.TrimSpace(by), now)
	if err != nil {
		return RealmEvent{}, err
	}
	id, _ := res.LastInsertId()
	return RealmEvent{ID: id, Date: date, Title: title, Detail: detail, CreatedBy: by, CreatedAt: time.Now().UTC()}, nil
}

func (s *Store) DeleteEvent(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM realm_events WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListUpcomingEvents(ctx context.Context, today string, limit int) ([]RealmEvent, error) {
	if !ValidEventDate(today) {
		today = time.Now().UTC().Format("2006-01-02")
	}
	if limit < 1 || limit > eventHomeCap {
		limit = eventHomeCap
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_date, title, detail, created_by, created_at FROM realm_events WHERE event_date >= ? ORDER BY event_date ASC, id ASC LIMIT ?`, today, limit)
	if err != nil {
		return nil, err
	}
	return scanRealmEvents(rows)
}

func (s *Store) ListStaffEvents(ctx context.Context, limit int) ([]RealmEvent, error) {
	if limit < 1 || limit > 50 {
		limit = eventListCap
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_date, title, detail, created_by, created_at FROM realm_events ORDER BY event_date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanRealmEvents(rows)
}

func scanRealmEvents(rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}) ([]RealmEvent, error) {
	defer rows.Close()
	var out []RealmEvent
	for rows.Next() {
		var e RealmEvent
		var created string
		if err := rows.Scan(&e.ID, &e.Date, &e.Title, &e.Detail, &e.CreatedBy, &created); err != nil {
			return out, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, e)
	}
	return out, rows.Err()
}
