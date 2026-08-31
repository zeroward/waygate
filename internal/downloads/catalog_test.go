package downloads

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadOrder(t *testing.T) {
	root := t.TempDir()
	mustMk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMk("catalog.json", `{
	  "items": [
	    {"id":"opt-patch","title":"Optional HD","category":"patches","file":"patches/opt.zip"},
	    {"id":"plain-mod","title":"Bagnon","category":"mods","file":"mods/bagnon.zip"},
	    {"id":"feat-mod","title":"Questie","category":"mods","file":"mods/questie.zip","featured":true},
	    {"id":"must-patch","title":"Core patches","category":"patches","file":"patches/must.zip","mandatory":true},
	    {"id":"the-client","title":"WotLK Client","category":"client","file":"client/wow.zip"}
	  ]
	}`)
	for _, f := range []string{"patches/opt.zip", "mods/bagnon.zip", "mods/questie.zip", "patches/must.zip", "client/wow.zip"} {
		mustMk(f, "PK")
	}
	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0
	all := s.List("all")
	if len(all) != 5 {
		t.Fatalf("len %d", len(all))
	}
	want := []string{"the-client", "must-patch", "opt-patch", "feat-mod", "plain-mod"}
	for i, id := range want {
		if all[i].ID != id {
			t.Fatalf("pos %d got %s want %s", i, all[i].ID, id)
		}
	}
	patches := s.List(CatPatches)
	if len(patches) != 2 || !patches[0].Mandatory || patches[1].Mandatory {
		t.Fatalf("patches order: %+v %+v", patches[0], patches[1])
	}
	mods := s.List(CatMods)
	if len(mods) != 2 || !mods[0].Featured || mods[1].Featured {
		t.Fatalf("mods order: %+v %+v", mods[0], mods[1])
	}
}

func TestSafeRelPath(t *testing.T) {
	ok, err := SafeRelPath("client/WoW-3.3.5a.zip")
	if err != nil || ok != "client/WoW-3.3.5a.zip" {
		t.Fatalf("got %q %v", ok, err)
	}
	for _, p := range []string{"../etc/passwd", "client/../../etc/passwd", "/etc/passwd", ""} {
		if _, err := SafeRelPath(p); err == nil {
			t.Fatalf("expected reject %q", p)
		}
	}
}

func TestResolveUnder(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveUnder(root, "../passwd"); err == nil {
		t.Fatal("traversal")
	}
	abs, err := ResolveUnder(root, "client/game.zip")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "client", "game.zip")
	if abs != want {
		t.Fatalf("got %s want %s", abs, want)
	}
}

func TestCatalogAndScan(t *testing.T) {
	root := t.TempDir()
	mustMk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMk("catalog.json", `{
	  "intro": "Grab the client.",
	  "items": [
	    {"id":"client-335a","title":"WotLK Client","category":"client","file":"client/WoW-3.3.5a.zip","featured":true},
	    {"id":"missing-patch","title":"HD Patch","category":"patches","file":"patches/hd.zip"}
	  ]
	}`)
	mustMk("client/WoW-3.3.5a.zip", "PK\x03\x04 fake zip")
	mustMk("mods/Cool-Mod.zip", "PK\x03\x04 mod")
	mustMk("client/WoW-3.3.5a.zip.sha256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  WoW-3.3.5a.zip\n")

	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0
	if s.Intro() != "Grab the client." {
		t.Fatalf("intro %q", s.Intro())
	}
	all := s.List("all")
	if len(all) != 3 {
		t.Fatalf("want 3 items, got %d", len(all))
	}
	cli, ok := s.Get("client-335a")
	if !ok || !cli.Ready || cli.SHA256 == "" || !cli.Featured {
		t.Fatalf("client: %+v ok=%v", cli, ok)
	}
	miss, ok := s.Get("missing-patch")
	if !ok || miss.Ready {
		t.Fatalf("missing should list but not be ready: %+v", miss)
	}
	mods := s.List(CatMods)
	if len(mods) != 1 || mods[0].FileName != "Cool-Mod.zip" {
		t.Fatalf("scanned mod: %+v", mods)
	}
	if s.Counts()[CatClient] != 1 {
		t.Fatalf("counts %+v", s.Counts())
	}
	hits := s.Search("all", "cool")
	if len(hits) != 1 || hits[0].FileName != "Cool-Mod.zip" {
		t.Fatalf("search: %+v", hits)
	}
	if n := len(s.Search(CatClient, "cool")); n != 0 {
		t.Fatalf("client tab search should miss mod, got %d", n)
	}
}

