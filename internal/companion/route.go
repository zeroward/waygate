package companion

import (
	"fmt"
	"math"
	"sort"
)

const (
	questFlagRaid         uint32 = 0x40
	questFlagTracking     uint32 = 0x400
	questFlagDaily        uint32 = 0x1000
	questFlagUnavailable  uint32 = 0x4000
	questFlagWeekly       uint32 = 0x8000
	specialFlagRepeatable uint32 = 0x1
	routeLimit                   = 40
)

type RouteInput struct {
	ID             uint32
	Title          string
	Level          int16
	MinLevel       uint8
	PrevQuestID    int32
	NextQuestID    uint32
	ExclusiveGroup int32
	BreadcrumbFor  uint32
	X, Y           float32
	HasPOI         bool
}

type RouteStep struct {
	Step   int    `json:"step"`
	ID     uint32 `json:"id"`
	Title  string `json:"title"`
	Level  int16  `json:"level"`
	Status string `json:"status"` // ready, active, locked
	Note   string `json:"note,omitempty"`
	Now    bool   `json:"now,omitempty"`
}

func (q RouteStep) Wowhead() string {
	if q.ID == 0 {
		return ""
	}
	return fmt.Sprintf("https://www.wowhead.com/wotlk/quest=%d", q.ID)
}

func (q RouteStep) StatusLabel() string {
	switch q.Status {
	case "active":
		return "In your log"
	case "locked":
		return "Locked"
	default:
		return "Pick up"
	}
}

func skipForRoute(flags uint32, info uint16, special uint32, maxLevel, playerLevel uint8, allowClass, classMask uint32) bool {
	if flags&(questFlagRaid|questFlagTracking|questFlagDaily|questFlagUnavailable|questFlagWeekly) != 0 {
		return true
	}
	switch info {
	case 41, 62, 81, 82, 85, 88, 89:
		return true
	}
	if special&specialFlagRepeatable != 0 {
		return true
	}
	if maxLevel > 0 && playerLevel > maxLevel {
		return true
	}
	if allowClass != 0 && classMask != 0 && allowClass&classMask == 0 {
		return true
	}
	return false
}

func BuildRoute(in []RouteInput, rewarded map[uint32]struct{}, inLog map[uint32]string, px, py float32, level uint8) []RouteStep {
	if rewarded == nil {
		rewarded = map[uint32]struct{}{}
	}
	if inLog == nil {
		inLog = map[uint32]string{}
	}
	byID := make(map[uint32]RouteInput, len(in))
	for _, n := range in {
		byID[n.ID] = n
	}

	keep := map[uint32]RouteInput{}
	groups := map[int32][]RouteInput{}
	for _, n := range in {
		if n.ID == 0 {
			continue
		}
		if _, done := rewarded[n.ID]; done {
			continue
		}
		if n.BreadcrumbFor != 0 {
			if _, done := rewarded[n.BreadcrumbFor]; done {
				continue
			}
			if _, active := inLog[n.BreadcrumbFor]; active {
				continue
			}
		}
		if n.ExclusiveGroup > 0 {
			groups[n.ExclusiveGroup] = append(groups[n.ExclusiveGroup], n)
			continue
		}
		keep[n.ID] = n
	}
	for _, g := range groups {
		if c := pickExclusive(g, inLog); c.ID != 0 {
			keep[c.ID] = c
		}
	}

	next := map[uint32]uint32{}
	predCount := map[uint32]int{}
	for id, n := range keep {
		if n.NextQuestID != 0 {
			if _, ok := keep[n.NextQuestID]; ok {
				next[id] = n.NextQuestID
				predCount[n.NextQuestID]++
			}
		}
		if n.PrevQuestID > 0 {
			pid := uint32(n.PrevQuestID)
			if _, ok := keep[pid]; ok && next[pid] == 0 {
				next[pid] = id
				predCount[id]++
			}
		}
	}

	var starts []RouteInput
	for id, n := range keep {
		if predCount[id] == 0 {
			starts = append(starts, n)
		}
	}
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].MinLevel != starts[j].MinLevel {
			return starts[i].MinLevel < starts[j].MinLevel
		}
		return starts[i].ID < starts[j].ID
	})

	used := map[uint32]bool{}
	var chains []routeChain
	walk := func(st RouteInput) {
		if used[st.ID] {
			return
		}
		c := routeChain{}
		cur := st
		for {
			if used[cur.ID] {
				break
			}
			used[cur.ID] = true
			c.nodes = append(c.nodes, cur)
			if _, ok := inLog[cur.ID]; ok {
				c.active = true
			}
			nid, ok := next[cur.ID]
			if !ok {
				break
			}
			nxt, ok := keep[nid]
			if !ok {
				break
			}
			cur = nxt
		}
		if len(c.nodes) > 0 {
			chains = append(chains, c)
		}
	}
	for _, st := range starts {
		walk(st)
	}
	var leftover []RouteInput
	for id, n := range keep {
		if !used[id] {
			leftover = append(leftover, n)
		}
	}
	sort.Slice(leftover, func(i, j int) bool { return leftover[i].ID < leftover[j].ID })
	for _, n := range leftover {
		walk(n)
	}

	chains = orderChainsNN(chains, px, py)

	out := make([]RouteStep, 0, len(keep))
	step := 1
	for _, c := range chains {
		for _, n := range c.nodes {
			if step > routeLimit {
				markNow(out)
				return out
			}
			st := RouteStep{Step: step, ID: n.ID, Title: QuestTitle(n.ID, n.Title), Level: n.Level, Status: "ready"}
			if _, ok := inLog[n.ID]; ok {
				st.Status = "active"
			}
			if n.MinLevel > level {
				st.Status = "locked"
				st.Note = fmt.Sprintf("Requires level %d", n.MinLevel)
			} else if n.PrevQuestID > 0 {
				pid := uint32(n.PrevQuestID)
				_, done := rewarded[pid]
				_, active := inLog[pid]
				if !done && !active {
					st.Status = "locked"
					if p, ok := byID[pid]; ok {
						st.Note = "After: " + QuestTitle(pid, p.Title)
					} else {
						st.Note = "Earlier quest still open"
					}
				}
			} else if n.PrevQuestID < 0 {
				pid := uint32(-n.PrevQuestID)
				_, done := rewarded[pid]
				_, active := inLog[pid]
				if !done && !active {
					st.Status = "locked"
					st.Note = "Pick up the previous quest first"
				}
			}
			out = append(out, st)
			step++
		}
	}
	markNow(out)
	return out
}

