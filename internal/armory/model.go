package armory

import "encoding/json"

// Inventory slots that have no 3D mesh (neck, rings, trinkets).
var skipModelSlots = map[uint8]bool{1: true, 10: true, 11: true, 12: true, 13: true}

const invTypeRobe uint8 = 20

type modelChar struct {
	Race        int     `json:"race"`
	Gender      int     `json:"gender"`
	Skin        int     `json:"skin"`
	Face        int     `json:"face"`
	HairStyle   int     `json:"hairStyle"`
	HairColor   int     `json:"hairColor"`
	FacialStyle int     `json:"facialStyle"`
	Items       [][]int `json:"items"`
}

// ModelJSON is the payload for ZamModelViewer. Gender is inverted from
// AzerothCore (0 male) to the viewer (0 female, 1 male).
func (p Profile) ModelJSON() string {
	b, err := json.Marshal(p.Model())
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (p Profile) Model() modelChar {
	m := modelChar{
		Race:        int(p.RaceID),
		Gender:      viewerGender(p.GenderID),
		Skin:        int(p.Skin),
		Face:        int(p.Face),
		HairStyle:   int(p.HairStyle),
		HairColor:   int(p.HairColor),
		FacialStyle: int(p.FacialStyle),
		Items:       make([][]int, 0, 12),
	}
	for _, g := range p.Gear {
		if g.Empty || g.DisplayID == 0 || skipModelSlots[g.Slot] {
			continue
		}
		slot := viewerSlot(g.Slot, g.InvType)
		if slot == 0 {
			continue
		}
		// [viewerSlot, displayId, itemEntry, inventoryType]
		m.Items = append(m.Items, []int{int(slot), int(g.DisplayID), int(g.Entry), int(g.InvType)})
	}
	return m
}

func viewerGender(ac uint8) int {
	if ac == 0 {
		return 1 // male
	}
	return 0 // female
}

func viewerSlot(invSlot, invType uint8) uint8 {
	if skipModelSlots[invSlot] {
		return 0
	}
	if invSlot == 4 && invType == invTypeRobe {
		return 20
	}
	return invSlot + 1
}
