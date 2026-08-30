package companion

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/wow"
)

const (
	questComplete   uint8 = 1
	questIncomplete uint8 = 3
	questFailed     uint8 = 5
)

type Service struct {
	cfg config.Config
	db  *db.DB
	log *slog.Logger
}

func New(cfg config.Config, database *db.DB, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{cfg: cfg, db: database, log: log}
}

type Pick struct {
	GUID     uint32 `json:"guid"`
	Name     string `json:"name"`
	Level    uint8  `json:"level"`
	Race     string `json:"race"`
	Class    string `json:"class"`
	ClassID  uint8  `json:"classId"`
	Faction  string `json:"faction"`
	Online   bool   `json:"online"`
	Selected bool   `json:"-"`
}

type Objective struct {
	Text string `json:"text"`
	Have int    `json:"have"`
	Need int    `json:"need"`
	Done bool   `json:"done"`
}

type Quest struct {
	ID         uint32      `json:"id"`
	Title      string      `json:"title"`
	Status     string      `json:"status"`
	Level      int16       `json:"level"`
	Objectives []Objective `json:"objectives,omitempty"`
}

func (q Quest) Wowhead() string {
	if q.ID == 0 {
		return ""
	}
	return fmt.Sprintf("https://www.wowhead.com/wotlk/quest=%d", q.ID)
}

func (q Quest) StatusLabel() string {
	switch q.Status {
	case "complete":
		return "Complete"
	case "failed":
		return "Failed"
	default:
		return "In progress"
	}
}

type Snapshot struct {
	GUID      uint32       `json:"guid"`
	Name      string       `json:"name"`
	Level     uint8        `json:"level"`
	Race      string       `json:"race"`
	RaceID    uint8        `json:"raceId"`
	Class     string       `json:"class"`
	ClassID   uint8        `json:"classId"`
	Faction   string       `json:"faction"`
	Online    bool         `json:"online"`
	Location  string       `json:"location"`
	MapID     uint32       `json:"mapId"`
	ZoneID    uint32       `json:"zoneId"`
	X         float32      `json:"x"`
	Y         float32      `json:"y"`
	Quests    []Quest      `json:"quests"`
	Route     []RouteStep  `json:"route"`
	RouteZone uint32       `json:"routeZone"`
	RouteName string       `json:"routeName"`
	Zones     []ZoneOption `json:"zones,omitempty"`
}

func (s Snapshot) CoordLabel() string {
	return fmt.Sprintf("%.1f, %.1f", s.X, s.Y)
}

func StatusKey(status uint8) string {
	switch status {
	case questComplete:
		return "complete"
	case questFailed:
		return "failed"
	case questIncomplete:
		return "incomplete"
	default:
		return "unknown"
	}
}

func QuestTitle(id uint32, title string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		return title
	}
	return fmt.Sprintf("Quest %d", id)
}

// SelectGUID returns requested when it is owned, otherwise the lone online
// character, otherwise the first pick (already online-then-level sorted).
func SelectGUID(picks []Pick, requested uint32) uint32 {
	if requested != 0 {
		for _, p := range picks {
			if p.GUID == requested {
				return requested
			}
		}
		return 0
	}
	var online int
	var onlineGUID uint32
	for _, p := range picks {
		if p.Online {
			online++
			onlineGUID = p.GUID
		}
	}
	if online == 1 {
		return onlineGUID
	}
	if len(picks) > 0 {
		return picks[0].GUID
	}
	return 0
}

func (s *Service) List(ctx context.Context, accountIDs []uint32) []Pick {
	if s.cfg.DemoMode || s.db == nil {
		return demoPicks()
	}
	return s.listSQL(ctx, accountIDs)
}

func (s *Service) Snapshot(ctx context.Context, accountIDs []uint32, guid, zone uint32) (Snapshot, bool) {
	if guid == 0 {
		return Snapshot{}, false
	}
	if s.cfg.DemoMode || s.db == nil {
		return demoSnapshot(guid, zone)
	}
	return s.snapshotSQL(ctx, accountIDs, guid, zone)
}

func pickFromRow(guid uint32, name string, level, race, class uint8, online bool) Pick {
	return Pick{
		GUID:    guid,
		Name:    name,
		Level:   level,
		Race:    wow.RaceName(race),
		Class:   wow.ClassName(class),
		ClassID: class,
		Faction: wow.Faction(race),
		Online:  online,
	}
}