func pickExclusive(g []RouteInput, inLog map[uint32]string) RouteInput {
	var chosen RouteInput
	for _, n := range g {
		if _, ok := inLog[n.ID]; ok {
			return n
		}
		if chosen.ID == 0 || n.MinLevel < chosen.MinLevel || (n.MinLevel == chosen.MinLevel && n.ID < chosen.ID) {
			chosen = n
		}
	}
	return chosen
}

func markNow(steps []RouteStep) {
	for i := range steps {
		if steps[i].Status == "active" || steps[i].Status == "ready" {
			steps[i].Now = true
			return
		}
	}
}

func chainDist(nodes []RouteInput, x, y float32) float32 {
	for _, n := range nodes {
		if n.HasPOI {
			dx, dy := n.X-x, n.Y-y
			return dx*dx + dy*dy
		}
	}
	if len(nodes) == 0 {
		return float32(math.MaxFloat32 / 4)
	}
	return 1e12 + float32(nodes[0].MinLevel)*1e6 + float32(nodes[0].ID)
}

func lastPOI(nodes []RouteInput) (float32, float32, bool) {
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i].HasPOI {
			return nodes[i].X, nodes[i].Y, true
		}
	}
	return 0, 0, false
}

func orderChainsNN(chains []routeChain, px, py float32) []routeChain {
	if len(chains) < 2 {
		return chains
	}
	var active, rest []routeChain
	for _, c := range chains {
		if c.active {
			active = append(active, c)
		} else {
			rest = append(rest, c)
		}
	}
	x, y := px, py
	if len(active) > 0 {
		if lx, ly, ok := lastPOI(active[len(active)-1].nodes); ok {
			x, y = lx, ly
		}
	}
	out := make([]routeChain, 0, len(chains))
	out = append(out, active...)
	used := make([]bool, len(rest))
	for n := 0; n < len(rest); n++ {
		best := -1
		bestD := float32(math.MaxFloat32)
		for i, c := range rest {
			if used[i] {
				continue
			}
			d := chainDist(c.nodes, x, y)
			if best < 0 || d < bestD || (d == bestD && c.nodes[0].ID < rest[best].nodes[0].ID) {
				best = i
				bestD = d
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		out = append(out, rest[best])
		if lx, ly, ok := lastPOI(rest[best].nodes); ok {
			x, y = lx, ly
		}
	}
	return out
}

type routeChain struct {
	nodes  []RouteInput
	active bool
}
