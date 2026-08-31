package kb

import (
	"context"
	"testing"
	"time"
)

func TestRealmEventsUpcoming(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.CreateEvent(ctx, "nope", "Raid", "", "ADMIN"); err == nil {
		t.Fatal("bad date")
	}
	if _, err := s.CreateEvent(ctx, time.Now().UTC().Format("2006-01-02"), "", "", "ADMIN"); err == nil {
		t.Fatal("empty title")
	}
	today := time.Now().UTC().Format("2006-01-02")
	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	if _, err := s.CreateEvent(ctx, past, "Old raid", "", "ADMIN"); err != nil {
		t.Fatal(err)
	}
	ev, err := s.CreateEvent(ctx, today, "Raid night", "ICC 25", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	up, err := s.ListUpcomingEvents(ctx, today, 8)
	if err != nil || len(up) != 1 || up[0].Title != "Raid night" || up[0].Detail != "ICC 25" {
		t.Fatalf("upcoming %v %+v", err, up)
	}
	if up[0].DisplayDate() == "" {
		t.Fatal("display date")
	}
	all, err := s.ListStaffEvents(ctx, 20)
	if err != nil || len(all) != 2 {
		t.Fatalf("staff %v %d", err, len(all))
	}
	if err := s.DeleteEvent(ctx, ev.ID); err != nil {
		t.Fatal(err)
	}
	up, err = s.ListUpcomingEvents(ctx, today, 8)
	if err != nil || len(up) != 0 {
		t.Fatalf("after delete %v %+v", err, up)
	}
}
