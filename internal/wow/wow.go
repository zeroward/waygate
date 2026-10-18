package wow

import (
	"fmt"
	"strings"
	"time"
)

func RaceName(id uint8) string {
	switch id {
	case 1:
		return "Human"
	case 2:
		return "Orc"
	case 3:
		return "Dwarf"
	case 4:
		return "Night Elf"
	case 5:
		return "Undead"
	case 6:
		return "Tauren"
	case 7:
		return "Gnome"
	case 8:
		return "Troll"
	case 10:
		return "Blood Elf"
	case 11:
		return "Draenei"
	default:
		return fmt.Sprintf("Race %d", id)
	}
}

func ClassName(id uint8) string {
	switch id {
	case 1:
		return "Warrior"
	case 2:
		return "Paladin"
	case 3:
		return "Hunter"
	case 4:
		return "Rogue"
	case 5:
		return "Priest"
	case 6:
		return "Death Knight"
	case 7:
		return "Shaman"
	case 8:
		return "Mage"
	case 9:
		return "Warlock"
	case 11:
		return "Druid"
	default:
		return fmt.Sprintf("Class %d", id)
	}
}

func ClassSlug(id uint8) string {
	return strings.ToLower(strings.ReplaceAll(ClassName(id), " ", "-"))
}

func Faction(race uint8) string {
	switch race {
	case 1, 3, 4, 7, 11:
		return "Alliance"
	case 2, 5, 6, 8, 10:
		return "Horde"
	default:
		return "Unknown"
	}
}

// RaceMask is the AllowableRaces bit for a chr_races id (Human=1 → bit 0).
func RaceMask(race uint8) uint32 {
	if race == 0 || race > 31 {
		return 0
	}
	return 1 << (race - 1)
}

// ClassMask is the AllowableClasses bit for a chr_classes id (Warrior=1 → bit 0).
func ClassMask(class uint8) uint32 {
	if class == 0 || class > 31 {
		return 0
	}
	return 1 << (class - 1)
}

func ExpansionName(v uint8) string {
	switch v {
	case 0:
		return "Classic"
	case 1:
		return "The Burning Crusade"
	default:
		return "Wrath of the Lich King"
	}
}

func Playtime(seconds uint32) string {
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func ZoneName(id uint32) string {
	if n, ok := zones[id]; ok {
		return n
	}
	return fmt.Sprintf("Unknown zone (%d)", id)
}

var zones = map[uint32]string{
	1: "Dun Morogh", 3: "Badlands", 4: "Blasted Lands", 8: "Swamp of Sorrows",
	10: "Duskwood", 11: "Wetlands", 12: "Elwynn Forest", 14: "Durotar",
	15: "Dustwallow Marsh", 17: "Northern Barrens", 28: "Western Plaguelands",
	33: "Northern Stranglethorn", 36: "Alterac Mountains", 38: "Loch Modan",
	40: "Westfall", 41: "Deadwind Pass", 44: "Redridge Mountains",
	45: "Arathi Highlands", 46: "Burning Steppes", 47: "The Hinterlands",
	51: "Searing Gorge", 65: "Dragonblight", 66: "Zul'Drak", 67: "The Storm Peaks",
	85: "Tirisfal Glades", 130: "Silverpine Forest", 139: "Eastern Plaguelands",
	141: "Teldrassil", 148: "Darkshore", 210: "Icecrown", 215: "Mulgore",
	267: "Hillsbrad Foothills", 331: "Ashenvale", 357: "Feralas", 361: "Felwood",
	394: "Grizzly Hills", 400: "Thousand Needles", 405: "Desolace",
	406: "Stonetalon Mountains", 440: "Tanaris", 490: "Un'Goro Crater",
	493: "Moonglade", 495: "Howling Fjord", 618: "Winterspring",
	1377: "Silithus", 1497: "Undercity", 1519: "Stormwind City", 1537: "Ironforge",
	1637: "Orgrimmar", 1638: "Thunder Bluff", 1657: "Darnassus",
	2817: "Crystalsong Forest", 3430: "Eversong Woods", 3433: "Ghostlands",
	3483: "Hellfire Peninsula", 3487: "Silvermoon City", 3518: "Nagrand",
	3519: "Terokkar Forest", 3520: "Shadowmoon Valley", 3521: "Zangarmarsh",
	3522: "Blade's Edge Mountains", 3523: "Netherstorm", 3524: "Azuremyst Isle",
	3525: "Bloodmyst Isle", 3537: "Borean Tundra", 3557: "The Exodar",
	3703: "Shattrath City", 3711: "Sholazar Basin", 4080: "Isle of Quel'Danas",
	4197: "Wintergrasp", 4298: "Plaguelands: The Scarlet Enclave",
	4395: "Dalaran", 4742: "Hrothgar's Landing",
	206: "Utgarde Keep", 209: "Shadowfang Keep", 491: "Razorfen Kraul",
	717: "The Stockade", 718: "Wailing Caverns", 719: "Blackfathom Deeps",
	721: "Gnomeregan", 722: "Razorfen Downs", 796: "Scarlet Monastery",
	1176: "Zul'Farrak", 1337: "Uldaman", 1477: "Sunken Temple",
	1581: "The Deadmines", 1583: "Blackrock Spire", 1584: "Blackrock Depths",
	2017: "Stratholme", 2057: "Scholomance", 2100: "Maraudon",
	2366: "The Black Morass", 2367: "Old Hillsbrad Foothills",
	2437: "Ragefire Chasm", 2557: "Dire Maul",
	3562: "Hellfire Ramparts", 3713: "The Blood Furnace", 3714: "The Shattered Halls",
	3715: "The Steamvault", 3716: "The Underbog", 3717: "The Slave Pens",
	3789: "Shadow Labyrinth", 3790: "Auchenai Crypts", 3791: "Sethekk Halls",
	3792: "Mana-Tombs", 3842: "Tempest Keep", 3845: "Magtheridon's Lair",
	3606: "Hyjal Summit", 3607: "Serpentshrine Cavern", 3618: "Gruul's Lair",
	3805: "Zul'Aman", 3836: "Magisters' Terrace", 3959: "Black Temple",
	4075: "Sunwell Plateau", 3456: "Naxxramas", 3457: "Karazhan",
	4493: "The Obsidian Sanctum", 4500: "The Eye of Eternity",
	4277: "Azjol-Nerub", 4494: "Ahn'kahet: The Old Kingdom",
	4196: "Drak'Tharon Keep", 4416: "Gundrak", 4415: "The Violet Hold",
	4264: "Halls of Stone", 4272: "Halls of Lightning", 4265: "The Nexus",
	4228: "The Oculus", 4100: "The Culling of Stratholme",
	4809: "The Forge of Souls", 4813: "Pit of Saron", 4820: "Halls of Reflection",
	4812: "Icecrown Citadel", 2159: "Onyxia's Lair", 4603: "Vault of Archavon",
	4273: "Ulduar", 4722: "Trial of the Crusader", 4987: "The Ruby Sanctum",
	1517: "Uldaman", 2558: "Deadwind Pass",
}
