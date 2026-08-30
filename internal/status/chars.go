package status

import (
	"context"
	"fmt"

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
	if c.cfg.DemoMode || c.db == nil {
		return demoCharacters(), nil
	}
	if accountID == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		SELECT c.`+"`guid`"+`, c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`,
		       c.`+"`money`"+`, c.`+"`totaltime`"+`, c.`+"`logout_time`"+`,
		       c.`+"`map`"+`, c.`+"`zone`"+`, c.`+"`online`"+`
		FROM %s c
		WHERE c.`+"`account`"+` = ? AND c.`+"`deleteDate`"+` IS NULL
		ORDER BY c.`+"`online`"+` DESC, c.`+"`level`"+` DESC, c.`+"`name`"+` ASC`,
		c.db.QChar("characters"),
	)
	rows, err := c.db.SQL.QueryContext(ctx, q, accountID)
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
