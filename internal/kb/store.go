package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("article not found")
	ErrSlugTaken = errors.New("slug is already used")
	ErrInvalid   = errors.New("invalid article")
)

type Article struct {
	ID           int64
	Slug         string
	Title        string
	BodyMarkdown string
	Summary      string
	Category     string
	SortOrder    int
	Published    bool
	CreatedBy    string
	UpdatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn, err := dsnFor(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("kb sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("kb ping: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func dsnFor(path string) (string, error) {
	if path == "" || path == ":memory:" {
		return fmt.Sprintf("file:kbmem-%d?mode=memory&cache=shared", time.Now().UnixNano()), nil
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("kb data dir: %w", err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		return err
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS articles (
  textid INTEGER PRIMARY KEY AUTOINCREMENT,
  slug TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  body_markdown TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT 'General',
  sort_order INTEGER NOT NULL DEFAULT 0,
  published INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_articles_cat ON articles(category, sort_order, title);
CREATE INDEX IF NOT EXISTS idx_articles_pub ON articles(published);
CREATE TABLE IF NOT EXISTS staff_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_staff_events_at ON staff_events(at DESC);
`)
	if err != nil {
		return err
	}
	return s.migrateTickets()
}

const articleCols = `textid, slug, title, body_markdown, summary, category, sort_order, published, created_by, updated_by, created_at, updated_at`

func scanArticle(row interface{ Scan(dest ...any) error }) (Article, error) {
	var a Article
	var pub int
	var created, updated string
	err := row.Scan(
		&a.ID, &a.Slug, &a.Title, &a.BodyMarkdown, &a.Summary, &a.Category, &a.SortOrder,
		&pub, &a.CreatedBy, &a.UpdatedBy, &created, &updated,
	)
	if err != nil {
		return Article{}, err
	}
	a.Published = pub != 0
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return a, nil
}

func (s *Store) GetByID(ctx context.Context, id int64) (Article, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+articleCols+` FROM articles WHERE textid = ?`, id)
	a, err := scanArticle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Article{}, ErrNotFound
	}
	return a, err
}

func (s *Store) GetBySlug(ctx context.Context, slug string) (Article, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+articleCols+` FROM articles WHERE slug = ?`, slug)
	a, err := scanArticle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Article{}, ErrNotFound
	}
	return a, err
}

func (s *Store) ListPublished(ctx context.Context, q string) ([]Article, error) {
	q = strings.TrimSpace(q)
	var (
		rows *sql.Rows
		err  error
	)
	if q == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT `+articleCols+` FROM articles WHERE published = 1 ORDER BY category ASC, sort_order ASC, title ASC`)
	} else {
		like := likeContains(q)
		rows, err = s.db.QueryContext(ctx, `SELECT `+articleCols+` FROM articles WHERE published = 1
AND (title LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\' OR category LIKE ? ESCAPE '\' OR body_markdown LIKE ? ESCAPE '\')
ORDER BY category ASC, sort_order ASC, title ASC`, like, like, like, like)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

func (s *Store) LatestPublished(ctx context.Context) (*Article, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+articleCols+` FROM articles WHERE published = 1 ORDER BY updated_at DESC, title ASC LIMIT 1`)
	a, err := scanArticle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListAll(ctx context.Context) ([]Article, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+articleCols+` FROM articles ORDER BY updated_at DESC, title ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

func (s *Store) CategoryPublished(ctx context.Context, category string) ([]Article, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+articleCols+` FROM articles WHERE published = 1 AND category = ? ORDER BY sort_order ASC, title ASC`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

func (s *Store) Categories(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT category FROM articles ORDER BY category ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, a Article) (Article, error) {
	if err := normalize(&a); err != nil {
		return Article{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.CreatedAt, _ = time.Parse(time.RFC3339, now)
	a.UpdatedAt = a.CreatedAt
	res, err := s.db.ExecContext(ctx, `INSERT INTO articles
(slug, title, body_markdown, summary, category, sort_order, published, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Slug, a.Title, a.BodyMarkdown, a.Summary, a.Category, a.SortOrder, boolInt(a.Published),
		a.CreatedBy, a.UpdatedBy, now, now)
	if err != nil {
		if isUnique(err) {
			return Article{}, ErrSlugTaken
		}
		return Article{}, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

func (s *Store) Update(ctx context.Context, a Article) (Article, error) {
	if a.ID < 1 {
		return Article{}, ErrNotFound
	}
	if err := normalize(&a); err != nil {
		return Article{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	res, err := s.db.ExecContext(ctx, `UPDATE articles SET
slug=?, title=?, body_markdown=?, summary=?, category=?, sort_order=?, published=?, updated_by=?, updated_at=?
WHERE textid=?`,
		a.Slug, a.Title, a.BodyMarkdown, a.Summary, a.Category, a.SortOrder, boolInt(a.Published),
		a.UpdatedBy, now, a.ID)
	if err != nil {
		if isUnique(err) {
			return Article{}, ErrSlugTaken
		}
		return Article{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Article{}, ErrNotFound
	}
	return s.GetByID(ctx, a.ID)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM articles WHERE textid=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SeedIfMissing(ctx context.Context, a Article) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE slug = ?`, a.Slug).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.Create(ctx, a)
	if errors.Is(err, ErrSlugTaken) {
		return nil
	}
	return err
}

func scanAll(rows *sql.Rows) ([]Article, error) {
	var out []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func normalize(a *Article) error {
	a.Title = strings.TrimSpace(a.Title)
	a.Slug = strings.ToLower(strings.TrimSpace(a.Slug))
	a.Category = strings.TrimSpace(a.Category)
	a.Summary = strings.TrimSpace(a.Summary)
	a.CreatedBy = strings.TrimSpace(a.CreatedBy)
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	if a.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if len(a.Title) > 120 {
		return fmt.Errorf("%w: title is too long", ErrInvalid)
	}
	if a.Slug == "" {
		a.Slug = Slugify(a.Title)
	}
	if !ValidSlug(a.Slug) {
		return fmt.Errorf("%w: slug must be lowercase letters, numbers, and hyphens", ErrInvalid)
	}
	if a.Category == "" {
		a.Category = "General"
	}
	if len(a.Category) > 64 {
		return fmt.Errorf("%w: category is too long", ErrInvalid)
	}
	if len(a.Summary) > 240 {
		a.Summary = strings.TrimSpace(a.Summary[:240])
	}
	if len(a.BodyMarkdown) > 100_000 {
		return fmt.Errorf("%w: body is too long", ErrInvalid)
	}
	if a.Summary == "" {
		a.Summary = Summarize(a.BodyMarkdown)
	}
	return nil
}

func Summarize(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "|") {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "`", "")
		if line == "" {
			continue
		}
		if len(line) > 180 {
			line = strings.TrimSpace(line[:180]) + "…"
		}
		return line
	}
	return ""
}

func likeContains(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return "%" + q + "%"
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "constraint")
}
