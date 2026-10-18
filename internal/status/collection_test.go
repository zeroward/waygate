package status

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/soap"
	_ "modernc.org/sqlite"
)

func collectionFixture(t *testing.T) *Cache {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = d.Close() })
	_, err = d.Exec(`
		ATTACH DATABASE ':memory:' AS auth;
		ATTACH DATABASE ':memory:' AS characters;
		ATTACH DATABASE ':memory:' AS world;
		CREATE TABLE auth.account(id INTEGER, username TEXT);
		CREATE TABLE auth.account_access(id INTEGER, gmlevel INTEGER);
		CREATE TABLE characters.characters(name TEXT, account INTEGER, level INTEGER, race INTEGER, class INTEGER,
			zone INTEGER, deleteDate INTEGER, online INTEGER, extra_flags INTEGER,
			totaltime INTEGER, totalKills INTEGER, totalHonorPoints INTEGER, arenaPoints INTEGER, money INTEGER);
		CREATE TABLE world.version(core_version TEXT);
		CREATE TABLE world.module_string(module TEXT);
		INSERT INTO world.version VALUES ('AzerothCore test');
		INSERT INTO world.module_string VALUES ('mod-playerbots'), ('mod-playerbots');
		INSERT INTO auth.account VALUES (1,'HUMAN'),(2,'STAFF'),(3,'GMFLAG'),(4,'RNDBOT1'),(5,'OFFLINE'),(6,'DELETED'),(7,'AUCTION');
		INSERT INTO auth.account_access VALUES (2,3);
		INSERT INTO characters.characters VALUES
			('Hero',1,80,1,2,210,NULL,1,0,1000,100,200,300,10000),
			('Staff',2,80,2,6,210,NULL,1,0,2000,200,400,600,20000),
			('Flagged',3,80,11,5,210,NULL,1,1,3000,300,600,900,30000),
			('Bot',4,80,2,1,210,NULL,1,0,9999,999,999,999,99999),
			('Offline',5,70,3,3,210,NULL,0,0,500,50,100,150,5000),
			('Deleted',6,80,1,1,210,123,1,0,9999,999,999,999,99999),
			('Auctioneer',7,80,1,1,210,NULL,1,0,9999,999,999,999,99999);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Config{
		RealmName: "Test", WorldHost: "127.0.0.1", WorldPort: 0,
		BotPrefixes: []string{"RNDBOT"}, LeaderboardSize: 20, StatusCache: time.Minute,
	}, &db.DB{SQL: d, AuthDB: "auth", CharactersDB: "characters", WorldDB: "world"}, nil)
}

func TestCollectionReadOnlyAndPopulationSemantics(t *testing.T) {
	c := collectionFixture(t)
	if _, err := c.db.SQL.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	s := c.Get(context.Background())
	if len(s.Unavailable) != 0 {
		t.Fatalf("collection failed: %v", s.Unavailable)
	}
	if s.RealmUp || s.Demo {
		t.Fatal("unreachable realm should be valid down status")
	}
	if s.Online != 4 || s.OnlineBots != 1 || s.OnlineGMs != 2 || s.OnlineTotal != 5 || s.Alliance != 3 || s.Horde != 1 {
		t.Fatalf("counts: %+v", s)
	}
	// Auctioneer is excluded from rosters/boards but remains in aggregate counts.
	if len(s.Players) != 3 || s.Players[0].Name != "Flagged" || s.Players[2].Name != "Staff" {
		t.Fatalf("roster: %+v", s.Players)
	}
	if len(s.Playtime) != 4 || s.Playtime[0].Name != "Flagged" || s.Playtime[3].Name != "Offline" {
		t.Fatalf("board: %+v", s.Playtime)
	}
	if s.WorldVersion != "AzerothCore test" || len(s.Modules) != 1 {
		t.Fatal("world data absent")
	}
	c.cfg.HideGM = true
	c.cfg.LeaderboardSize = 1
	s = c.refresh(context.Background())
	if len(s.Players) != 3 || s.OnlineGMs != 2 || len(s.Playtime) != 1 || s.Playtime[0].Name != "Hero" {
		t.Fatalf("GM filtering/limit incorrect: %+v", s)
	}
}

func TestCollectionEmptyIsAvailable(t *testing.T) {
	c := collectionFixture(t)
	if _, err := c.db.SQL.Exec("DELETE FROM characters.characters; DELETE FROM world.module_string; DELETE FROM world.version"); err != nil {
		t.Fatal(err)
	}
	s := c.refresh(context.Background())
	if len(s.Unavailable) != 0 || s.OnlineTotal != 0 || len(s.Players) != 0 || len(s.Playtime) != 0 || len(s.Modules) != 0 || s.WorldVersion != "" {
		t.Fatalf("empty incorrectly unavailable: %+v", s)
	}
}

func TestCollectionFailuresAreSectionSpecific(t *testing.T) {
	for _, tc := range []struct{ name, query, failed string }{
		{"counts", "DROP TABLE auth.account_access", "status"},
		{"version", "DROP TABLE world.version", "status"},
		{"player scan", "UPDATE characters.characters SET zone='invalid' WHERE name='Hero'", "players"},
		{"board scan", "UPDATE characters.characters SET totaltime='invalid' WHERE name='Hero'", "leaderboards"},
		{"modules", "DROP TABLE world.module_string", "modules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := collectionFixture(t)
			if _, err := c.db.SQL.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			s := c.refresh(context.Background())
			if len(s.Unavailable) != 1 || !s.Unavailable[tc.failed] {
				t.Fatalf("failures: %v", s.Unavailable)
			}
		})
	}
	c := collectionFixture(t)
	c.cfg.ModulesDir = t.TempDir() + "/missing"
	if s := c.refresh(context.Background()); len(s.Unavailable) != 1 || !s.Unavailable["modules"] {
		t.Fatalf("directory error: %v", s.Unavailable)
	}
	_ = c.db.Close()
	if s := c.refresh(context.Background()); len(s.Unavailable) != 4 || s.Demo {
		t.Fatalf("database failure: %v", s.Unavailable)
	}
	if s := New(config.Config{}, nil, nil).Get(context.Background()); len(s.Unavailable) != 4 || s.Demo || len(s.Players) != 0 {
		t.Fatal("missing database returned fake realm data")
	}
}

func TestOptionalWorldTables(t *testing.T) {
	if !optionalTableMissing(&mysql.MySQLError{Number: 1146}) {
		t.Fatal("missing table should be optional")
	}
	for _, err := range []error{nil, &mysql.MySQLError{Number: 1142}, &mysql.MySQLError{Number: 1049}, errors.New("connection lost")} {
		if optionalTableMissing(err) {
			t.Fatalf("real failure classified as absence: %v", err)
		}
	}
	c := collectionFixture(t)
	c.db.WorldDB = ""
	if _, err := c.db.SQL.Exec("DETACH DATABASE world"); err != nil {
		t.Fatal(err)
	}
	if s := c.refresh(context.Background()); len(s.Unavailable) != 0 || s.WorldVersion != "" || len(s.Modules) != 0 {
		t.Fatal("unconfigured optional data should be empty")
	}
}

type collectionTransport func(*http.Request) (*http.Response, error)

func (f collectionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCacheCoalescesAndSurvivesCanceledCaller(t *testing.T) {
	c := collectionFixture(t)
	c.cfg.SOAPEnabled, c.cfg.SOAPHost, c.cfg.SOAPUsername, c.cfg.SOAPPassword = true, "test", "test", "test"
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c.soap = &soap.Client{BaseURL: "http://test/", HTTP: &http.Client{Transport: collectionTransport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "<command>server info</command>") {
			t.Error("probe sent a modifying SOAP command")
		}
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<Envelope><Body><executeCommandResponse><result>ok</result></executeCommandResponse></Body></Envelope>`)), Header: make(http.Header)}, nil
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan Snapshot, 1)
	go func() { canceled <- c.Get(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancel()
	select {
	case s := <-canceled:
		if len(s.Unavailable) != 4 {
			t.Fatal("canceled caller got false success")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter stuck")
	}
	const count = 16
	results := make(chan Snapshot, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- c.Get(context.Background()) }()
	}
	close(release)
	wg.Wait()
	close(results)
	var generated time.Time
	for s := range results {
		if !s.RealmUp || len(s.Unavailable) != 0 {
			t.Fatalf("caller canceled shared collection: %+v", s)
		}
		if generated.IsZero() {
			generated = s.GeneratedAt
		}
		if generated != s.GeneratedAt {
			t.Fatal("concurrent snapshots differ")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate refreshes: %d", calls.Load())
	}
	if s := c.Get(context.Background()); s.GeneratedAt != generated || calls.Load() != 1 {
		t.Fatal("fresh cache not reused")
	}
	c.mu.Lock()
	c.until = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if s := c.Get(context.Background()); s.GeneratedAt == generated || calls.Load() != 2 {
		t.Fatal("expired cache not refreshed")
	}
}
