package status

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/zeroward/waygate/internal/acmod"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/soap"
	"github.com/zeroward/waygate/internal/wow"
)

type Snapshot struct {
	RealmName    string
	RealmUp      bool
	Online       int // non-bot players, including GMs
	OnlineBots   int
	OnlineGMs    int
	OnlineTotal  int
	Alliance     int
	Horde        int
	CoreName     string
	Expansion    string
	PublicHost   string
	PublicAuth   int
	PublicWorld  int
	Blurb        string
	Demo         bool
	GeneratedAt  time.Time
	Players      []Player
	Playtime     []BoardRow
	Kills        []BoardRow
	Honor        []BoardRow
	Arena        []BoardRow
	Gold         []BoardRow
	WorldVersion string
	Modules      []acmod.Module
	// Unavailable contains failed sections: status, players, leaderboards, modules.
	// Snapshots (including their slices and maps) are immutable after publication.
	Unavailable map[string]bool
}

type Player struct {
	Name    string
	Level   uint8
	Race    string
	RaceID  uint8
	Class   string
	ClassID uint8
	Zone    string
	Faction string
}

type BoardRow struct {
	Rank  int
	Name  string
	Level uint8
	Class string
	Value string
	Raw   uint32
}

type Cache struct {
	cfg  config.Config
	db   *db.DB
	soap *soap.Client

	mu         sync.Mutex
	snap       Snapshot
	until      time.Time
	refreshing chan struct{}
}

func New(cfg config.Config, database *db.DB, soapc *soap.Client) *Cache {
	return &Cache{cfg: cfg, db: database, soap: soapc}
}

func (c *Cache) Database() *db.DB { return c.db }

// Get shares one bounded refresh across web and API callers. A disconnected caller
// cannot cancel collection for other clients or publish cancellation as realm data.
func (c *Cache) Get(ctx context.Context) Snapshot {
	c.mu.Lock()
	if time.Now().Before(c.until) && !c.snap.GeneratedAt.IsZero() {
		s := c.snap
		c.mu.Unlock()
		return s
	}
	if c.refreshing == nil {
		c.refreshing = make(chan struct{})
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s := c.refresh(refreshCtx)
			c.mu.Lock()
			c.snap = s
			c.until = time.Now().Add(c.cfg.StatusCache)
			close(c.refreshing)
			c.refreshing = nil
			c.mu.Unlock()
		}()
	}
	done := c.refreshing
	c.mu.Unlock()
	select {
	case <-done:
		c.mu.Lock()
		s := c.snap
		c.mu.Unlock()
		return s
	case <-ctx.Done():
		return c.unavailableSnapshot()
	}
}

func (c *Cache) baseSnapshot() Snapshot {
	return Snapshot{
		RealmName:   c.cfg.RealmName,
		CoreName:    c.cfg.CoreName,
		Expansion:   wow.ExpansionName(c.cfg.DefaultExpansion),
		PublicHost:  c.cfg.PublicHost,
		PublicAuth:  c.cfg.PublicAuthPort,
		PublicWorld: c.cfg.PublicWorldPort,
		Blurb:       c.cfg.SiteBlurb,
		Demo:        c.cfg.DemoMode,
		GeneratedAt: time.Now().UTC(),
		Unavailable: make(map[string]bool),
	}
}

func (c *Cache) unavailableSnapshot() Snapshot {
	s := c.baseSnapshot()
	for _, section := range []string{"status", "players", "leaderboards", "modules"} {
		s.Unavailable[section] = true
	}
	return s
}

