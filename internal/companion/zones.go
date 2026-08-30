package companion

import (
	"fmt"

	"github.com/zeroward/waygate/internal/wow"
)

type ZoneOption struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Band     string `json:"band,omitempty"`
	Selected bool   `json:"-"`
}

type zoneBand struct {
	ID      uint32
	Min     uint8
	Max     uint8
	Faction string // empty = both
}

// Leveling zones only (zone id + level band). Names come from wow.ZoneName.
var levelingZones = []zoneBand{
	{12, 1, 10, "Alliance"}, {1, 1, 10, "Alliance"}, {141, 1, 10, "Alliance"}, {3524, 1, 10, "Alliance"},
	{14, 1, 10, "Horde"}, {215, 1, 10, "Horde"}, {85, 1, 10, "Horde"}, {3430, 1, 10, "Horde"},
	{40, 10, 20, "Alliance"}, {38, 10, 20, "Alliance"}, {148, 10, 20, "Alliance"}, {3525, 10, 20, "Alliance"},
	{17, 10, 25, "Horde"}, {130, 10, 20, "Horde"}, {3433, 10, 20, "Horde"},
	{44, 15, 25, "Alliance"}, {11, 20, 30, "Alliance"}, {10, 20, 30, "Alliance"},
	{406, 15, 27, "Horde"}, {267, 20, 30, ""}, {331, 20, 30, ""},
	{33, 25, 35, ""}, {400, 25, 35, "Horde"}, {36, 30, 40, ""}, {45, 30, 40, ""}, {405, 30, 40, ""},
	{8, 35, 45, "Alliance"}, {15, 35, 45, ""}, {47, 40, 50, ""}, {357, 40, 50, ""}, {440, 40, 50, ""},
	{4, 45, 55, ""}, {51, 45, 50, ""}, {361, 48, 55, ""}, {490, 48, 55, ""},
	{46, 50, 58, ""}, {28, 51, 58, ""}, {139, 53, 60, ""}, {618, 53, 60, ""}, {1377, 55, 60, ""},
	{3483, 58, 63, ""}, {3521, 60, 64, ""}, {3519, 62, 65, ""}, {3518, 64, 67, ""},
	{3522, 65, 68, ""}, {3520, 67, 70, ""}, {3523, 67, 70, ""}, {3703, 65, 70, ""}, {4080, 70, 70, ""},
	{3537, 68, 72, ""}, {495, 68, 72, ""}, {65, 71, 75, ""}, {394, 73, 75, ""},
	{66, 74, 77, ""}, {3711, 75, 78, ""}, {2817, 74, 77, ""}, {67, 76, 80, ""},
	{210, 77, 80, ""}, {4197, 77, 80, ""}, {4395, 74, 80, ""},
}

func ZoneChoices(faction string, level uint8, current uint32) []ZoneOption {
	seen := map[uint32]struct{}{}
	var out []ZoneOption
	add := func(id uint32, min, max uint8) {
		if id == 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		opt := ZoneOption{ID: id, Name: wow.ZoneName(id)}
		if min > 0 && max > 0 {
			opt.Band = fmt.Sprintf("%d–%d", min, max)
		}
		out = append(out, opt)
	}
	if current != 0 {
		min, max := uint8(0), uint8(0)
		for _, z := range levelingZones {
			if z.ID == current {
				min, max = z.Min, z.Max
				break
			}
		}
		add(current, min, max)
	}
	for _, z := range levelingZones {
		if z.Faction != "" && faction != "" && z.Faction != faction {
			continue
		}
		if int(z.Min) > int(level)+2 {
			continue
		}
		if int(z.Max) < int(level)-12 {
			continue
		}
		add(z.ID, z.Min, z.Max)
	}
	for i := range out {
		out[i].Selected = out[i].ID == current
	}
	return out
}
