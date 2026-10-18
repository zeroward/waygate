package kb

import (
	"context"
	"strings"
	"time"
)

const eventKeep = 200

type Event struct {
	At     time.Time
	Actor  string
	Action string
	Target string
}

func (s *Store) LogEvent(ctx context.Context, actor, action, target string) error {
	if s == nil || s.db == nil {
		return nil
	}
	actor = strings.TrimSpace(actor)
	action = strings.TrimSpace(action)
	target = strings.TrimSpace(target)
	if actor == "" || action == "" {
		return nil
	}
	if len(target) > 120 {
		target = target[:120]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO staff_events (at, actor, action, target) VALUES (?, ?, ?, ?)`, now, actor, action, target); err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM staff_events WHERE at < ?`, cutoff)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM staff_events WHERE id NOT IN (SELECT id FROM staff_events ORDER BY at DESC, id DESC LIMIT ?)`, eventKeep)
	return nil
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit < 1 || limit > eventKeep {
		limit = eventKeep
	}
	rows, err := s.db.QueryContext(ctx, `SELECT at, actor, action, target FROM staff_events ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var at, actor, action, target string
		if err := rows.Scan(&at, &actor, &action, &target); err != nil {
			return nil, err
		}
		e := Event{Actor: actor, Action: action, Target: target}
		e.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, e)
	}
	return out, rows.Err()
}