func (c *Cache) refresh(ctx context.Context) Snapshot {
	s := c.baseSnapshot()
	if c.cfg.DemoMode {
		return demoSnapshot(s)
	}
	if c.db == nil || c.db.SQL == nil {
		return c.unavailableSnapshot()
	}
	s.RealmUp = c.probeWorld(ctx)
	if ctx.Err() != nil {
		s.Unavailable["status"] = true
	}
	var err error
	s.WorldVersion, err = c.worldVersion(ctx)
	if err != nil {
		s.Unavailable["status"] = true
	}
	s.Online, s.OnlineBots, s.OnlineGMs, s.Alliance, s.Horde, err = c.onlineCounts(ctx)
	if err != nil {
		s.Unavailable["status"] = true
	}
	s.OnlineTotal = s.Online + s.OnlineBots
	s.Players, err = c.onlinePlayers(ctx)
	if err != nil {
		s.Unavailable["players"] = true
	}
	for _, board := range []struct {
		dest   *[]BoardRow
		col    string
		format func(uint32) string
	}{
		{&s.Playtime, "c.`totaltime`", playtimeFmt},
		{&s.Kills, "c.`totalKills`", uintFmt},
		{&s.Honor, "c.`totalHonorPoints`", uintFmt},
		{&s.Arena, "c.`arenaPoints`", uintFmt},
		{&s.Gold, "c.`money`", goldFmt},
	} {
		*board.dest, err = c.board(ctx, board.col, board.format)
		if err != nil {
			s.Unavailable["leaderboards"] = true
		}
	}
	s.Modules, err = c.listModules(ctx)
	if err != nil {
		s.Unavailable["modules"] = true
	}
	return s
}

func (c *Cache) listModules(ctx context.Context) ([]acmod.Module, error) {
	mods, scanErr := acmod.ScanDirWithError(c.cfg.ModulesDir)
	if c.db != nil && c.db.WorldDB != "" {
		extra, err := c.modulesFromDB(ctx)
		return acmod.Merge(mods, extra), errors.Join(scanErr, err)
	}
	return mods, scanErr
}

// The world tables are optional on some AzerothCore installations. Missing
// tables are absence, while permission/connectivity/query failures are errors.
func optionalTableMissing(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1146
}

func (c *Cache) modulesFromDB(ctx context.Context) ([]acmod.Module, error) {
	q := fmt.Sprintf("SELECT DISTINCT `module` FROM %s WHERE `module` <> '' ORDER BY `module`", c.db.QWorld("module_string"))
	rows, err := c.db.SQL.QueryContext(ctx, q)
	if optionalTableMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []acmod.Module
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, acmod.Module{ID: id, Title: ""})
	}
	return out, rows.Err()
}

func playtimeFmt(v uint32) string { return wow.Playtime(v) }
func uintFmt(v uint32) string     { return fmt.Sprintf("%d", v) }
func goldFmt(v uint32) string     { return wow.Gold(v) }

// Character names omitted from boards (AH bots and similar). Account bots
// are already filtered via BOT_USERNAME_PREFIXES (rndbot*).
var boardSkipNames = []string{"AUCTIONEER"}

