package armory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/wow"
)

const searchLimit = 25

type Service struct {
	cfg config.Config
	db  *db.DB

	mu    sync.Mutex
	cache map[string]cacheEnt
}

type cacheEnt struct {
	p     Profile
	until time.Time
	ok    bool
}

func New(cfg config.Config, database *db.DB) *Service {
	return &Service{cfg: cfg, db: database, cache: map[string]cacheEnt{}}
}

type SearchHit struct {
	Name    string
	Level   uint8
	Race    string
	Class   string
	ClassID uint8
	Faction string
}

type Profile struct {
	GUID           uint32
	Name           string
	Level          uint8
	Race           string
	RaceID         uint8
	Class          string
	ClassID        uint8
	Gender         string
	Faction        string
	Guild          string
	Location       string
	Played         string
	Gold           string
	Online         bool
	Logout         string
	Honor          uint32
	ArenaPoints    uint32
	HonorableKills uint32
	ActiveSpec     int
	Gear           []GearItem
	Specs          []TalentSpec
	Achievements   []Achievement
	Arena          []ArenaTeam
}

type GearItem struct {
	Slot     uint8
	SlotName string
	Entry    uint32
	Name     string
	Quality  uint8
	Empty    bool
}

func (g GearItem) Wowhead() string {
	if g.Empty || g.Entry == 0 {
		return ""
	}
	return fmt.Sprintf("https://www.wowhead.com/wotlk/item=%d", g.Entry)
}

func (g GearItem) QualityClass() string {
	return fmt.Sprintf("q%d", g.Quality)
}

type TalentSpec struct {
	Index   int
	Label   string
	Active  bool
	Talents []Talent
	Glyphs  []Glyph
}

type Talent struct {
	Spell uint32
	Name  string
}

type Glyph struct {
	ID    uint32
	Name  string
	Major bool
	Slot  int
}

type Achievement struct {
	ID   uint32
	Name string
	Date string
}

type ArenaTeam struct {
	Name           string
	Bracket        string
	Type           uint8
	Rating         uint32
	SeasonGames    uint32
	SeasonWins     uint32
	PersonalRating uint32
}

func ValidName(s string) bool {
	s = strings.TrimSpace(s)
	n := 0
	for _, r := range s {
		if !unicode.IsLetter(r) || r > unicode.MaxASCII {
			return false
		}
		n++
	}
	return n >= 2 && n <= 12
}

func (s *Service) Search(ctx context.Context, q string) []SearchHit {
	q = strings.TrimSpace(q)
	if q == "" || !ValidName(q) && !validPrefix(q) {
		return nil
	}
	if s.cfg.DemoMode || s.db == nil {
		return demoSearch(q)
	}
	return s.searchSQL(ctx, q)
}

func validPrefix(s string) bool {
	n := 0
	for _, r := range s {
		if !unicode.IsLetter(r) || r > unicode.MaxASCII {
			return false
		}
		n++
	}
	return n >= 1 && n <= 12
}

func (s *Service) Inspect(ctx context.Context, name string) (Profile, bool) {
	name = strings.TrimSpace(name)
	if !ValidName(name) {
		return Profile{}, false
	}
	key := strings.ToLower(name)
	ttl := s.cfg.StatusCache
	if ttl < time.Second {
		ttl = 20 * time.Second
	}
	s.mu.Lock()
	if e, ok := s.cache[key]; ok && time.Now().Before(e.until) {
		s.mu.Unlock()
		return e.p, e.ok
	}
	s.mu.Unlock()

	var p Profile
	var ok bool
	if s.cfg.DemoMode || s.db == nil {
		p, ok = demoInspect(name)
	} else {
		p, ok = s.inspectSQL(ctx, name)
	}
	s.mu.Lock()
	s.cache[key] = cacheEnt{p: p, ok: ok, until: time.Now().Add(ttl)}
	s.mu.Unlock()
	return p, ok
}

func fillSheet(p *Profile, race, class, gender uint8, money, played, logout uint32, mapID, zone uint32) {
	p.Race = wow.RaceName(race)
	p.RaceID = race
	p.Class = wow.ClassName(class)
	p.ClassID = class
	p.Gender = genderName(gender)
	p.Faction = wow.Faction(race)
	p.Gold = wow.Gold(money)
	p.Played = wow.Playtime(played)
	p.Location = wow.Location(mapID, zone)
	p.Logout = wow.LogoutLabel(p.Online, logout)
}

func genderName(g uint8) string {
	if g == 1 {
		return "Female"
	}
	return "Male"
}

func AchievementName(id uint32) string {
	if n, ok := achievementNames[id]; ok {
		return n
	}
	return fmt.Sprintf("Achievement %d", id)
}

func TalentName(spell uint32) string {
	if n, ok := talentNames[spell]; ok {
		return n
	}
	return fmt.Sprintf("Talent %d", spell)
}

func GlyphName(id uint32) string {
	if n, ok := glyphNames[id]; ok {
		return n
	}
	return fmt.Sprintf("Glyph %d", id)
}

func SlotName(slot uint8) string {
	if int(slot) < len(slotNames) {
		return slotNames[slot]
	}
	return fmt.Sprintf("Slot %d", slot)
}

var slotNames = [...]string{
	"Head", "Neck", "Shoulder", "Shirt", "Chest", "Waist", "Legs", "Feet",
	"Wrist", "Hands", "Finger", "Finger", "Trinket", "Trinket", "Back",
	"Main Hand", "Off Hand", "Ranged", "Tabard",
}

func emptyGear() []GearItem {
	out := make([]GearItem, 19)
	for i := range out {
		out[i] = GearItem{Slot: uint8(i), SlotName: SlotName(uint8(i)), Empty: true}
	}
	return out
}

func arenaBracket(t uint8) string {
	switch t {
	case 2:
		return "2v2"
	case 3:
		return "3v3"
	case 5:
		return "5v5"
	default:
		return fmt.Sprintf("%dv%d", t, t)
	}
}
