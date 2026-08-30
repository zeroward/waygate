// Package downloads catalogs client zips, patches, and mods from a directory
// plus an optional catalog.json. Only files listed or discovered under the
// downloads root are served — no directory listing, no path traversal.
package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	CatClient    = "client"
	CatPatches   = "patches"
	CatMods      = "mods"
	defaultIntro = "A clean Wrath of the Lich King 3.3.5a client (build 12340), plus patches and addons published by the realm's admins."
)

var (
	ErrNotFound    = errors.New("download not found")
	ErrNotWritable = errors.New("downloads folder is not writable")
	ErrBadFile     = errors.New("that file type is not allowed")
	ErrEmptyFile   = errors.New("file is empty")
	ErrInvalidName = errors.New("invalid file name")
	ErrInvalidID   = errors.New("invalid download id")
	ErrTooLarge    = errors.New("file is too large")
	ErrNeedName    = errors.New("enter the client name")
	ErrNeedVersion = errors.New("enter the client version")
	ErrBadSource   = errors.New("source link must be an http(s) URL")
	ErrBadAddonID  = errors.New("addon id can only use letters, digits, and hyphens")
)

const (
	ClamClean   = "clean"
	ClamSkipped = "not scanned"
	ClamTooBig  = "skipped"
)

type Scanner interface {
	Scan(ctx context.Context, r io.Reader) error
}

var (
	idRe        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	allowedExt  = map[string]bool{".zip": true, ".7z": true, ".rar": true, ".mpq": true, ".patch": true}
	scanFolders = []string{CatClient, CatPatches, CatMods}
	slugClean   = regexp.MustCompile(`[^a-z0-9]+`)
)

type CatalogFile struct {
	Intro string         `json:"intro"`
	Items []CatalogEntry `json:"items"`
}

type CatalogEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	File        string `json:"file"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Featured    bool   `json:"featured,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	AddonID     string `json:"addon_id,omitempty"`
	UploadedAt  string `json:"uploaded_at,omitempty"`
	ClamAV      string `json:"clamav,omitempty"`
	Mandatory   bool   `json:"mandatory,omitempty"`
}

type Item struct {
	ID          string
	Title       string
	Category    string
	Label       string
	Description string
	Version     string
	FileName    string
	RelPath     string
	AbsPath     string
	Size        int64
	SizeHuman   string
	SHA256      string
	Ready       bool
	Featured    bool
	SourceURL   string
	AddonID     string
	UploadedAt  string
	ClamAV      string
	ClamClean   bool
	ClamAVLabel string
	Mandatory   bool
}

type Store struct {
	root    string
	catalog string
	ttl     time.Duration
	scanner Scanner
	scanMax int64

	mu      sync.Mutex
	items   []Item
	byID    map[string]Item
	intro   string
	expires time.Time
}

func New(root, catalogPath string) *Store {
	if root == "" {
		root = "downloads"
	}
	if catalogPath == "" {
		catalogPath = filepath.Join(root, "catalog.json")
	}
	return &Store{root: root, catalog: catalogPath, ttl: 5 * time.Second}
}

func (s *Store) SetScanner(sc Scanner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanner = sc
}

func (s *Store) Scanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanner != nil
}

func (s *Store) SetScanMax(n int64) {
	s.scanMax = n
}

func (s *Store) Intro() string {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intro
}

func (s *Store) Search(category, q string) []Item {
	items := s.List(category)
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return items
	}
	if len(q) > 64 {
		q = q[:64]
	}
	var out []Item
	for _, it := range items {
		if itemMatches(it, q) {
			out = append(out, it)
		}
	}
	return out
}

func itemMatches(it Item, q string) bool {
	fields := []string{
		it.Title, it.FileName, it.Description, it.Version,
		it.AddonID, it.SourceURL, it.Label, it.ID,
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (s *Store) List(category string) []Item {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	if category == "" || category == "all" {
		out := make([]Item, len(s.items))
		copy(out, s.items)
		return out
	}
	var out []Item
	for _, it := range s.items {
		if it.Category == category {
			out = append(out, it)
		}
	}
	return out
}

func (s *Store) Counts() map[string]int {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := map[string]int{"all": len(s.items), CatClient: 0, CatPatches: 0, CatMods: 0}
	for _, it := range s.items {
		c[it.Category]++
	}
	return c
}

func (s *Store) Get(id string) (Item, bool) {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.byID[id]
	return it, ok
}

func (s *Store) refresh() {
	s.mu.Lock()
	if time.Now().Before(s.expires) && s.byID != nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	intro, items := s.load()

	s.mu.Lock()
	s.intro = intro
	s.items = items
	s.byID = make(map[string]Item, len(items))
	for _, it := range items {
		s.byID[it.ID] = it
	}
	s.expires = time.Now().Add(s.ttl)
	s.mu.Unlock()
}

func (s *Store) load() (string, []Item) {
	intro := defaultIntro
	var cataloged []CatalogEntry
	if raw, err := os.ReadFile(s.catalog); err == nil {
		var cf CatalogFile
		if json.Unmarshal(raw, &cf) == nil {
			if strings.TrimSpace(cf.Intro) != "" {
				intro = cf.Intro
			}
			cataloged = cf.Items
		}
	}

	seenFile := map[string]bool{}
	usedID := map[string]bool{}
	var out []Item

	for _, e := range cataloged {
		it, ok := s.itemFromEntry(e)
		if !ok {
			continue
		}
		out = append(out, it)
		usedID[it.ID] = true
		if it.RelPath != "" {
			seenFile[filepath.ToSlash(it.RelPath)] = true
		}
	}

	for _, folder := range scanFolders {
		entries, err := os.ReadDir(filepath.Join(s.root, folder))
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
				continue
			}
			ext := strings.ToLower(filepath.Ext(de.Name()))
			if !allowedExt[ext] {
				continue
			}
			rel := folder + "/" + de.Name()
			if seenFile[rel] {
				continue
			}
			id := slug(folder + "-" + strings.TrimSuffix(de.Name(), ext))
			for usedID[id] {
				id += "-x"
			}
			usedID[id] = true
			seenFile[rel] = true
			abs := filepath.Join(s.root, folder, de.Name())
			out = append(out, s.statItem(Item{
				ID:       id,
				Title:    prettyName(de.Name()),
				Category: folder,
				Label:    categoryLabel(folder),
				FileName: de.Name(),
				RelPath:  rel,
				AbsPath:  abs,
			}))
		}
	}
	sortDownloads(out)
	return intro, out
}