func (c *Cache) probeWorld(ctx context.Context) bool {
	if c.soap != nil && c.cfg.SOAPConfigured() {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := c.soap.Ping(pctx); err == nil {
			return true
		}
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	addr := net.JoinHostPort(c.cfg.WorldHost, fmt.Sprintf("%d", c.cfg.WorldPort))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *Cache) worldVersion(ctx context.Context) (string, error) {
	if c.db == nil || c.db.WorldDB == "" {
		return "", nil
	}
	q := fmt.Sprintf("SELECT `core_version` FROM %s LIMIT 1", c.db.QWorld("version"))
	var v string
	err := c.db.SQL.QueryRowContext(ctx, q).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || optionalTableMissing(err) {
		return "", nil
	}
	return v, err
}

func (c *Cache) onlineCounts(ctx context.Context) (players, bots, gms, alliance, horde int, err error) {
	botSQL, botArgs := c.botPredicate()
	q := fmt.Sprintf(`
		SELECT
			COALESCE(SUM(CASE WHEN is_bot = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_bot = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_bot = 0 AND is_gm = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_bot = 0 AND race IN (1,3,4,7,11) THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_bot = 0 AND race IN (2,5,6,8,10) THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT
				c.`+"`race`"+` AS race,
				CASE WHEN %s THEN 1 ELSE 0 END AS is_bot,
				CASE WHEN EXISTS (
					SELECT 1 FROM %s aa WHERE aa.`+"`id`"+` = a.`+"`id`"+` AND aa.`+"`gmlevel`"+` > 0
				) OR (c.`+"`extra_flags`"+` & 1) <> 0 THEN 1 ELSE 0 END AS is_gm
			FROM %s c
			INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
			WHERE c.`+"`deleteDate`"+` IS NULL AND c.`+"`online`"+` = 1
		) t`,
		botSQL,
		c.db.QAuth("account_access"),
		c.db.QChar("characters"),
		c.db.QAuth("account"),
	)
	err = c.db.SQL.QueryRowContext(ctx, q, botArgs...).Scan(&players, &bots, &gms, &alliance, &horde)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return players, bots, gms, alliance, horde, nil
}

func (c *Cache) botPredicate() (string, []any) {
	if len(c.cfg.BotPrefixes) == 0 {
		return "0", nil
	}
	parts := make([]string, 0, len(c.cfg.BotPrefixes))
	args := make([]any, 0, len(c.cfg.BotPrefixes))
	for _, p := range c.cfg.BotPrefixes {
		parts = append(parts, "a.`username` LIKE ?")
		args = append(args, p+"%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (c *Cache) onlinePlayers(ctx context.Context) ([]Player, error) {
	q, args := c.playerQuery(true)
	rows, err := c.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Player
	for rows.Next() {
		var (
			name               string
			level, race, class uint8
			zone               uint32
		)
		if err := rows.Scan(&name, &level, &race, &class, &zone); err != nil {
			return nil, err
		}
		out = append(out, Player{
			Name:    name,
			Level:   level,
			Race:    wow.RaceName(race),
			RaceID:  race,
			Class:   wow.ClassName(class),
			ClassID: class,
			Zone:    wow.ZoneName(zone),
			Faction: wow.Faction(race),
		})
	}
	return out, rows.Err()
}

func (c *Cache) board(ctx context.Context, col string, fmtVal func(uint32) string) ([]BoardRow, error) {
	where, args := c.filters(false, c.cfg.HideGM)
	q := fmt.Sprintf(
		`SELECT c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`class`"+`, %s AS v
		 FROM %s c
		 INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		 %s
		 ORDER BY v DESC
		 LIMIT ?`,
		col, c.db.QChar("characters"), c.db.QAuth("account"), where,
	)
	args = append(args, c.cfg.LeaderboardSize)
	rows, err := c.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BoardRow
	rank := 1
	for rows.Next() {
		var name string
		var level, class uint8
		var v uint32
		if err := rows.Scan(&name, &level, &class, &v); err != nil {
			return nil, err
		}
		out = append(out, BoardRow{
			Rank:  rank,
			Name:  name,
			Level: level,
			Class: wow.ClassName(class),
			Value: fmtVal(v),
			Raw:   v,
		})
		rank++
	}
	return out, rows.Err()
}

func (c *Cache) playerQuery(onlineOnly bool) (string, []any) {
	// Online roster shows real players including GMs; bots stay off the list.
	where, args := c.filters(onlineOnly, false)
	q := fmt.Sprintf(
		`SELECT c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`, c.`+"`zone`"+`
		 FROM %s c
		 INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		 %s
		 ORDER BY c.`+"`level`"+` DESC, c.`+"`name`"+` ASC`,
		c.db.QChar("characters"), c.db.QAuth("account"), where,
	)
	return q, args
}

func (c *Cache) filters(onlineOnly, hideGM bool) (string, []any) {
	var b strings.Builder
	var args []any
	b.WriteString("WHERE c.`deleteDate` IS NULL")
	if onlineOnly {
		b.WriteString(" AND c.`online` = 1")
	}
	for _, p := range c.cfg.BotPrefixes {
		b.WriteString(" AND a.`username` NOT LIKE ?")
		args = append(args, p+"%")
	}
	for _, n := range boardSkipNames {
		b.WriteString(" AND UPPER(c.`name`) <> ?")
		args = append(args, n)
	}
	if hideGM {
		b.WriteString(" AND NOT EXISTS (SELECT 1 FROM " + c.db.QAuth("account_access") + " aa WHERE aa.`id` = a.`id` AND aa.`gmlevel` > 0)")
		b.WriteString(" AND (c.`extra_flags` & 1) = 0")
	}
	return b.String(), args
}

func demoSnapshot(base Snapshot) Snapshot {
	base.RealmUp = true
	base.WorldVersion = "AzerothCore 3.3.5a (demo)"
	base.Players = []Player{
		{Name: "Frostwarden", Level: 80, Race: "Human", RaceID: 1, Class: "Paladin", ClassID: 2, Zone: wow.ZoneName(210), Faction: "Alliance"},
		{Name: "Icemourn", Level: 80, Race: "Orc", RaceID: 2, Class: "Death Knight", ClassID: 6, Zone: wow.ZoneName(4812), Faction: "Horde"},
		{Name: "NorthrendScout", Level: 77, Race: "Night Elf", RaceID: 4, Class: "Hunter", ClassID: 3, Zone: wow.ZoneName(3537), Faction: "Alliance"},
		{Name: "Plaguebloom", Level: 80, Race: "Undead", RaceID: 5, Class: "Warlock", ClassID: 9, Zone: wow.ZoneName(139), Faction: "Horde"},
		{Name: "Stormpike", Level: 68, Race: "Dwarf", RaceID: 3, Class: "Warrior", ClassID: 1, Zone: wow.ZoneName(65), Faction: "Alliance"},
		{Name: "Sunreaver", Level: 80, Race: "Blood Elf", RaceID: 10, Class: "Mage", ClassID: 8, Zone: wow.ZoneName(4395), Faction: "Horde"},
		{Name: "Anchorite", Level: 71, Race: "Draenei", RaceID: 11, Class: "Priest", ClassID: 5, Zone: wow.ZoneName(394), Faction: "Alliance"},
		{Name: "Grimtotem", Level: 80, Race: "Tauren", RaceID: 6, Class: "Druid", ClassID: 11, Zone: wow.ZoneName(3711), Faction: "Horde"},
	}
	base.Online = len(base.Players) + 1 // GMs count as players
	base.OnlineBots = 24
	base.OnlineGMs = 1
	base.OnlineTotal = base.Online + base.OnlineBots
	for _, p := range base.Players {
		if p.Faction == "Alliance" {
			base.Alliance++
		} else {
			base.Horde++
		}
	}
	mk := func(name string, level uint8, class string, raw uint32, val string) BoardRow {
		return BoardRow{Name: name, Level: level, Class: class, Raw: raw, Value: val}
	}
	base.Playtime = rankRows([]BoardRow{
		mk("Icemourn", 80, "Death Knight", 1200_000, wow.Playtime(1200_000)),
		mk("Frostwarden", 80, "Paladin", 980_000, wow.Playtime(980_000)),
		mk("Plaguebloom", 80, "Warlock", 810_000, wow.Playtime(810_000)),
		mk("Sunreaver", 80, "Mage", 640_000, wow.Playtime(640_000)),
		mk("Grimtotem", 80, "Druid", 500_000, wow.Playtime(500_000)),
	})
	base.Kills = rankRows([]BoardRow{
		mk("Icemourn", 80, "Death Knight", 4200, "4200"),
		mk("Frostwarden", 80, "Paladin", 3100, "3100"),
		mk("Sunreaver", 80, "Mage", 1800, "1800"),
	})
	base.Honor = rankRows([]BoardRow{
		mk("Frostwarden", 80, "Paladin", 85000, "85000"),
		mk("Icemourn", 80, "Death Knight", 72000, "72000"),
		mk("Stormpike", 68, "Warrior", 14000, "14000"),
	})
	base.Arena = rankRows([]BoardRow{
		mk("Sunreaver", 80, "Mage", 1850, "1850"),
		mk("Anchorite", 71, "Priest", 1600, "1600"),
		mk("NorthrendScout", 77, "Hunter", 1420, "1420"),
	})
	base.Gold = rankRows([]BoardRow{
		mk("Frostwarden", 80, "Paladin", 12_345_678, wow.Gold(12_345_678)),
		mk("Icemourn", 80, "Death Knight", 8_800_000, wow.Gold(8_800_000)),
		mk("Sunreaver", 80, "Mage", 250_000, wow.Gold(250_000)),
	})
	base.Modules = []acmod.Module{
		{ID: "mod-playerbots", Title: "Playerbots", Blurb: "Random and player-controlled AI companions."},
		{ID: "mod-ah-bot", Title: "AH Bot"},
		{ID: "mod-aoe-loot", Title: "AoE Loot"},
		{ID: "mod-autobalance", Title: "Autobalance"},
		{ID: "mod-transmog", Title: "Transmog"},
	}
	return base
}

func rankRows(in []BoardRow) []BoardRow {
	for i := range in {
		in[i].Rank = i + 1
	}
	return in
}
