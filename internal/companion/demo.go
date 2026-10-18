package companion

import "github.com/zeroward/waygate/internal/wow"

func demoPicks() []Pick {
	return []Pick{
		pickFromRow(1, "Frostwarden", 80, 1, 2, true),
		pickFromRow(2, "NorthrendScout", 77, 4, 3, false),
	}
}

func demoSnapshot(guid, zone uint32) (Snapshot, bool) {
	switch guid {
	case 1:
		if zone == 0 {
			zone = 210
		}
		inLog := map[uint32]string{13300: "incomplete", 13104: "incomplete"}
		rewarded := map[uint32]struct{}{13157: {}, 13221: {}}
		snap := Snapshot{
			GUID:     1,
			Name:     "Frostwarden",
			Level:    80,
			Race:     "Human",
			RaceID:   1,
			Class:    "Paladin",
			ClassID:  2,
			Faction:  "Alliance",
			Online:   true,
			Location: wow.Location(571, 210),
			MapID:    571,
			ZoneID:   210,
			X:        6415.2,
			Y:        455.8,
			Quests: []Quest{
				{
					ID: 13300, Title: "Slaves to Saronite", Status: "incomplete", Level: 80,
					Objectives: []Objective{{Text: "Saronite Mine Slaves rescued", Have: 6, Need: 10}},
				},
				{
					ID: 13104, Title: "Once More Unto The Breach, Hero", Status: "incomplete", Level: 78,
					Objectives: []Objective{{Text: "Speak with the Ebon Watcher", Have: 0, Need: 1}},
				},
				{
					ID: 13157, Title: "The Crusaders' Pinnacle", Status: "complete", Level: 78,
				},
				{
					ID: 13221, Title: "I'm Not Dead Yet!", Status: "complete", Level: 80,
				},
			},
			RouteZone: zone,
			RouteName: wow.ZoneName(zone),
			Zones:     ZoneChoices("Alliance", 80, zone),
			Route:     BuildRoute(demoZoneQuests(zone), rewarded, inLog, 6415.2, 455.8, 80),
		}
		return snap, true
	case 2:
		if zone == 0 {
			zone = 3537
		}
		inLog := map[uint32]string{11789: "incomplete"}
		rewarded := map[uint32]struct{}{11672: {}}
		snap := Snapshot{
			GUID:     2,
			Name:     "NorthrendScout",
			Level:    77,
			Race:     "Night Elf",
			RaceID:   4,
			Class:    "Hunter",
			ClassID:  3,
			Faction:  "Alliance",
			Online:   false,
			Location: wow.Location(571, 3537),
			MapID:    571,
			ZoneID:   3537,
			X:        2231.4,
			Y:        5134.1,
			Quests: []Quest{
				{ID: 11672, Title: "Enlistment Day", Status: "complete", Level: 71},
				{
					ID: 11789, Title: "A Soldier in Need", Status: "incomplete", Level: 71,
					Objectives: []Objective{{Text: "Cultist Shroud", Have: 0, Need: 1}},
				},
			},
			RouteZone: zone,
			RouteName: wow.ZoneName(zone),
			Zones:     ZoneChoices("Alliance", 77, zone),
			Route:     BuildRoute(demoZoneQuests(zone), rewarded, inLog, 2231.4, 5134.1, 77),
		}
		return snap, true
	default:
		return Snapshot{}, false
	}
}

func demoZoneQuests(zone uint32) []RouteInput {
	switch zone {
	case 210:
		return []RouteInput{
			{ID: 13036, Title: "Honor Above All Else", Level: 77, MinLevel: 77, NextQuestID: 13008, X: 6420, Y: 450, HasPOI: true},
			{ID: 13008, Title: "Scourge Tactics", Level: 77, MinLevel: 77, PrevQuestID: 13036, NextQuestID: 13039, X: 6430, Y: 460, HasPOI: true},
			{ID: 13039, Title: "Destroying the Altars", Level: 78, MinLevel: 77, PrevQuestID: 13008, X: 6440, Y: 470, HasPOI: true},
			{ID: 13104, Title: "Once More Unto The Breach, Hero", Level: 78, MinLevel: 78, X: 6500, Y: 500, HasPOI: true},
			{ID: 13300, Title: "Slaves to Saronite", Level: 80, MinLevel: 77, X: 6410, Y: 440, HasPOI: true},
			{ID: 13332, Title: "Raise the Barricades", Level: 80, MinLevel: 77, X: 7000, Y: 800, HasPOI: true},
			{ID: 13348, Title: "Futility", Level: 80, MinLevel: 77, PrevQuestID: 13332, X: 7050, Y: 820, HasPOI: true},
			{ID: 13140, Title: "The Runesmiths of Malykriss", Level: 80, MinLevel: 77, X: 7200, Y: 900, HasPOI: true},
			{ID: 13157, Title: "The Crusaders' Pinnacle", Level: 78, MinLevel: 77},
		}
	case 3537:
		return []RouteInput{
			{ID: 11672, Title: "Enlistment Day", Level: 71, MinLevel: 68, NextQuestID: 11789, X: 2230, Y: 5130, HasPOI: true},
			{ID: 11789, Title: "A Soldier in Need", Level: 71, MinLevel: 68, PrevQuestID: 11672, X: 2240, Y: 5140, HasPOI: true},
			{ID: 11612, Title: "Reclaiming the Quarry", Level: 72, MinLevel: 68, X: 3500, Y: 5200, HasPOI: true},
			{ID: 11613, Title: "Karuk's Oath", Level: 72, MinLevel: 68, PrevQuestID: 11612, X: 3520, Y: 5210, HasPOI: true},
		}
	default:
		return nil
	}
}
