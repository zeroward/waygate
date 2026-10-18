package armory

type professionMeta struct {
	Name      string
	Secondary bool
}

var professionSkills = map[uint32]professionMeta{
	164: {Name: "Blacksmithing"},
	165: {Name: "Leatherworking"},
	171: {Name: "Alchemy"},
	182: {Name: "Herbalism"},
	186: {Name: "Mining"},
	197: {Name: "Tailoring"},
	202: {Name: "Engineering"},
	333: {Name: "Enchanting"},
	393: {Name: "Skinning"},
	755: {Name: "Jewelcrafting"},
	773: {Name: "Inscription"},
	129: {Name: "First Aid", Secondary: true},
	185: {Name: "Cooking", Secondary: true},
	356: {Name: "Fishing", Secondary: true},
}

func professionIDs() []uint32 {
	out := make([]uint32, 0, len(professionSkills))
	for id := range professionSkills {
		out = append(out, id)
	}
	return out
}
