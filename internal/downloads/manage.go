package downloads

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._()\[\] +-]{0,180}$`)

type UploadInput struct {
	ID          string
	Title       string
	Category    string
	Description string
	Version     string
	Featured    bool
	FileName    string
	SourceURL   string
	AddonID     string
	Mandatory   bool
}

func AllowedExtHint() string {
	return ".zip, .7z, .rar, .mpq, .patch, .exe"
}

func (s *Store) Writable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writableLocked()
}

func (s *Store) writableLocked() bool {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return false
	}
	p := filepath.Join(s.root, ".waygate-writable")
	if err := os.WriteFile(p, []byte("ok\n"), 0o644); err != nil {
		return false
	}
	_ = os.Remove(p)
	return true
}

func (s *Store) Add(ctx context.Context, in UploadInput, r io.Reader) (Item, error) {
	name, err := SanitizeFileName(in.FileName)
	if err != nil {
		return Item{}, err
	}
	cat := normalizeCategory(in.Category)
	title := strings.TrimSpace(in.Title)
	ver := strings.TrimSpace(in.Version)
	desc := ""
	source := ""
	addonID := ""
	mandatory := false
	switch cat {
	case CatClient:
		if title == "" {
			return Item{}, ErrNeedName
		}
		if ver == "" {
			return Item{}, ErrNeedVersion
		}
	case CatMods:
		var err error
		source, err = SanitizeSourceURL(in.SourceURL)
		if err != nil {
			return Item{}, err
		}
		addonID = strings.ToLower(strings.TrimSpace(in.AddonID))
		if addonID != "" && !idRe.MatchString(addonID) {
			return Item{}, ErrBadAddonID
		}
		if title == "" {
			if addonID != "" {
				title = addonID
			} else {
				title = prettyName(name)
			}
		}
		desc = strings.TrimSpace(in.Description)
	default: // patches
		title = prettyName(name)
		ver = ""
		desc = strings.TrimSpace(in.Description)
		mandatory = in.Mandatory
	}
	if len(title) > 120 {
		title = strings.TrimSpace(title[:120])
	}
	if len(ver) > 40 {
		ver = ver[:40]
	}
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	wantID := strings.ToLower(strings.TrimSpace(in.ID))
	if wantID == "" && addonID != "" {
		wantID = addonID
	}
	if wantID != "" && !idRe.MatchString(wantID) {
		return Item{}, ErrInvalidID
	}
	uploaded := time.Now().UTC().Format("2006-01-02 15:04") + " UTC"
	if ctx == nil {
		ctx = context.Background()
	}

	incomingDir := filepath.Join(s.root, ".incoming")
	if err := os.MkdirAll(incomingDir, 0o755); err != nil {
		return Item{}, ErrNotWritable
	}
	tmp := filepath.Join(incomingDir, randomPartName())
	size, sum, err := writeIncomingFile(tmp, r)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = os.Remove(tmp) }()

	clam := ClamSkipped
	if s.scanner != nil {
		max := s.scanMax
		if max <= 0 {
			max = 100 << 20
		}
		if size > max {
			clam = ClamTooBig
		} else {
			f, err := os.Open(tmp)
			if err != nil {
				return Item{}, err
			}
			scanErr := s.scanner.Scan(ctx, f)
			_ = f.Close()
			if scanErr != nil {
				return Item{}, scanErr
			}
			clam = ClamClean
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.writableLocked() {
		return Item{}, ErrNotWritable
	}

	cf := s.readCatalogFile()
	rel := cat + "/" + name
	idx := findCatalogItem(cf, wantID, rel)
	replace := idx >= 0
	if replace {
		existing, err := SafeRelPath(cf.Items[idx].File)
		if err == nil {
			rel = existing
		}
		if wantID == "" {
			wantID = strings.ToLower(cf.Items[idx].ID)
		}
	} else {
		rel = s.uniqueRelLocked(cat, name)
		if wantID == "" {
			wantID = slug(cat + "-" + strings.TrimSuffix(name, filepath.Ext(name)))
		}
		wantID = uniqueCatalogID(cf, wantID)
	}

	abs, err := ResolveUnder(s.root, rel)
	if err != nil {
		return Item{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Item{}, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return Item{}, err
	}

	entry := CatalogEntry{
		ID:          wantID,
		Title:       title,
		Category:    cat,
		File:        rel,
		Version:     ver,
		Description: desc,
		SHA256:      sum,
		SourceURL:   source,
		AddonID:     addonID,
		UploadedAt:  uploaded,
		ClamAV:      clam,
		Mandatory:   mandatory,
	}
	if replace {
		old := cf.Items[idx]
		if cat != CatClient && strings.TrimSpace(in.Title) == "" && old.Title != "" {
			entry.Title = old.Title
		}
		if cat != CatClient && desc == "" {
			entry.Description = old.Description
		}
		if cat == CatMods && ver == "" {
			entry.Version = old.Version
		}
		if cat == CatMods && source == "" {
			entry.SourceURL = old.SourceURL
		}
		if cat == CatMods && addonID == "" {
			entry.AddonID = old.AddonID
		}
		entry.ID = strings.ToLower(old.ID)
		entry.Featured = old.Featured
		cf.Items[idx] = entry
	} else {
		cf.Items = append(cf.Items, entry)
	}
	if err := s.writeCatalogFile(cf); err != nil {
		return Item{}, err
	}

	s.expires = time.Time{}
	it, ok := s.itemFromEntry(entry)
	if !ok {
		it = Item{
			ID: entry.ID, Title: entry.Title, Category: cat, Label: categoryLabel(cat),
			Description: entry.Description, Version: entry.Version, FileName: filepath.Base(rel),
			RelPath: rel, AbsPath: abs, SHA256: sum,
			SourceURL: entry.SourceURL, AddonID: entry.AddonID, Mandatory: entry.Mandatory,
			UploadedAt: uploaded, ClamAV: clam,
		}
	}
	it.Ready = true
	it.Size = size
	it.SizeHuman = HumanSize(size)
	it.SHA256 = sum
	it.ClamAV = clam
	it.ClamAVLabel, it.ClamClean = clamLabel(clam)
	it.UploadedAt = uploaded
	return it, nil
}

func (s *Store) Remove(id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if !idRe.MatchString(id) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.writableLocked() {
		return ErrNotWritable
	}

	it, inCache := s.byID[id]
	cf := s.readCatalogFile()
	filtered := cf.Items[:0]
	inCatalog := false
	abs := ""
	if inCache {
		abs = it.AbsPath
	}
	for _, e := range cf.Items {
		if strings.ToLower(strings.TrimSpace(e.ID)) == id {
			inCatalog = true
			if rel, err := SafeRelPath(e.File); err == nil {
				if p, err := ResolveUnder(s.root, rel); err == nil {
					abs = p
				}
			}
			continue
		}
		filtered = append(filtered, e)
	}
	if !inCatalog && !inCache {
		return ErrNotFound
	}
	cf.Items = filtered
	if inCatalog {
		if err := s.writeCatalogFile(cf); err != nil {
			return err
		}
	}
	if abs != "" {
		_ = os.Remove(abs + ".sha256")
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			s.expires = time.Time{}
			return err
		}
	}
	s.expires = time.Time{}
	return nil
}

func (s *Store) readCatalogFile() CatalogFile {
	cf := CatalogFile{Intro: defaultIntro}
	raw, err := os.ReadFile(s.catalog)
	if err != nil {
		return cf
	}
	if json.Unmarshal(raw, &cf) != nil {
		return CatalogFile{Intro: defaultIntro}
	}
	if strings.TrimSpace(cf.Intro) == "" {
		cf.Intro = defaultIntro
	}
	if cf.Items == nil {
		cf.Items = []CatalogEntry{}
	}
	return cf
}

func (s *Store) writeCatalogFile(cf CatalogFile) error {
	if err := os.MkdirAll(filepath.Dir(s.catalog), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := s.catalog + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.catalog); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeIncomingFile(tmp string, r io.Reader) (int64, string, error) {
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, "", closeErr
	}
	if n == 0 {
		_ = os.Remove(tmp)
		return 0, "", ErrEmptyFile
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func randomPartName() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d.part", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:]) + ".part"
}

func findCatalogItem(cf CatalogFile, id, rel string) int {
	if id != "" {
		for i, e := range cf.Items {
			if strings.ToLower(strings.TrimSpace(e.ID)) == id {
				return i
			}
		}
	}
	rel = filepath.ToSlash(rel)
	for i, e := range cf.Items {
		p, err := SafeRelPath(e.File)
		if err == nil && p == rel {
			return i
		}
	}
	return -1
}

func uniqueCatalogID(cf CatalogFile, id string) string {
	if id == "" || !idRe.MatchString(id) {
		id = "file"
	}
	used := map[string]bool{}
	for _, e := range cf.Items {
		used[strings.ToLower(strings.TrimSpace(e.ID))] = true
	}
	if !used[id] {
		return id
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", id, i)
		if len(cand) > 64 {
			cand = fmt.Sprintf("file-%d", i)
		}
		if !used[cand] && idRe.MatchString(cand) {
			return cand
		}
	}
	return "file"
}

func (s *Store) uniqueRelLocked(cat, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	cand := name
	for i := 2; i < 1000; i++ {
		rel := cat + "/" + cand
		abs := filepath.Join(s.root, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			return rel
		}
		cand = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	return cat + "/" + cand
}

func SanitizeFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return "", ErrInvalidName
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedExt[ext] {
		return "", ErrBadFile
	}
	if strings.Count(name, ".") > 4 {
		return "", ErrInvalidName
	}
	if !fileNameRe.MatchString(name) {
		return "", ErrInvalidName
	}
	return name, nil
}

func SanitizeSourceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 500 {
		return "", ErrBadSource
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", ErrBadSource
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", ErrBadSource
	}
	return raw, nil
}
