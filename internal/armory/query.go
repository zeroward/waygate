package armory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/wow"
)

func (s *Service) botWhere() (string, []any) {
	var b strings.Builder
	var args []any
	for _, p := range s.cfg.BotPrefixes {
		b.WriteString(" AND a.`username` NOT LIKE ?")
		args = append(args, p+"%")
	}
	return b.String(), args
}

func (s *Service) searchSQL(ctx context.Context, q string) []SearchHit {
	botSQL, botArgs := s.botWhere()
	query := fmt.Sprintf(`
		SELECT c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`
		FROM %s c
		INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		WHERE c.`+"`deleteDate`"+` IS NULL AND c.`+"`name`"+` LIKE ?%s
		ORDER BY c.`+"`name`"+` ASC
		LIMIT ?`, s.db.QChar("characters"), s.db.QAuth("account"), botSQL)
	args := make([]any, 0, len(botArgs)+2)
	args = append(args, q+"%")
	args = append(args, botArgs...)
	args = append(args, searchLimit)
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var name string
		var level, race, class uint8
		if err := rows.Scan(&name, &level, &race, &class); err != nil {
			return out
		}
		out = append(out, SearchHit{
			Name:    name,
			Level:   level,
			Race:    wow.RaceName(race),
			Class:   wow.ClassName(class),
			ClassID: class,
			Faction: wow.Faction(race),
		})
	}
	return out
}

func (s *Service) inspectSQL(ctx context.Context, name string) (Profile, bool) {
	botSQL, botArgs := s.botWhere()
	q := fmt.Sprintf(`
		SELECT c.`+"`guid`"+`, c.`+"`name`"+`, c.`+"`race`"+`, c.`+"`class`"+`, c.`+"`gender`"+`,
		       c.`+"`level`"+`, c.`+"`money`"+`, c.`+"`totaltime`"+`, c.`+"`logout_time`"+`,
		       c.`+"`online`"+`, c.`+"`map`"+`, c.`+"`zone`"+`, c.`+"`arenaPoints`"+`,
		       c.`+"`totalHonorPoints`"+`, c.`+"`totalKills`"+`, c.`+"`activeTalentGroup`"+`,
		       c.`+"`talentGroupsCount`"+`
		FROM %s c
		INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		WHERE c.`+"`deleteDate`"+` IS NULL AND c.`+"`name`"+` = ?%s
		LIMIT 1`, s.db.QChar("characters"), s.db.QAuth("account"), botSQL)
	args := append([]any{name}, botArgs...)
	var p Profile
	var race, class, gender, level uint8
	var money, played, logout, mapID, zone, honor, arena, hk uint32
	var online, activeSpec, specCount int
	err := s.db.SQL.QueryRowContext(ctx, q, args...).Scan(
		&p.GUID, &p.Name, &race, &class, &gender, &level, &money, &played, &logout,
		&online, &mapID, &zone, &arena, &honor, &hk, &activeSpec, &specCount,
	)
	if err != nil {
		return Profile{}, false
	}
	p.Level = level
	p.Online = online != 0
	p.Honor = honor
	p.ArenaPoints = arena
	p.HonorableKills = hk
	p.ActiveSpec = activeSpec
	fillSheet(&p, race, class, gender, money, played, logout, mapID, zone)
	p.Guild = s.guildName(ctx, p.GUID)
	p.Gear = s.gear(ctx, p.GUID)
	p.Specs = s.talents(ctx, p.GUID, activeSpec, specCount)
	p.Achievements = s.achievements(ctx, p.GUID)
	p.Arena = s.arena(ctx, p.GUID)
	return p, true
}

func (s *Service) guildName(ctx context.Context, guid uint32) string {
	q := fmt.Sprintf(`SELECT g.`+"`name`"+` FROM %s gm INNER JOIN %s g ON g.`+"`guildid`"+` = gm.`+"`guildid`"+` WHERE gm.`+"`guid`"+` = ?`,
		s.db.QChar("guild_member"), s.db.QChar("guild"))
	var name string
	if err := s.db.SQL.QueryRowContext(ctx, q, guid).Scan(&name); err != nil {
		return ""
	}
	return name
}

func (s *Service) gear(ctx context.Context, guid uint32) []GearItem {
	out := emptyGear()
	q := fmt.Sprintf(`
		SELECT ci.`+"`slot`"+`, ii.`+"`itemEntry`"+`, COALESCE(it.`+"`name`"+`, ''), COALESCE(it.`+"`Quality`"+`, 0)
		FROM %s ci
		INNER JOIN %s ii ON ii.`+"`guid`"+` = ci.`+"`item`"+`
		LEFT JOIN %s it ON it.`+"`entry`"+` = ii.`+"`itemEntry`"+`
		WHERE ci.`+"`guid`"+` = ? AND ci.`+"`bag`"+` = 0 AND ci.`+"`slot`"+` BETWEEN 0 AND 18`,
		s.db.QChar("character_inventory"), s.db.QChar("item_instance"), s.db.QWorld("item_template"))
	rows, err := s.db.SQL.QueryContext(ctx, q, guid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var slot uint8
		var entry uint32
		var name string
		var quality uint8
		if err := rows.Scan(&slot, &entry, &name, &quality); err != nil {
			return out
		}
		if int(slot) >= len(out) {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("Item %d", entry)
		}
		out[slot] = GearItem{Slot: slot, SlotName: SlotName(slot), Entry: entry, Name: name, Quality: quality}
	}
	return out
}

func (s *Service) talents(ctx context.Context, guid uint32, active, count int) []TalentSpec {
	if count < 1 {
		count = 1
	}
	if count > 2 {
		count = 2
	}
	specs := make([]TalentSpec, count)
	for i := 0; i < count; i++ {
		specs[i] = TalentSpec{
			Index:  i,
			Label:  specLabel(i),
			Active: i == active,
		}
	}
	q := fmt.Sprintf(`SELECT `+"`spell`"+`, `+"`specMask`"+` FROM %s WHERE `+"`guid`"+` = ?`, s.db.QChar("character_talent"))
	rows, err := s.db.SQL.QueryContext(ctx, q, guid)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var spell uint32
			var mask int
			if err := rows.Scan(&spell, &mask); err != nil {
				break
			}
			t := Talent{Spell: spell, Name: TalentName(spell)}
			for i := range specs {
				if mask&(1<<i) != 0 {
					specs[i].Talents = append(specs[i].Talents, t)
				}
			}
		}
	}
	gq := fmt.Sprintf(`SELECT `+"`talentGroup`"+`, `+"`glyph1`"+`, `+"`glyph2`"+`, `+"`glyph3`"+`, `+"`glyph4`"+`, `+"`glyph5`"+`, `+"`glyph6`"+` FROM %s WHERE `+"`guid`"+` = ?`,
		s.db.QChar("character_glyphs"))
	grows, err := s.db.SQL.QueryContext(ctx, gq, guid)
	if err != nil {
		return specs
	}
	defer grows.Close()
	for grows.Next() {
		var group int
		var ids [6]uint32
		if err := grows.Scan(&group, &ids[0], &ids[1], &ids[2], &ids[3], &ids[4], &ids[5]); err != nil {
			return specs
		}
		if group < 0 || group >= len(specs) {
			continue
		}
		for i, id := range ids {
			if id == 0 {
				continue
			}
			specs[group].Glyphs = append(specs[group].Glyphs, Glyph{
				ID:    id,
				Name:  GlyphName(id),
				Major: i < 3,
				Slot:  i + 1,
			})
		}
	}
	return specs
}

func specLabel(i int) string {
	if i == 0 {
		return "Primary"
	}
	return "Secondary"
}

func (s *Service) achievements(ctx context.Context, guid uint32) []Achievement {
	q := fmt.Sprintf(`SELECT `+"`achievement`"+`, `+"`date`"+` FROM %s WHERE `+"`guid`"+` = ? ORDER BY `+"`date`"+` DESC`,
		s.db.QChar("character_achievement"))
	rows, err := s.db.SQL.QueryContext(ctx, q, guid)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Achievement
	for rows.Next() {
		var id, date uint32
		if err := rows.Scan(&id, &date); err != nil {
			return out
		}
		out = append(out, Achievement{
			ID:   id,
			Name: AchievementName(id),
			Date: time.Unix(int64(date), 0).UTC().Format("2006-01-02"),
		})
	}
	return out
}

func (s *Service) arena(ctx context.Context, guid uint32) []ArenaTeam {
	q := fmt.Sprintf(`
		SELECT at.`+"`name`"+`, at.`+"`type`"+`, at.`+"`rating`"+`, at.`+"`seasonGames`"+`, at.`+"`seasonWins`"+`, atm.`+"`personalRating`"+`
		FROM %s atm
		INNER JOIN %s at ON at.`+"`arenaTeamId`"+` = atm.`+"`arenaTeamId`"+`
		WHERE atm.`+"`guid`"+` = ?
		ORDER BY at.`+"`type`"+` ASC`, s.db.QChar("arena_team_member"), s.db.QChar("arena_team"))
	rows, err := s.db.SQL.QueryContext(ctx, q, guid)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ArenaTeam
	for rows.Next() {
		var t ArenaTeam
		if err := rows.Scan(&t.Name, &t.Type, &t.Rating, &t.SeasonGames, &t.SeasonWins, &t.PersonalRating); err != nil {
			return out
		}
		t.Bracket = arenaBracket(t.Type)
		out = append(out, t)
	}
	return out
}
