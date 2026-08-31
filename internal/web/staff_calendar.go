package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/kb"
)

func (s *Server) upcomingCalendar(ctx context.Context) []kb.RealmEvent {
	if s.kb == nil {
		return nil
	}
	today := time.Now().UTC().Format("2006-01-02")
	list, err := s.kb.ListUpcomingEvents(ctx, today, 8)
	if err != nil {
		s.log.Error("calendar", "err", err)
		return nil
	}
	return list
}

func (s *Server) staffCalendar(ctx context.Context) []kb.RealmEvent {
	if s.kb == nil {
		return nil
	}
	list, err := s.kb.ListStaffEvents(ctx, 20)
	if err != nil {
		s.log.Error("staff calendar", "err", err)
		return nil
	}
	return list
}

func (s *Server) staffEventPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	date := strings.TrimSpace(r.FormValue("event_date"))
	title := kb.ClipEventTitle(r.FormValue("title"))
	detail := kb.ClipEventDetail(r.FormValue("detail"))
	if !kb.ValidEventDate(date) {
		s.flashRedirect(w, r, "/staff#calendar", "error", "Pick a date.")
		return
	}
	if title == "" {
		s.flashRedirect(w, r, "/staff#calendar", "error", "Give the event a title.")
		return
	}
	ev, err := s.kb.CreateEvent(r.Context(), date, title, detail, sess.User.Username)
	if err != nil {
		s.log.Error("calendar add", "err", err)
		s.flashRedirect(w, r, "/staff#calendar", "error", "Could not save the event.")
		return
	}
	s.logStaff(sess.User.Username, "event-add", ev.Date+" "+ev.Title)
	s.flashRedirect(w, r, "/staff#calendar", "success", "Added "+ev.Title+".")
}

func (s *Server) staffEventDeletePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		s.flashRedirect(w, r, "/staff#calendar", "error", "That event was not found.")
		return
	}
	if err := s.kb.DeleteEvent(r.Context(), id); err != nil {
		s.flashRedirect(w, r, "/staff#calendar", "error", "Could not remove the event.")
		return
	}
	s.logStaff(sess.User.Username, "event-delete", r.PathValue("id"))
	s.flashRedirect(w, r, "/staff#calendar", "success", "Removed the event.")
}
