package armory

const (
	repVisible   = 0x1
	repHidden    = 0x4
	repInvisible = 0x8
)

type factionMeta struct {
	Name  string
	Group string
}

var factions = map[uint32]factionMeta{
	72:   {Name: "Stormwind", Group: "Alliance"},
	47:   {Name: "Ironforge", Group: "Alliance"},
	69:   {Name: "Darnassus", Group: "Alliance"},
	54:   {Name: "Gnomeregan Exiles", Group: "Alliance"},
	930:  {Name: "Exodar", Group: "Alliance"},
	890:  {Name: "Silverwing Sentinels", Group: "Alliance"},
	509:  {Name: "The League of Arathor", Group: "Alliance"},
	730:  {Name: "Stormpike Guard", Group: "Alliance"},
	946:  {Name: "Honor Hold", Group: "Alliance"},
	978:  {Name: "Kurenai", Group: "Alliance"},
	1037: {Name: "Alliance Vanguard", Group: "Alliance"},
	1050: {Name: "Valiance Expedition", Group: "Alliance"},
	1068: {Name: "Explorers' League", Group: "Alliance"},
	1094: {Name: "The Silver Covenant", Group: "Alliance"},
	1126: {Name: "The Frostborn", Group: "Alliance"},
	76:   {Name: "Orgrimmar", Group: "Horde"},
	530:  {Name: "Darkspear Trolls", Group: "Horde"},
	81:   {Name: "Thunder Bluff", Group: "Horde"},
	68:   {Name: "Undercity", Group: "Horde"},
	911:  {Name: "Silvermoon City", Group: "Horde"},
	889:  {Name: "Warsong Outriders", Group: "Horde"},
	510:  {Name: "The Defilers", Group: "Horde"},
	729:  {Name: "Frostwolf Clan", Group: "Horde"},
	947:  {Name: "Thrallmar", Group: "Horde"},
	941:  {Name: "The Mag'har", Group: "Horde"},
	1052: {Name: "Horde Expedition", Group: "Horde"},
	1067: {Name: "The Hand of Vengeance", Group: "Horde"},
	1085: {Name: "Warsong Offensive", Group: "Horde"},
	1124: {Name: "The Sunreavers", Group: "Horde"},
	1064: {Name: "The Taunka", Group: "Horde"},
	1106: {Name: "Argent Crusade", Group: "Northrend"},
	1098: {Name: "Knights of the Ebon Blade", Group: "Northrend"},
	1090: {Name: "Kirin Tor", Group: "Northrend"},
	1091: {Name: "The Wyrmrest Accord", Group: "Northrend"},
	1119: {Name: "The Sons of Hodir", Group: "Northrend"},
	1156: {Name: "The Ashen Verdict", Group: "Northrend"},
	1073: {Name: "The Kalu'ak", Group: "Northrend"},
	1104: {Name: "Frenzyheart Tribe", Group: "Northrend"},
	1105: {Name: "The Oracles", Group: "Northrend"},
	529:  {Name: "Argent Dawn", Group: "Other"},
	609:  {Name: "Cenarion Circle", Group: "Other"},
	576:  {Name: "Timbermaw Hold", Group: "Other"},
	59:   {Name: "Thorium Brotherhood", Group: "Other"},
	270:  {Name: "Zandalar Tribe", Group: "Other"},
	910:  {Name: "Brood of Nozdormu", Group: "Other"},
	749:  {Name: "Hydraxian Waterlords", Group: "Other"},
	909:  {Name: "Darkmoon Faire", Group: "Other"},
	967:  {Name: "The Violet Eye", Group: "Other"},
	989:  {Name: "Keepers of Time", Group: "Other"},
	935:  {Name: "The Sha'tar", Group: "Other"},
	942:  {Name: "Cenarion Expedition", Group: "Other"},
	1012: {Name: "Ashtongue Deathsworn", Group: "Other"},
	933:  {Name: "The Consortium", Group: "Other"},
	970:  {Name: "Sporeggar", Group: "Other"},
	1011: {Name: "Lower City", Group: "Other"},
	1031: {Name: "Sha'tari Skyguard", Group: "Other"},
	1038: {Name: "Ogri'la", Group: "Other"},
	932:  {Name: "The Aldor", Group: "Other"},
	934:  {Name: "The Scryers", Group: "Other"},
	1077: {Name: "Shattered Sun Offensive", Group: "Other"},
	21:   {Name: "Booty Bay", Group: "Other"},
	369:  {Name: "Gadgetzan", Group: "Other"},
	470:  {Name: "Ratchet", Group: "Other"},
	577:  {Name: "Everlook", Group: "Other"},
	87:   {Name: "Bloodsail Buccaneers", Group: "Other"},
}

func factionIDs() []uint32 {
	out := make([]uint32, 0, len(factions))
	for id := range factions {
		out = append(out, id)
	}
	return out
}

func StandingRank(standing int32) string {
	switch {
	case standing >= 42000:
		return "Exalted"
	case standing >= 21000:
		return "Revered"
	case standing >= 9000:
		return "Honored"
	case standing >= 3000:
		return "Friendly"
	case standing >= 0:
		return "Neutral"
	case standing >= -3000:
		return "Unfriendly"
	case standing >= -6000:
		return "Hostile"
	default:
		return "Hated"
	}
}

func showReputation(standing int32, flags uint32) bool {
	if flags&repHidden != 0 || flags&repInvisible != 0 {
		return false
	}
	return standing != 0 || flags&repVisible != 0
}
