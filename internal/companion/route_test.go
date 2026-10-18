package companion

import "testing"

func TestBuildRouteChainOrder(t *testing.T) {
	in := []RouteInput{
		{ID: 14, Title: "The People's Militia III", Level: 17, MinLevel: 9, PrevQuestID: 13},
		{ID: 12, Title: "The People's Militia I", Level: 12, MinLevel: 9, NextQuestID: 13},
		{ID: 13, Title: "The People's Militia II", Level: 14, MinLevel: 9, PrevQuestID: 12, NextQuestID: 14},
		{ID: 9, Title: "The Killing Fields", Level: 15, MinLevel: 8, X: 100, Y: 0, HasPOI: true},
	}
	route := BuildRoute(in, nil, nil, 0, 0, 15)
	if len(route) != 4 {
		t.Fatalf("len %d", len(route))
	}
	got := []uint32{route[0].ID, route[1].ID, route[2].ID, route[3].ID}
	// Killing Fields is closer to the player at origin than the Militia chain (no POI → after).
	if got[0] != 9 {
		t.Fatalf("nearest first %v", got)
	}
	if got[1] != 12 || got[2] != 13 || got[3] != 14 {
		t.Fatalf("chain order %v", got)
	}
	if !route[0].Now || route[0].Status != "ready" {
		t.Fatalf("now %+v", route[0])
	}
}

func TestBuildRouteSkipsRewardedAndExclusive(t *testing.T) {
	in := []RouteInput{
		{ID: 1, Title: "Start", Level: 10, MinLevel: 10, NextQuestID: 2},
		{ID: 2, Title: "Followup", Level: 11, MinLevel: 10, PrevQuestID: 1},
		{ID: 10, Title: "Breadcrumb", Level: 10, MinLevel: 10, BreadcrumbFor: 2},
		{ID: 20, Title: "Hub A", Level: 10, MinLevel: 10, ExclusiveGroup: 50},
		{ID: 21, Title: "Hub B", Level: 10, MinLevel: 10, ExclusiveGroup: 50},
	}
	rewarded := map[uint32]struct{}{1: {}}
	inLog := map[uint32]string{2: "incomplete"}
	route := BuildRoute(in, rewarded, inLog, 0, 0, 12)
	ids := map[uint32]bool{}
	for _, s := range route {
		ids[s.ID] = true
	}
	if ids[1] {
		t.Fatal("rewarded still listed")
	}
	if ids[10] {
		t.Fatal("breadcrumb for active quest")
	}
	if ids[20] && ids[21] {
		t.Fatal("both exclusive")
	}
	if !ids[2] {
		t.Fatal("active followup missing")
	}
	var follow RouteStep
	for _, s := range route {
		if s.ID == 2 {
			follow = s
		}
	}
	if follow.Status != "active" || !follow.Now {
		t.Fatalf("follow %+v", follow)
	}
}

func TestBuildRouteLockedPrev(t *testing.T) {
	in := []RouteInput{
		{ID: 1, Title: "Start", Level: 20, MinLevel: 18},
		{ID: 2, Title: "Later", Level: 21, MinLevel: 18, PrevQuestID: 1},
	}
	route := BuildRoute(in, nil, nil, 0, 0, 15)
	if len(route) != 2 {
		t.Fatalf("len %d", len(route))
	}
	if route[0].Status != "locked" || route[0].Note != "Requires level 18" {
		t.Fatalf("level lock %+v", route[0])
	}
	route = BuildRoute(in, nil, nil, 0, 0, 20)
	if route[1].Status != "locked" || route[1].Note != "After: Start" {
		t.Fatalf("prev lock %+v", route[1])
	}
}

func TestZoneChoicesIncludesCurrent(t *testing.T) {
	z := ZoneChoices("Alliance", 80, 210)
	if len(z) == 0 || z[0].ID != 210 || !z[0].Selected || z[0].Name != "Icecrown" {
		t.Fatalf("%+v", z)
	}
	foundBorean := false
	for _, o := range z {
		if o.ID == 3537 {
			foundBorean = true
		}
		if o.ID == 14 {
			t.Fatal("horde starter at 80 alliance")
		}
	}
	if !foundBorean {
		t.Fatal("borean leftover at 80")
	}
}
