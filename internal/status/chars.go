package status

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/zeroward/waygate/internal/wow"
)

type Character struct {
	GUID     uint32
	Name     string
	Level    uint8
	Race     string
	RaceID   uint8
	Class    string
	ClassID  uint8
	Faction  string
	Gold     string
	Played   string
	Logout   string
	Location string
	Online   bool
}

func (c *Cache) AccountCharacters(ctx context.Context, accountID uint32) ([]Character, error) {
	if accountID == 0 {
		return c.AccountCharactersMany(ctx, nil)
	}
	return c.AccountCharactersMany(ctx, []uint32{accountID})
}

func (c *Cache) AccountCharactersMany(ctx context.Context, accountIDs []uint32) ([]Character, error) {
	if c.cfg.DemoMode || c.db == nil {
		return demoCharacters(), nil
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}
	ph := make([]string, len(accountIDs))
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		ph[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`
		SELECT c.`+"`guid`"+`, c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`,
		       c.`+"`money`"+`, c.`+"`totaltime`"+`, c.`+"`logout_time`"+`,
		       c.`+"`map`"+`, c.`+"`zone`"+`, c.`+"`online`"+`
		FROM %s c
		WHERE c.`+"`account`"+` IN (%s) AND c.`+"`deleteDate`"+` IS NULL
		ORDER BY c.`+"`online`"+` DESC, c.`+"`level`"+` DESC, c.`+"`name`"+` ASC`,
		c.db.QChar("characters"), strings.Join(ph, ","),
	)
	rows, err := c.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		var (
			guid               uint32
			name               string
			level, race, class uint8
			money, played, lo  uint32
			mapID, zone        uint32
			online             int
		)
		if err := rows.Scan(&guid, &name, &level, &race, &class, &money, &played, &lo, &mapID, &zone, &online); err != nil {
			return out, err
		}
		on := online != 0
		out = append(out, Character{
			GUID:     guid,
			Name:     name,
			Level:    level,
			Race:     wow.RaceName(race),
			RaceID:   race,
			Class:    wow.ClassName(class),
			ClassID:  class,
			Faction:  wow.Faction(race),
			Gold:     wow.Gold(money),
			Played:   wow.Playtime(played),
			Logout:   wow.LogoutLabel(on, lo),
			Location: wow.Location(mapID, zone),
			Online:   on,
		})
	}
	return out, rows.Err()
}

type AccountStat struct {
	LastLogin string
	Chars     int
}

func (c *Cache) AccountStats(ctx context.Context, accountIDs []uint32) map[uint32]AccountStat {
	out := make(map[uint32]AccountStat, len(accountIDs))
	if len(accountIDs) == 0 {
		return out
	}
	if c.cfg.DemoMode || c.db == nil {
		n := len(demoCharacters())
		for _, id := range accountIDs {
			out[id] = AccountStat{LastLogin: "—", Chars: n}
		}
		return out
	}
	ph := make([]string, len(accountIDs))
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		ph[i] = "?"
		args[i] = id
	}
	in := strings.Join(ph, ",")
	lq := fmt.Sprintf(`SELECT a.`+"`id`"+`, CAST(a.`+"`last_login`"+` AS CHAR) FROM %s a WHERE a.`+"`id`"+` IN (%s)`, c.db.QAuth("account"), in)
	rows, err := c.db.SQL.QueryContext(ctx, lq, args...)
	if err != nil {
		return out
	}
	for rows.Next() {
		var id uint32
		var last sql.NullString
		if err := rows.Scan(&id, &last); err != nil {
			break
		}
		st := AccountStat{LastLogin: "—"}
		if last.Valid {
			v := strings.TrimSpace(last.String)
			if v != "" && !strings.HasPrefix(v, "0000-00-00") {
				if len(v) >= 16 {
					st.LastLogin = v[:16]
				} else {
					st.LastLogin = v
				}
			}
		}
		out[id] = st
	}
	rows.Close()
	cq := fmt.Sprintf(`SELECT c.`+"`account`"+`, COUNT(*) FROM %s c WHERE c.`+"`account`"+` IN (%s) AND c.`+"`deleteDate`"+` IS NULL GROUP BY c.`+"`account`"+``,
		c.db.QChar("characters"), in)
	crows, err := c.db.SQL.QueryContext(ctx, cq, args...)
	if err != nil {
		return out
	}
	defer crows.Close()
	for crows.Next() {
		var id uint32
		var n int
		if err := crows.Scan(&id, &n); err != nil {
			return out
		}
		st := out[id]
		st.Chars = n
		out[id] = st
	}
	return out
}

func demoCharacters() []Character {
	return []Character{
		{
			GUID: 1, Name: "Frostwarden", Level: 80, Race: "Human", RaceID: 1,
			Class: "Paladin", ClassID: 2, Faction: "Alliance",
			Gold: "1234g 56s 78c", Played: wow.Playtime(980_000),
			Logout: "Online", Location: wow.Location(571, 210), Online: true,
		},
		{
			GUID: 2, Name: "NorthrendScout", Level: 77, Race: "Night Elf", RaceID: 4,
			Class: "Hunter", ClassID: 3, Faction: "Alliance",
			Gold: "88g 12s", Played: wow.Playtime(410_000),
			Logout: wow.LogoutLabel(false, 1_704_067_200), Location: wow.Location(571, 3537),
		},
	}
}
