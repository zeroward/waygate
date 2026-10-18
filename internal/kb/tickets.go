package kb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTicketNotFound = errors.New("ticket not found")
	ErrTicketClosed   = errors.New("ticket is closed")
	ErrBadCategory    = errors.New("invalid category")
	ErrBadStatus      = errors.New("invalid status")
)

const (
	TicketOpen       = "open"
	TicketInProgress = "in-progress"
	TicketDone       = "done"
	TicketClosed     = "closed"
)

var TicketCategories = []string{
	"Name change",
	"Character transfer/restore",
	"Guild",
	"Items",
	"Other",
}

type Ticket struct {
	ID            int64
	PublicRef     string
	AccountID     uint32
	Username      string
	Category      string
	Title         string
	CharacterGUID uint32
	CharacterName string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ClosedBy      string
	Messages      []TicketMessage
}

type TicketMessage struct {
	ID        int64
	TicketID  int64
	Author    string
	FromStaff bool
	Body      string
	CreatedAt time.Time
}

func ValidTicketCategory(c string) bool {
	for _, x := range TicketCategories {
		if x == c {
			return true
		}
	}
	return false
}

func ValidTicketStatus(s string) bool {
	switch s {
	case TicketOpen, TicketInProgress, TicketDone, TicketClosed:
		return true
	}
	return false
}

func (t Ticket) OpenForComment() bool {
	return t.Status == TicketOpen || t.Status == TicketInProgress
}

type TicketStatusChoice struct {
	Value string
	Label string
}

func TicketStatusChoices() []TicketStatusChoice {
	return []TicketStatusChoice{
		{TicketOpen, "Open"},
		{TicketInProgress, "In progress"},
		{TicketDone, "Done"},
		{TicketClosed, "Closed"},
	}
}

func (t Ticket) StatusLabel() string {
	for _, c := range TicketStatusChoices() {
		if c.Value == t.Status {
			return c.Label
		}
	}
	return t.Status
}

func (s *Store) migrateTickets() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  public_ref TEXT NOT NULL UNIQUE,
  account_id INTEGER NOT NULL,
  username TEXT NOT NULL,
  category TEXT NOT NULL,
  title TEXT NOT NULL,
  character_guid INTEGER NOT NULL DEFAULT 0,
  character_name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  closed_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tickets_account ON tickets(account_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status, updated_at DESC);
CREATE TABLE IF NOT EXISTS ticket_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL,
  author_username TEXT NOT NULL,
  from_staff INTEGER NOT NULL DEFAULT 0,
  body_text TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ticket_messages_tid ON ticket_messages(ticket_id, id);
`)
	return err
}

func newTicketRef() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("T-%d", time.Now().UnixNano()%1_000_000)
	}
	return "T-" + strings.ToUpper(hex.EncodeToString(b))
}

func (s *Store) CreateTicket(ctx context.Context, t Ticket, body string) (Ticket, error) {
	t.Title = strings.TrimSpace(t.Title)
	t.Username = strings.TrimSpace(t.Username)
	t.CharacterName = strings.TrimSpace(t.CharacterName)
	body = strings.TrimSpace(body)
	if t.Title == "" || len(t.Title) > 120 {
		return Ticket{}, fmt.Errorf("%w: title", ErrInvalid)
	}
	if !ValidTicketCategory(t.Category) {
		return Ticket{}, ErrBadCategory
	}
	if body == "" || len(body) > 4000 {
		return Ticket{}, fmt.Errorf("%w: message", ErrInvalid)
	}
	if t.AccountID == 0 || t.Username == "" {
		return Ticket{}, fmt.Errorf("%w: account", ErrInvalid)
	}
	t.Status = TicketOpen
	t.PublicRef = newTicketRef()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `INSERT INTO tickets
