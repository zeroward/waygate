package armory

import (
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/wow"
)

type demoChar struct {
	name, race, class, gender, guild string
	raceID, classID, genderID        uint8
	level                            uint8
	money, played, logout            uint32
	mapID, zone                      uint32
	online                           bool
	honor, arena, hk                 uint32
}

func demoRoster() []demoChar {
	return []demoChar{
		{name: "Frostwarden", race: "Human", class: "Paladin", gender: "Male", guild: "Ashen Verdict",
			raceID: 1, classID: 2, genderID: 0, level: 80, money: 12_345_678, played: 980_000,
			mapID: 571, zone: 210, online: true, honor: 85000, arena: 1850, hk: 3100},
		{name: "Icemourn", race: "Orc", class: "Death Knight", gender: "Female", guild: "Warsong Offensive",
			raceID: 2, classID: 6, genderID: 1, level: 80, money: 8_800_000, played: 1_200_000,
			mapID: 631, zone: 4812, online: true, honor: 72000, arena: 1420, hk: 4200},
		{name: "NorthrendScout", race: "Night Elf", class: "Hunter", gender: "Female", guild: "",
			raceID: 4, classID: 3, genderID: 1, level: 77, money: 881200, played: 410_000,
			mapID: 571, zone: 3537, online: false, logout: 1_704_067_200, honor: 14000, arena: 1420, hk: 800},
		{name: "Plaguebloom", race: "Undead", class: "Warlock", gender: "Male", guild: "Hand of Vengeance",
			raceID: 5, classID: 9, genderID: 0, level: 80, money: 2_100_000, played: 810_000,
			mapID: 0, zone: 139, online: true, honor: 22000, arena: 0, hk: 1100},
		{name: "Stormpike", race: "Dwarf", class: "Warrior", gender: "Male", guild: "",
			raceID: 3, classID: 1, genderID: 0, level: 68, money: 55_000, played: 200_000,
			mapID: 571, zone: 65, online: true, honor: 14000, arena: 0, hk: 400},
		{name: "Sunreaver", race: "Blood Elf", class: "Mage", gender: "Female", guild: "Sunreavers",
			raceID: 10, classID: 8, genderID: 1, level: 80, money: 250_000, played: 640_000,
			mapID: 571, zone: 4395, online: true, honor: 33000, arena: 1850, hk: 1800},
		{name: "Anchorite", race: "Draenei", class: "Priest", gender: "Male", guild: "Exodar",
			raceID: 11, classID: 5, genderID: 0, level: 71, money: 120_000, played: 280_000,
			mapID: 571, zone: 394, online: true, honor: 9000, arena: 1600, hk: 220},
		{name: "Grimtotem", race: "Tauren", class: "Druid", gender: "Male", guild: "",
			raceID: 6, classID: 11, genderID: 0, level: 80, money: 640_000, played: 500_000,
			mapID: 571, zone: 3711, online: true, honor: 18000, arena: 0, hk: 900},
	}
}

func demoSearch(q string) []SearchHit {
	ql := strings.ToLower(q)
	var out []SearchHit
	for _, c := range demoRoster() {
		if !strings.HasPrefix(strings.ToLower(c.name), ql) {
			continue
		}
		out = append(out, SearchHit{
			Name: c.name, Level: c.level, Race: c.race, Class: c.class,
			ClassID: c.classID, Faction: wow.Faction(c.raceID),
		})
	}
	return out
}

