package companion

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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

func (s *Service) listSQL(ctx context.Context, accountIDs []uint32) []Pick {
	if len(accountIDs) == 0 {
		return nil
	}
	ph := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+8)
	for i, id := range accountIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	botSQL, botArgs := s.botWhere()
	args = append(args, botArgs...)
	q := fmt.Sprintf(`
		SELECT c.`+"`guid`"+`, c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`, c.`+"`online`"+`
		FROM %s c
		INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		WHERE c.`+"`account`"+` IN (%s) AND c.`+"`deleteDate`"+` IS NULL%s
		ORDER BY c.`+"`online`"+` DESC, c.`+"`level`"+` DESC, c.`+"`name`"+` ASC`,
		s.db.QChar("characters"), s.db.QAuth("account"), strings.Join(ph, ","), botSQL,
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		s.log.Error("companion list", "err", err)
		return nil
	}
	defer rows.Close()
	var out []Pick
	for rows.Next() {
		var guid uint32
		var name string
		var level, race, class uint8
		var online int
		if err := rows.Scan(&guid, &name, &level, &race, &class, &online); err != nil {
			return out
		}
		out = append(out, pickFromRow(guid, name, level, race, class, online != 0))
	}
	return out
}

func (s *Service) snapshotSQL(ctx context.Context, accountIDs []uint32, guid, zone uint32) (Snapshot, bool) {
	if len(accountIDs) == 0 {
		return Snapshot{}, false
	}
	ph := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+8)
	args = append(args, guid)
	for i, id := range accountIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	botSQL, botArgs := s.botWhere()
	args = append(args, botArgs...)
	q := fmt.Sprintf(`
		SELECT c.`+"`guid`"+`, c.`+"`name`"+`, c.`+"`level`"+`, c.`+"`race`"+`, c.`+"`class`"+`,
		       c.`+"`online`"+`, c.`+"`map`"+`, c.`+"`zone`"+`, c.`+"`position_x`"+`, c.`+"`position_y`"+`
		FROM %s c
		INNER JOIN %s a ON a.`+"`id`"+` = c.`+"`account`"+`
		WHERE c.`+"`guid`"+` = ? AND c.`+"`account`"+` IN (%s) AND c.`+"`deleteDate`"+` IS NULL%s
		LIMIT 1`,
		s.db.QChar("characters"), s.db.QAuth("account"), strings.Join(ph, ","), botSQL,
	)
	var snap Snapshot
	var race, class uint8
	var online int
	err := s.db.SQL.QueryRowContext(ctx, q, args...).Scan(
		&snap.GUID, &snap.Name, &snap.Level, &race, &class,
		&online, &snap.MapID, &snap.ZoneID, &snap.X, &snap.Y,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			s.log.Error("companion snapshot", "err", err, "guid", guid)
		}
		return Snapshot{}, false
	}
	snap.RaceID = race
	snap.Race = wow.RaceName(race)
	snap.ClassID = class
	snap.Class = wow.ClassName(class)
	snap.Faction = wow.Faction(race)
	snap.Online = online != 0
	snap.Location = wow.Location(snap.MapID, snap.ZoneID)
	snap.Quests = s.questLog(ctx, guid)
	if zone == 0 {
		zone = snap.ZoneID
	}
	snap.RouteZone = zone
	snap.RouteName = wow.ZoneName(zone)
	snap.Zones = ZoneChoices(snap.Faction, snap.Level, zone)
	snap.Route = s.zoneRoute(ctx, guid, zone, snap.Level, race, class, snap.X, snap.Y)
	return snap, true
}

func (s *Service) questLog(ctx context.Context, guid uint32) []Quest {
	if s.db.WorldDB == "" {
		return s.questLogBare(ctx, guid)
	}
	q := fmt.Sprintf(`
		SELECT qs.`+"`quest`"+`, qs.`+"`status`"+`,
		       qs.`+"`mobcount1`"+`, qs.`+"`mobcount2`"+`, qs.`+"`mobcount3`"+`, qs.`+"`mobcount4`"+`,
		       qs.`+"`itemcount1`"+`, qs.`+"`itemcount2`"+`, qs.`+"`itemcount3`"+`,
		       qs.`+"`itemcount4`"+`, qs.`+"`itemcount5`"+`, qs.`+"`itemcount6`"+`,
		       qs.`+"`playercount`"+`,
		       q.`+"`LogTitle`"+`, q.`+"`QuestLevel`"+`,
		       q.`+"`RequiredNpcOrGoCount1`"+`, q.`+"`RequiredNpcOrGoCount2`"+`,
		       q.`+"`RequiredNpcOrGoCount3`"+`, q.`+"`RequiredNpcOrGoCount4`"+`,
		       q.`+"`RequiredItemId1`"+`, q.`+"`RequiredItemId2`"+`, q.`+"`RequiredItemId3`"+`,
		       q.`+"`RequiredItemId4`"+`, q.`+"`RequiredItemId5`"+`, q.`+"`RequiredItemId6`"+`,
		       q.`+"`RequiredItemCount1`"+`, q.`+"`RequiredItemCount2`"+`, q.`+"`RequiredItemCount3`"+`,
		       q.`+"`RequiredItemCount4`"+`, q.`+"`RequiredItemCount5`"+`, q.`+"`RequiredItemCount6`"+`,
		       q.`+"`RequiredPlayerKills`"+`,
		       q.`+"`ObjectiveText1`"+`, q.`+"`ObjectiveText2`"+`, q.`+"`ObjectiveText3`"+`, q.`+"`ObjectiveText4`"+`
		FROM %s qs
		LEFT JOIN %s q ON q.`+"`ID`"+` = qs.`+"`quest`"+`
		WHERE qs.`+"`guid`"+` = ? AND qs.`+"`status`"+` IN (?,?,?)
		ORDER BY FIELD(qs.`+"`status`"+`, ?, ?, ?), qs.`+"`quest`"+``,
		s.db.QChar("character_queststatus"), s.db.QWorld("quest_template"),
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, guid,
		questIncomplete, questComplete, questFailed,
		questIncomplete, questComplete, questFailed,
	)
	if err != nil {
		s.log.Error("companion quest log", "err", err, "guid", guid)
		return s.questLogBare(ctx, guid)
	}
	defer rows.Close()
	var out []Quest
	var itemIDs []uint32
	type row struct {
		q     Quest
		items [6]uint32
		icnt  [6]uint16
		ihave [6]uint16
	}
	var staged []row
	for rows.Next() {
		var (
			id         uint32
			status     uint8
			mobHave    [4]uint16
			itemHave   [6]uint16
			playerHave uint16
			title      sql.NullString
			level      sql.NullInt64
			npcNeed    [4]uint16
			itemID     [6]uint32
			itemNeed   [6]uint16
			playerNeed uint8
			objText    [4]sql.NullString
		)
		if err := rows.Scan(
			&id, &status,
			&mobHave[0], &mobHave[1], &mobHave[2], &mobHave[3],
			&itemHave[0], &itemHave[1], &itemHave[2], &itemHave[3], &itemHave[4], &itemHave[5],
			&playerHave,
			&title, &level,
			&npcNeed[0], &npcNeed[1], &npcNeed[2], &npcNeed[3],
			&itemID[0], &itemID[1], &itemID[2], &itemID[3], &itemID[4], &itemID[5],
			&itemNeed[0], &itemNeed[1], &itemNeed[2], &itemNeed[3], &itemNeed[4], &itemNeed[5],
			&playerNeed,
			&objText[0], &objText[1], &objText[2], &objText[3],
		); err != nil {
			return out
		}
		quest := Quest{
			ID:     id,
			Title:  QuestTitle(id, title.String),
			Status: StatusKey(status),
			Level:  int16(level.Int64),
		}
		for i := 0; i < 4; i++ {
			text := strings.TrimSpace(objText[i].String)
			need := int(npcNeed[i])
			if need == 0 && text == "" {
				continue
			}
			if text == "" {
				text = "Objective"
			}
			if need == 0 {
				need = 1
			}
			have := int(mobHave[i])
			quest.Objectives = append(quest.Objectives, Objective{
				Text: text, Have: have, Need: need, Done: have >= need,
			})
		}
		r := row{q: quest, items: itemID, icnt: itemNeed, ihave: itemHave}
		if playerNeed > 0 {
			have := int(playerHave)
			need := int(playerNeed)
			r.q.Objectives = append(r.q.Objectives, Objective{
				Text: "Player kills", Have: have, Need: need, Done: have >= need,
			})
		}
		for i := 0; i < 6; i++ {
			if itemID[i] != 0 && itemNeed[i] > 0 {
				itemIDs = append(itemIDs, itemID[i])
			}
		}
		staged = append(staged, r)
	}
	names := s.itemNames(ctx, itemIDs)
	for _, r := range staged {
		for i := 0; i < 6; i++ {
			if r.items[i] == 0 || r.icnt[i] == 0 {
				continue
			}
			text := names[r.items[i]]
			if text == "" {
				text = fmt.Sprintf("Item %d", r.items[i])
			}
			have := int(r.ihave[i])
			need := int(r.icnt[i])
			r.q.Objectives = append(r.q.Objectives, Objective{
				Text: text, Have: have, Need: need, Done: have >= need,
			})
		}
		out = append(out, r.q)
	}
	return out
}

func (s *Service) questLogBare(ctx context.Context, guid uint32) []Quest {
	q := fmt.Sprintf(`
		SELECT qs.`+"`quest`"+`, qs.`+"`status`"+`
		FROM %s qs
		WHERE qs.`+"`guid`"+` = ? AND qs.`+"`status`"+` IN (?,?,?)
		ORDER BY FIELD(qs.`+"`status`"+`, ?, ?, ?), qs.`+"`quest`"+``,
		s.db.QChar("character_queststatus"),
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, guid,
		questIncomplete, questComplete, questFailed,
		questIncomplete, questComplete, questFailed,
	)
	if err != nil {
		s.log.Error("companion quest log bare", "err", err, "guid", guid)
		return nil
	}
	defer rows.Close()
	var out []Quest
	for rows.Next() {
		var id uint32
		var status uint8
		if err := rows.Scan(&id, &status); err != nil {
			return out
		}
		out = append(out, Quest{ID: id, Title: QuestTitle(id, ""), Status: StatusKey(status)})
	}
	return out
}

func (s *Service) zoneRoute(ctx context.Context, guid, zone uint32, level, race, class uint8, x, y float32) []RouteStep {
	if s.db.WorldDB == "" || zone == 0 {
		return nil
	}
	in := s.loadZoneQuests(ctx, zone, level, race, class)
	if len(in) == 0 {
		return nil
	}
	return BuildRoute(in, s.rewardedSet(ctx, guid), s.logSet(ctx, guid), x, y, level)
}

func (s *Service) loadZoneQuests(ctx context.Context, zone uint32, level, race, class uint8) []RouteInput {
	maxMin := int(level) + 2
	mask := wow.RaceMask(race)
	classMask := wow.ClassMask(class)
	q := fmt.Sprintf(`
		SELECT q.`+"`ID`"+`, q.`+"`LogTitle`"+`, q.`+"`QuestLevel`"+`, q.`+"`MinLevel`"+`,
		       q.`+"`Flags`"+`, q.`+"`QuestInfoID`"+`, q.`+"`RewardNextQuest`"+`,
		       COALESCE(a.`+"`PrevQuestID`"+`, 0), COALESCE(a.`+"`NextQuestID`"+`, 0),
		       COALESCE(a.`+"`ExclusiveGroup`"+`, 0), COALESCE(a.`+"`BreadcrumbForQuestId`"+`, 0),
		       COALESCE(a.`+"`AllowableClasses`"+`, 0), COALESCE(a.`+"`MaxLevel`"+`, 0),
		       COALESCE(a.`+"`SpecialFlags`"+`, 0),
		       poi.`+"`x`"+`, poi.`+"`y`"+`
		FROM %s q
		LEFT JOIN %s a ON a.`+"`ID`"+` = q.`+"`ID`"+`
		LEFT JOIN (
			SELECT `+"`QuestID`"+`, AVG(`+"`X`"+`) AS x, AVG(`+"`Y`"+`) AS y
			FROM %s
			GROUP BY `+"`QuestID`"+`
		) poi ON poi.`+"`QuestID`"+` = q.`+"`ID`"+`
		WHERE q.`+"`QuestSortID`"+` = ?
		  AND q.`+"`MinLevel`"+` <= ?
		  AND (q.`+"`AllowableRaces`"+` = 0 OR (q.`+"`AllowableRaces`"+` & ?) != 0)
		  AND q.`+"`LogTitle`"+` IS NOT NULL AND q.`+"`LogTitle`"+` != ''`,
		s.db.QWorld("quest_template"),
		s.db.QWorld("quest_template_addon"),
		s.db.QWorld("quest_poi_points"),
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, zone, maxMin, mask)
	if err != nil {
		s.log.Error("companion zone route", "err", err, "zone", zone)
		return s.loadZoneQuestsPlain(ctx, zone, level, race)
	}
	defer rows.Close()
	var out []RouteInput
	for rows.Next() {
		var (
			id         uint32
			title      sql.NullString
			qlevel     int16
			minLevel   uint8
			flags      uint32
			info       uint16
			rewardNext uint32
			prev       int32
			nextID     uint32
			excl       int32
			bread      uint32
			allowClass uint32
			maxLevel   uint8
			special    uint32
			px, py     sql.NullFloat64
		)
		if err := rows.Scan(
			&id, &title, &qlevel, &minLevel, &flags, &info, &rewardNext,
			&prev, &nextID, &excl, &bread, &allowClass, &maxLevel, &special, &px, &py,
		); err != nil {
			return out
		}
		if skipForRoute(flags, info, special, maxLevel, level, allowClass, classMask) {
			continue
		}
		if nextID == 0 {
			nextID = rewardNext
		}
		n := RouteInput{
			ID: id, Title: title.String, Level: qlevel, MinLevel: minLevel,
			PrevQuestID: prev, NextQuestID: nextID, ExclusiveGroup: excl, BreadcrumbFor: bread,
		}
		if px.Valid && py.Valid {
			n.X, n.Y, n.HasPOI = float32(px.Float64), float32(py.Float64), true
		}
		out = append(out, n)
	}
	return out
}

func (s *Service) loadZoneQuestsPlain(ctx context.Context, zone uint32, level, race uint8) []RouteInput {
	maxMin := int(level) + 2
	mask := wow.RaceMask(race)
	q := fmt.Sprintf(`
		SELECT q.`+"`ID`"+`, q.`+"`LogTitle`"+`, q.`+"`QuestLevel`"+`, q.`+"`MinLevel`"+`,
		       q.`+"`Flags`"+`, q.`+"`QuestInfoID`"+`, q.`+"`RewardNextQuest`"+`
		FROM %s q
		WHERE q.`+"`QuestSortID`"+` = ?
		  AND q.`+"`MinLevel`"+` <= ?
		  AND (q.`+"`AllowableRaces`"+` = 0 OR (q.`+"`AllowableRaces`"+` & ?) != 0)
		  AND q.`+"`LogTitle`"+` IS NOT NULL AND q.`+"`LogTitle`"+` != ''`,
		s.db.QWorld("quest_template"),
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, zone, maxMin, mask)
	if err != nil {
		s.log.Error("companion zone route plain", "err", err, "zone", zone)
		return nil
	}
	defer rows.Close()
	var out []RouteInput
	for rows.Next() {
		var id uint32
		var title sql.NullString
		var qlevel int16
		var minLevel uint8
		var flags uint32
		var info uint16
		var rewardNext uint32
		if err := rows.Scan(&id, &title, &qlevel, &minLevel, &flags, &info, &rewardNext); err != nil {
			return out
		}
		if skipForRoute(flags, info, 0, 0, level, 0, 0) {
			continue
		}
		out = append(out, RouteInput{
			ID: id, Title: title.String, Level: qlevel, MinLevel: minLevel, NextQuestID: rewardNext,
		})
	}
	return out
}

func (s *Service) rewardedSet(ctx context.Context, guid uint32) map[uint32]struct{} {
	out := map[uint32]struct{}{}
	q := fmt.Sprintf(`SELECT `+"`quest`"+` FROM %s WHERE `+"`guid`"+` = ?`, s.db.QChar("character_queststatus_rewarded"))
	rows, err := s.db.SQL.QueryContext(ctx, q, guid)
	if err != nil {
		s.log.Error("companion rewarded", "err", err, "guid", guid)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out[id] = struct{}{}
	}
	return out
}

func (s *Service) logSet(ctx context.Context, guid uint32) map[uint32]string {
	out := map[uint32]string{}
	q := fmt.Sprintf(`
		SELECT `+"`quest`"+`, `+"`status`"+` FROM %s
		WHERE `+"`guid`"+` = ? AND `+"`status`"+` IN (?,?,?)`,
		s.db.QChar("character_queststatus"),
	)
	rows, err := s.db.SQL.QueryContext(ctx, q, guid, questIncomplete, questComplete, questFailed)
	if err != nil {
		s.log.Error("companion log set", "err", err, "guid", guid)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id uint32
		var status uint8
		if err := rows.Scan(&id, &status); err != nil {
			return out
		}
		out[id] = StatusKey(status)
	}
	return out
}

func (s *Service) itemNames(ctx context.Context, ids []uint32) map[uint32]string {
	out := map[uint32]string{}
	if len(ids) == 0 || s.db.WorldDB == "" {
		return out
	}
	seen := map[uint32]struct{}{}
	uniq := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out
	}
	ph := make([]string, len(uniq))
	args := make([]any, len(uniq))
	for i, id := range uniq {
		ph[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT `+"`entry`"+`, `+"`name`"+` FROM %s WHERE `+"`entry`"+` IN (%s)`,
		s.db.QWorld("item_template"), strings.Join(ph, ","))
	rows, err := s.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		s.log.Error("companion item names", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id uint32
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return out
		}
		out[id] = name
	}
	return out
}