func TestHumanSize(t *testing.T) {
	if HumanSize(512) != "512 B" {
		t.Fatal(HumanSize(512))
	}
	if HumanSize(10*1024*1024) != "10 MB" {
		t.Fatal(HumanSize(10 * 1024 * 1024))
	}
}

func TestSanitizeFileName(t *testing.T) {
	ok, err := SanitizeFileName("WoW-3.3.5a.zip")
	if err != nil || ok != "WoW-3.3.5a.zip" {
		t.Fatalf("got %q %v", ok, err)
	}
	if _, err := SanitizeFileName("../../etc/passwd"); err == nil {
		t.Fatal("traversal")
	}
	if _, err := SanitizeFileName("payload.php"); err == nil {
		t.Fatal("bad ext")
	}
	if _, err := SanitizeFileName(".hidden.zip"); err == nil {
		t.Fatal("hidden")
	}
	if _, err := SanitizeFileName("Wow.exe"); err == nil {
		t.Fatal("exe")
	}
}

func TestAddAndRemove(t *testing.T) {
	root := t.TempDir()
	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0
	if !s.Writable() {
		t.Fatal("temp dir should be writable")
	}

	it, err := s.Add(context.Background(), UploadInput{
		Title:    "Cool Mod",
		Category: "mods",
		FileName: "Cool-Mod.zip",
	}, strings.NewReader("PK zip body"))
	if err != nil {
		t.Fatal(err)
	}
	if it.ID == "" || !it.Ready || it.Size == 0 || it.SHA256 == "" {
		t.Fatalf("item %+v", it)
	}
	got, ok := s.Get(it.ID)
	if !ok || !got.Ready || got.Title != "Cool Mod" {
		t.Fatalf("get %+v ok=%v", got, ok)
	}
	raw, err := os.ReadFile(filepath.Join(root, "mods", "Cool-Mod.zip"))
	if err != nil || string(raw) != "PK zip body" {
		t.Fatalf("file %q %v", raw, err)
	}

	// Same name replaces the catalog slot and file.
	it2, err := s.Add(context.Background(), UploadInput{
		Title:    "Cool Mod v2",
		Category: "mods",
		FileName: "Cool-Mod.zip",
	}, strings.NewReader("PK zip v2"))
	if err != nil {
		t.Fatal(err)
	}
	if it2.ID != it.ID {
		t.Fatalf("expected replace id %s got %s", it.ID, it2.ID)
	}
	all := s.List(CatMods)
	if len(all) != 1 {
		t.Fatalf("want 1 mod, got %d", len(all))
	}

	if err := s.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(it.ID); ok {
		t.Fatal("still listed")
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "Cool-Mod.zip")); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
}