(public_ref, account_id, username, category, title, character_guid, character_name, status, created_at, updated_at, closed_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		t.PublicRef, t.AccountID, t.Username, t.Category, t.Title, t.CharacterGUID, t.CharacterName, t.Status, now, now)
	if err != nil {
		return Ticket{}, err
	}
	t.ID, _ = res.LastInsertId()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO ticket_messages (ticket_id, author_username, from_staff, body_text, created_at) VALUES (?, ?, 0, ?, ?)`,
		t.ID, t.Username, body, now); err != nil {
		return Ticket{}, err
	}
	return s.GetTicket(ctx, t.ID)
}

func (s *Store) GetTicket(ctx context.Context, id int64) (Ticket, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, public_ref, account_id, username, category, title, character_guid, character_name, status, created_at, updated_at, closed_by FROM tickets WHERE id = ?`, id)
	t, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrTicketNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	msgs, err := s.ticketMessages(ctx, t.ID)
	if err != nil {
		return Ticket{}, err
	}
	t.Messages = msgs
	return t, nil
}

func (s *Store) ListTicketsForAccount(ctx context.Context, accountID uint32) ([]Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, public_ref, account_id, username, category, title, character_guid, character_name, status, created_at, updated_at, closed_by FROM tickets WHERE account_id = ? ORDER BY updated_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTickets(rows)
}

func (s *Store) ListOpenTickets(ctx context.Context) ([]Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, public_ref, account_id, username, category, title, character_guid, character_name, status, created_at, updated_at, closed_by FROM tickets WHERE status IN (?, ?) ORDER BY updated_at DESC`, TicketOpen, TicketInProgress)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTickets(rows)
}

func (s *Store) AddTicketMessage(ctx context.Context, id int64, author string, fromStaff bool, body string) error {
	body = strings.TrimSpace(body)
	author = strings.TrimSpace(author)
	if body == "" || len(body) > 4000 || author == "" {
		return fmt.Errorf("%w: message", ErrInvalid)
	}
	t, err := s.GetTicket(ctx, id)
	if err != nil {
		return err
	}
	if !fromStaff && !t.OpenForComment() {
		return ErrTicketClosed
	}
	now := time.Now().UTC().Format(time.RFC3339)
	fs := 0
	if fromStaff {
		fs = 1
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO ticket_messages (ticket_id, author_username, from_staff, body_text, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, author, fs, body, now); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tickets SET updated_at = ? WHERE id = ?`, now, id)
	return err
}

func (s *Store) SetTicketStatus(ctx context.Context, id int64, status, actor string) error {
	if !ValidTicketStatus(status) {
		return ErrBadStatus
	}
	t, err := s.GetTicket(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	closedBy := t.ClosedBy
	if status == TicketDone || status == TicketClosed {
		closedBy = actor
	} else {
		closedBy = ""
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tickets SET status = ?, updated_at = ?, closed_by = ? WHERE id = ?`, status, now, closedBy, id)
	return err
}

func scanTicket(row interface{ Scan(dest ...any) error }) (Ticket, error) {
	var t Ticket
	var created, updated string
	var acc int64
	err := row.Scan(&t.ID, &t.PublicRef, &acc, &t.Username, &t.Category, &t.Title, &t.CharacterGUID, &t.CharacterName, &t.Status, &created, &updated, &t.ClosedBy)
	if err != nil {
		return Ticket{}, err
	}
	t.AccountID = uint32(acc)
	t.CreatedAt, _ = time.Parse(time.RFC3339, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return t, nil
}

func scanTickets(rows *sql.Rows) ([]Ticket, error) {
	var out []Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ticketMessages(ctx context.Context, ticketID int64) ([]TicketMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ticket_id, author_username, from_staff, body_text, created_at FROM ticket_messages WHERE ticket_id = ? ORDER BY id ASC`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketMessage
	for rows.Next() {
		var m TicketMessage
		var fs int
		var created string
		if err := rows.Scan(&m.ID, &m.TicketID, &m.Author, &fs, &m.Body, &created); err != nil {
			return nil, err
		}
		m.FromStaff = fs != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, m)
	}
	return out, rows.Err()
}
