package wow

import (
	"fmt"
	"strings"
	"time"
)

func MapName(id uint32) string {
	if n, ok := maps[id]; ok {
		return n
	}
	return fmt.Sprintf("Unknown map (%d)", id)
}

// Location prefers zone, then map. When both are known and differ, "Icecrown · Northrend".
func Location(mapID, zoneID uint32) string {
	z := ZoneName(zoneID)
	m := MapName(mapID)
	zKnown := !strings.HasPrefix(z, "Unknown")
	mKnown := !strings.HasPrefix(m, "Unknown")
	switch {
	case zKnown && mKnown && z != m:
		return z + " · " + m
	case zKnown:
		return z
	case mKnown:
		return m
	default:
		return z
	}
}

func LogoutLabel(online bool, unix uint32) string {
	if online {
		return "Online"
	}
	if unix == 0 {
		return "—"
	}
	return time.Unix(int64(unix), 0).UTC().Format("2 Jan 2006 15:04")
}

var maps = map[uint32]string{
	0: "Eastern Kingdoms", 1: "Kalimdor", 13: "Testing",
	30: "Alterac Valley", 33: "Shadowfang Keep", 34: "The Stockade",
	36: "The Deadmines", 43: "Wailing Caverns", 47: "Razorfen Kraul",
	48: "Blackfathom Deeps", 70: "Uldaman", 90: "Gnomeregan",
	109: "The Temple of Atal'Hakkar", 129: "Razorfen Downs",
	169: "Emerald Dream", 189: "Scarlet Monastery", 209: "Zul'Farrak",
	229: "Blackrock Spire", 230: "Blackrock Depths", 249: "Onyxia's Lair",
	269: "The Black Morass", 289: "Scholomance", 309: "Zul'Gurub",
	329: "Stratholme", 349: "Maraudon", 369: "Deeprun Tram",
	389: "Ragefire Chasm", 409: "Molten Core", 429: "Dire Maul",
	469: "Blackwing Lair", 489: "Warsong Gulch", 509: "Ruins of Ahn'Qiraj",
	529: "Arathi Basin", 530: "Outland", 531: "Ahn'Qiraj Temple",
	532: "Karazhan", 533: "Naxxramas", 534: "The Battle for Mount Hyjal",
	540: "The Shattered Halls", 542: "The Blood Furnace", 543: "Hellfire Ramparts",
	544: "Magtheridon's Lair", 545: "The Steamvault", 546: "The Underbog",
	547: "The Slave Pens", 548: "Serpentshrine Cavern", 550: "Tempest Keep",
	552: "The Arcatraz", 553: "The Botanica", 554: "The Mechanar",
	555: "Shadow Labyrinth", 556: "Sethekk Halls", 557: "Mana-Tombs",
	558: "Auchenai Crypts", 559: "Nagrand Arena", 560: "Old Hillsbrad Foothills",
	562: "Blade's Edge Arena", 564: "Black Temple", 565: "Gruul's Lair",
	566: "Eye of the Storm", 568: "Zul'Aman", 571: "Northrend",
	572: "Ruins of Lordaeron", 574: "Utgarde Keep", 575: "Utgarde Pinnacle",
	576: "The Nexus", 578: "The Oculus", 580: "Sunwell Plateau",
	585: "Magisters' Terrace", 595: "The Culling of Stratholme",
	599: "Halls of Stone", 600: "Drak'Tharon Keep", 601: "Azjol-Nerub",
	602: "Halls of Lightning", 603: "Ulduar", 604: "Gundrak",
	607: "Strand of the Ancients", 608: "The Violet Hold",
	615: "The Obsidian Sanctum", 616: "The Eye of Eternity",
	617: "Dalaran Sewers", 618: "The Ring of Valor", 619: "Ahn'kahet: The Old Kingdom",
	624: "Vault of Archavon", 628: "Isle of Conquest", 631: "Icecrown Citadel",
	632: "The Forge of Souls", 649: "Trial of the Crusader",
	650: "Trial of the Champion", 658: "Pit of Saron",
	668: "Halls of Reflection", 724: "The Ruby Sanctum",
}