func TestAddClientAndAddonMetadata(t *testing.T) {
	root := t.TempDir()
	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0

	if _, err := s.Add(context.Background(), UploadInput{
		Category: "client", FileName: "wow.zip",
	}, strings.NewReader("PK")); err != ErrNeedName {
		t.Fatalf("need name: %v", err)
	}
	if _, err := s.Add(context.Background(), UploadInput{
		Category: "client", FileName: "wow.zip", Title: "WotLK",
	}, strings.NewReader("PK")); err != ErrNeedVersion {
		t.Fatalf("need version: %v", err)
	}
	cli, err := s.Add(context.Background(), UploadInput{
		Category: "client", FileName: "wow.zip", Title: "WotLK", Version: "3.3.5a",
	}, strings.NewReader("PK client"))
	if err != nil {
		t.Fatal(err)
	}
	if cli.Title != "WotLK" || cli.Version != "3.3.5a" || cli.UploadedAt == "" || cli.ClamAVLabel != ClamSkipped {
		t.Fatalf("client %+v", cli)
	}

	mod, err := s.Add(context.Background(), UploadInput{
		Category: "mods", FileName: "dbm.zip", AddonID: "dbm", Version: "4.2",
		SourceURL: "https://example.com/dbm", Description: "Boss timers.",
	}, strings.NewReader("PK mod"))
	if err != nil {
		t.Fatal(err)
	}
	if mod.ID != "dbm" || mod.AddonID != "dbm" || mod.Version != "4.2" || mod.SourceURL != "https://example.com/dbm" || mod.Description != "Boss timers." {
		t.Fatalf("addon %+v", mod)
	}

	patch, err := s.Add(context.Background(), UploadInput{
		Category: "patches", FileName: "hd.zip", Title: "ignored", Version: "nope",
		Description: "HD textures.",
	}, strings.NewReader("PK patch"))
	if err != nil {
		t.Fatal(err)
	}
	if patch.Version != "" || patch.Title != "hd" || patch.Description != "HD textures." {
		t.Fatalf("patch %+v", patch)
	}

	must, err := s.Add(context.Background(), UploadInput{
		Category: "patches", FileName: "core.zip", Description: "Required.", Mandatory: true,
	}, strings.NewReader("PK must"))
	if err != nil {
		t.Fatal(err)
	}
	if !must.Mandatory {
		t.Fatal("expected mandatory patch")
	}

	if _, err := s.Add(context.Background(), UploadInput{
		Category: "mods", FileName: "x.zip", SourceURL: "javascript:alert(1)",
	}, strings.NewReader("PK")); err != ErrBadSource {
		t.Fatalf("bad source: %v", err)
	}
}

type callScanner struct {
	called bool
	err    error
}

func (c *callScanner) Scan(ctx context.Context, rd io.Reader) error {
	c.called = true
	_, _ = io.Copy(io.Discard, rd)
	return c.err
}

func TestAddSkipsScanWhenTooBig(t *testing.T) {
	root := t.TempDir()
	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0
	sc := &callScanner{err: errors.New("should not scan")}
	s.SetScanner(sc)
	s.SetScanMax(4)
	it, err := s.Add(context.Background(), UploadInput{
		Title: "WotLK", Category: "client", Version: "3.3.5a", FileName: "wow.zip",
	}, strings.NewReader("12345"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.called {
		t.Fatal("scanner ran on oversized file")
	}
	if it.ClamAV != ClamTooBig || !strings.Contains(it.ClamAVLabel, "100 MB") {
		t.Fatalf("clamav %+v", it)
	}
}

type rejectScanner struct{ err error }

func (r rejectScanner) Scan(ctx context.Context, rd io.Reader) error {
	_, _ = io.Copy(io.Discard, rd)
	return r.err
}

func TestAddRejectsInfected(t *testing.T) {
	root := t.TempDir()
	s := New(root, filepath.Join(root, "catalog.json"))
	s.ttl = 0
	infected := errors.New("file failed the virus scan")
	s.SetScanner(rejectScanner{err: infected})
	_, err := s.Add(context.Background(), UploadInput{
		Title: "Bad", Category: "mods", FileName: "bad.zip",
	}, strings.NewReader("PK eicar"))
	if !errors.Is(err, infected) {
		t.Fatalf("got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "mods"))
	if len(entries) != 0 {
		t.Fatalf("infected file was published: %v", entries)
	}
}