func demoInspect(name string) (Profile, bool) {
	var c demoChar
	found := false
	for _, d := range demoRoster() {
		if strings.EqualFold(d.name, name) {
			c = d
			found = true
			break
		}
	}
	if !found {
		return Profile{}, false
	}
	p := Profile{
		GUID:           1,
		Name:           c.name,
		Level:          c.level,
		Race:           c.race,
		RaceID:         c.raceID,
		Class:          c.class,
		ClassID:        c.classID,
		Gender:         c.gender,
		Faction:        wow.Faction(c.raceID),
		Guild:          c.guild,
		Location:       wow.Location(c.mapID, c.zone),
		Played:         wow.Playtime(c.played),
		Gold:           wow.Gold(c.money),
		Online:         c.online,
		Logout:         wow.LogoutLabel(c.online, c.logout),
		Honor:          c.honor,
		ArenaPoints:    c.arena,
		HonorableKills: c.hk,
		ActiveSpec:     0,
		Gear:           emptyGear(),
	}
	if strings.EqualFold(c.name, "Frostwarden") {
		p.GUID = 1
		p.Gear = frostwardenGear()
		p.Specs = frostwardenSpecs()
		p.Achievements = frostwardenAchievements()
		p.Arena = []ArenaTeam{
			{Name: "Frozen Steel", Bracket: "2v2", Type: 2, Rating: 1840, SeasonGames: 80, SeasonWins: 48, PersonalRating: 1822},
			{Name: "Northrend Knights", Bracket: "3v3", Type: 3, Rating: 1650, SeasonGames: 40, SeasonWins: 22, PersonalRating: 1610},
		}
	}
	return p, true
}

func frostwardenGear() []GearItem {
	g := emptyGear()
	put := func(slot uint8, entry uint32, name string, q uint8) {
		g[slot] = GearItem{Slot: slot, SlotName: SlotName(slot), Entry: entry, Name: name, Quality: q}
	}
	put(0, 51272, "Lightsworn Headpiece", 4)
	put(1, 50647, "Ahn'kahar Onyx Neckguard", 4)
	put(2, 51269, "Lightsworn Shoulderguards", 4)
	put(3, 4330, "Stylish Red Shirt", 1)
	put(4, 51265, "Lightsworn Chestguard", 4)
	put(5, 50620, "Coldwraith Links", 4)
	put(6, 51267, "Lightsworn Legguards", 4)
	put(7, 54578, "Apocalypse's Advance", 4)
	put(8, 54580, "Umbrage Armbands", 4)
	put(9, 51270, "Lightsworn Gloves", 4)
	put(10, 50604, "Band of the Bone Colossus", 4)
	put(11, 50614, "Loop of the Endless Labyrinth", 4)
	put(12, 54590, "Sharpened Twilight Scale", 4)
	put(13, 50351, "Tiny Abomination in a Jar", 4)
	put(14, 50628, "Frostbinder's Shredded Cape", 4)
	put(15, 50730, "Glorenzelg, High-Blade of the Silver Hand", 4)
	put(16, 50729, "Icecrown Glacial Wall", 4)
	put(17, 50461, "Libram of Eternal Spring", 4)
	put(18, 5976, "Guild Tabard", 1)
	return g
}

func frostwardenSpecs() []TalentSpec {
	tal := func(id uint32) Talent { return Talent{Spell: id, Name: TalentName(id)} }
	gly := func(id uint32, major bool, slot int) Glyph {
		return Glyph{ID: id, Name: GlyphName(id), Major: major, Slot: slot}
	}
	return []TalentSpec{
		{
			Index: 0, Label: "Primary", Active: true,
			Talents: []Talent{
				tal(35395), tal(53385), tal(20375), tal(20066), tal(20113),
				tal(20059), tal(31883), tal(53488), tal(53382), tal(53648),
			},
			Glyphs: []Glyph{
				gly(183, true, 1), gly(200, true, 2), gly(193, true, 3),
				gly(188, false, 4), gly(189, false, 5), gly(191, false, 6),
			},
		},
		{
			Index: 1, Label: "Secondary", Active: false,
			Talents: []Talent{
				tal(20473), tal(53563), tal(31842), tal(20215), tal(31841),
				tal(53576), tal(20237), tal(54154), tal(31821),
			},
			Glyphs: []Glyph{
				gly(186, true, 1), gly(195, true, 2), gly(187, true, 3),
				gly(188, false, 4), gly(189, false, 5),
			},
		},
	}
}

func frostwardenAchievements() []Achievement {
	ids := []uint32{13, 576, 1658, 4530, 4532, 4584, 4602, 239, 870, 46, 41, 478}
	out := make([]Achievement, 0, len(ids))
	for i, id := range ids {
		out = append(out, Achievement{
			ID:   id,
			Name: AchievementName(id),
			Date: time.Unix(1_704_067_200+int64(i)*86400, 0).UTC().Format("2006-01-02"),
		})
	}
	return out
}