// Display order: client, mandatory patches, optional patches, featured addons, other addons.
func sortDownloads(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := downloadRank(items[i]), downloadRank(items[j])
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
}

func downloadRank(it Item) int {
	switch it.Category {
	case CatClient:
		return 0
	case CatPatches:
		if it.Mandatory {
			return 1
		}
		return 2
	case CatMods:
		if it.Featured {
			return 3
		}
		return 4
	default:
		return 5
	}
}

func (s *Store) itemFromEntry(e CatalogEntry) (Item, bool) {
	id := strings.ToLower(strings.TrimSpace(e.ID))
	if !idRe.MatchString(id) {
		return Item{}, false
	}
	cat := normalizeCategory(e.Category)
	rel, err := SafeRelPath(e.File)
	if err != nil {
		return Item{}, false
	}
	abs, err := ResolveUnder(s.root, rel)
	if err != nil {
		return Item{}, false
	}
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = prettyName(filepath.Base(rel))
	}
	it := Item{
		ID:          id,
		Title:       title,
		Category:    cat,
		Label:       categoryLabel(cat),
		Description: strings.TrimSpace(e.Description),
		Version:     strings.TrimSpace(e.Version),
		FileName:    filepath.Base(rel),
		RelPath:     rel,
		AbsPath:     abs,
		SHA256:      strings.ToLower(strings.TrimSpace(e.SHA256)),
		Featured:    e.Featured,
		SourceURL:   strings.TrimSpace(e.SourceURL),
		AddonID:     strings.TrimSpace(e.AddonID),
		UploadedAt:  strings.TrimSpace(e.UploadedAt),
		ClamAV:      strings.TrimSpace(e.ClamAV),
		Mandatory:   e.Mandatory,
	}
	return s.statItem(it), true
}

func (s *Store) statItem(it Item) Item {
	fi, err := os.Stat(it.AbsPath)
	if err == nil && fi.Mode().IsRegular() {
		it.Ready = true
		it.Size = fi.Size()
		it.SizeHuman = HumanSize(fi.Size())
		if it.UploadedAt == "" {
			it.UploadedAt = fi.ModTime().UTC().Format("2006-01-02 15:04") + " UTC"
		}
	}
	if it.SHA256 == "" {
		it.SHA256 = readSidecarHash(it.AbsPath)
	}
	it.ClamAVLabel, it.ClamClean = clamLabel(it.ClamAV)
	return it
}

func clamLabel(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ClamClean, "ok", "scanned":
		return "clean", true
	case ClamTooBig:
		return "skipped (over 100 MB)", false
	case "":
		return ClamSkipped, false
	default:
		return raw, false
	}
}

func readSidecarHash(abs string) string {
	b, err := os.ReadFile(abs + ".sha256")
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	if i := strings.IndexAny(line, " \t"); i > 0 {
		line = line[:i]
	}
	line = strings.ToLower(line)
	if len(line) != 64 {
		return ""
	}
	for _, r := range line {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return line
}

func SafeRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(filepath.ToSlash(p), "/") {
		return "", fmt.Errorf("empty or absolute path")
	}
	p = filepath.ToSlash(p)
	if strings.Contains(p, "\\") {
		return "", fmt.Errorf("empty path")
	}
	clean := pathCleanSlash(p)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("path escapes downloads root")
	}
	return clean, nil
}

func ResolveUnder(root, rel string) (string, error) {
	rel, err := SafeRelPath(rel)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(rootAbs, filepath.FromSlash(rel))
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	relToRoot, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil || strings.HasPrefix(relToRoot, "..") {
		return "", fmt.Errorf("path escapes downloads root")
	}
	return fullAbs, nil
}

func pathCleanSlash(p string) string {
	parts := strings.Split(p, "/")
	var stack []string
	for _, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if len(stack) == 0 {
				return "../"
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return "."
	}
	return strings.Join(stack, "/")
}

func NormalizeCategory(c string) string { return normalizeCategory(c) }

func normalizeCategory(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "client", "clients":
		return CatClient
	case "patch", "patches":
		return CatPatches
	case "mod", "mods", "addon", "addons":
		return CatMods
	default:
		return CatPatches
	}
}

func categoryLabel(c string) string {
	switch c {
	case CatClient:
		return "Client"
	case CatPatches:
		return "Patch"
	case CatMods:
		return "Addon"
	default:
		return c
	}
}

func slug(s string) string {
	s = strings.ToLower(s)
	s = slugClean.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "file"
	}
	if len(s) > 64 {
		s = s[:64]
		s = strings.Trim(s, "-")
	}
	if !idRe.MatchString(s) {
		s = "file"
	}
	return s
}

func prettyName(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	return strings.TrimSpace(base)
}

func HumanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	val := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	u := ""
	for _, unit := range units {
		val /= 1024
		u = unit
		if val < 1024 {
			break
		}
	}
	if val >= 10 {
		return fmt.Sprintf("%.0f %s", val, u)
	}
	return fmt.Sprintf("%.1f %s", val, u)
}
