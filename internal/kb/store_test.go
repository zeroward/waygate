package kb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSeedOnceAndCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seed := Article{
		Slug:         "how-to-connect",
		Title:        "How to connect",
		BodyMarkdown: "set realmlist example\n\nUse [Downloads](/downloads).",
		Summary:      "Set realmlist and start Wow.exe.",
		Category:     "Getting started",
		Published:    true,
		CreatedBy:    "system",
		UpdatedBy:    "system",
	}
	if err := s.SeedIfMissing(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedIfMissing(ctx, Article{Slug: "how-to-connect", Title: "changed", Category: "X", CreatedBy: "x", UpdatedBy: "x"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBySlug(ctx, "how-to-connect")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "How to connect" || !got.Published || got.ID < 1 {
		t.Fatalf("seed overwritten or incomplete: %+v", got)
	}

	all, err := s.ListAll(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list all %v %d", err, len(all))
	}
	pub, err := s.ListPublished(ctx, "")
	if err != nil || len(pub) != 1 {
		t.Fatalf("published %v %d", err, len(pub))
	}

	got.Title = "How to connect (edit)"
	got.UpdatedBy = "ADMIN"
	got, err = s.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "How to connect (edit)" || got.UpdatedBy != "ADMIN" {
		t.Fatalf("update: %+v", got)
	}

	draft := Article{Title: "Hidden notes", Slug: "hidden-notes", Category: "Staff", BodyMarkdown: "secret", CreatedBy: "ADMIN", UpdatedBy: "ADMIN"}
	draft, err = s.Create(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	pub, err = s.ListPublished(ctx, "")
	if err != nil || len(pub) != 1 {
		t.Fatalf("draft leaked into published: %v %d", err, len(pub))
	}
	found, err := s.ListPublished(ctx, "realmlist")
	if err != nil || len(found) != 1 {
		t.Fatalf("search %v %d", err, len(found))
	}

	if _, err := s.Create(ctx, Article{Title: "Dup", Slug: "how-to-connect", CreatedBy: "a", UpdatedBy: "a"}); err != ErrSlugTaken {
		t.Fatalf("unique slug: %v", err)
	}
	if err := s.Delete(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(ctx, draft.ID); err != ErrNotFound {
		t.Fatalf("deleted: %v", err)
	}
}

func TestStaffEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.LogEvent(ctx, "STAFFER", "create", "NEWPLAYER"); err != nil {
		t.Fatal(err)
	}
	ev, err := s.RecentEvents(ctx, 10)
	if err != nil || len(ev) != 1 || ev[0].Actor != "STAFFER" || ev[0].Action != "create" || ev[0].Target != "NEWPLAYER" {
		t.Fatalf("%v %+v", err, ev)
	}
}

func TestTickets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	t1, err := s.CreateTicket(ctx, Ticket{
		AccountID: 1, Username: "HEROONE", Category: "Name change", Title: "Rename me",
	}, "please")
	if err != nil || t1.ID < 1 || !strings.HasPrefix(t1.PublicRef, "T-") || t1.Status != TicketOpen {
		t.Fatalf("create %v %+v", err, t1)
	}
	if _, err := s.CreateTicket(ctx, Ticket{AccountID: 1, Username: "HEROONE", Category: "Nope", Title: "x"}, "y"); err != ErrBadCategory {
		t.Fatalf("bad category %v", err)
	}
	t2, err := s.CreateTicket(ctx, Ticket{
		AccountID: 2, Username: "OTHERTWO", Category: "Items", Title: "Other ticket",
	}, "mine")
	if err != nil {
		t.Fatal(err)
	}
	mine, err := s.ListTicketsForAccount(ctx, 1)
	if err != nil || len(mine) != 1 || mine[0].ID != t1.ID {
		t.Fatalf("account list %v %+v", err, mine)
	}
	open, err := s.ListOpenTickets(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("open %v %+v", err, open)
	}
	if err := s.AddTicketMessage(ctx, t1.ID, "STAFFER", true, "working"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketStatus(ctx, t1.ID, TicketDone, "STAFFER"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTicket(ctx, t1.ID)
	if err != nil || got.Status != TicketDone || got.ClosedBy != "STAFFER" || len(got.Messages) != 2 {
		t.Fatalf("get %v %+v", err, got)
	}
	if err := s.AddTicketMessage(ctx, t1.ID, "HEROONE", false, "late"); err != ErrTicketClosed {
		t.Fatalf("closed comment %v", err)
	}
	open, err = s.ListOpenTickets(ctx)
	if err != nil || len(open) != 1 || open[0].ID != t2.ID {
		t.Fatalf("open after done %v %+v", err, open)
	}
}

func TestLatestPublished(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	got, err := s.LatestPublished(ctx)
	if err != nil || got != nil {
		t.Fatalf("empty %v %+v", err, got)
	}
	if _, err := s.Create(ctx, Article{Title: "Draft", Slug: "draft", Category: "X", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestPublished(ctx)
	if err != nil || got != nil {
		t.Fatalf("draft leaked %v %+v", err, got)
	}
	old := Article{Title: "Older", Slug: "older", Category: "Getting started", Summary: "old", Published: true, CreatedBy: "a", UpdatedBy: "a"}
	if _, err := s.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	newer := Article{Title: "How to connect", Slug: "how-to-connect", Category: "Getting started", Summary: "Set realmlist.", Published: true, CreatedBy: "a", UpdatedBy: "a"}
	if _, err := s.Create(ctx, newer); err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestPublished(ctx)
	if err != nil || got == nil || got.Slug != "how-to-connect" {
		t.Fatalf("want newest published, got %+v %v", got, err)
	}
}

func TestSlugify(t *testing.T) {
	if g := Slugify("How to connect!"); g != "how-to-connect" {
		t.Fatalf("got %q", g)
	}
	if !ValidSlug("how-to-connect") || ValidSlug("How_to") || ValidSlug("-nope") || ValidSlug("a/b") {
		t.Fatal("valid slug rules")
	}
}
