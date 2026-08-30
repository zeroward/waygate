package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/status"
)

func (s *Server) ticketsList(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	list, err := s.kb.ListTicketsForAccount(r.Context(), sess.User.ID)
	if err != nil {
		s.log.Error("tickets list", "err", err)
		s.flashRedirect(w, r, "/account", "error", "Could not load tickets.")
		return
	}
	s.view(w, r, "tickets.html", "Tickets", "tickets", map[string]any{"Tickets": list})
}

func (s *Server) ticketsNew(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	chars, _ := s.status.AccountCharactersMany(r.Context(), s.wowAccountIDs(r.Context(), sess.User.ID))
	s.view(w, r, "ticket_new.html", "New ticket", "tickets", map[string]any{
		"Categories": kb.TicketCategories,
		"Characters": chars,
		"Form":       kb.Ticket{},
		"Body":       "",
		"Error":      "",
	})
}

func (s *Server) ticketsCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.ticketRL.Allow(s.ip(r) + ":" + sess.User.Username) {
		s.flashRedirect(w, r, "/tickets/new", "error", "Too many tickets. Wait and try again.")
		return
	}
	t := kb.Ticket{
		AccountID: sess.User.ID,
		Username:  sess.User.Username,
		Category:  strings.TrimSpace(r.FormValue("category")),
		Title:     strings.TrimSpace(r.FormValue("title")),
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if guid, err := strconv.ParseUint(strings.TrimSpace(r.FormValue("character_guid")), 10, 32); err == nil && guid > 0 {
		ch, ok := s.ownedCharacter(r, sess.User.ID, uint32(guid))
		if !ok {
			s.ticketFormError(w, r, t, body, "Choose one of your characters.")
			return
		}
		t.CharacterGUID = ch.GUID
		t.CharacterName = ch.Name
	}
	saved, err := s.kb.CreateTicket(r.Context(), t, body)
	if err != nil {
		msg := "Could not open the ticket."
		if errors.Is(err, kb.ErrBadCategory) {
			msg = "Pick a category."
		} else if errors.Is(err, kb.ErrInvalid) {
			msg = "Title and a message are required."
		}
		s.ticketFormError(w, r, t, body, msg)
		return
	}
	s.logStaff(sess.User.Username, "ticket-open", saved.PublicRef)
	s.notifyNewTicket(saved)
	s.flashRedirect(w, r, "/tickets/"+strconv.FormatInt(saved.ID, 10), "success", "Ticket "+saved.PublicRef+" opened.")
}

func (s *Server) ticketFormError(w http.ResponseWriter, r *http.Request, t kb.Ticket, body, msg string) {
	chars, _ := s.status.AccountCharactersMany(r.Context(), s.wowAccountIDs(r.Context(), t.AccountID))
	w.WriteHeader(http.StatusBadRequest)
	s.view(w, r, "ticket_new.html", "New ticket", "tickets", map[string]any{
		"Categories": kb.TicketCategories,
		"Characters": chars,
		"Form":       t,
		"Body":       body,
		"Error":      msg,
	})
}

func (s *Server) ticketsView(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	t, ok := s.loadOwnTicket(w, r, sess.User.ID)
	if !ok {
		return
	}
	s.view(w, r, "ticket_view.html", t.PublicRef, "tickets", map[string]any{"Ticket": t, "Staff": false})
}

func (s *Server) ticketsComment(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	t, ok := s.loadOwnTicket(w, r, sess.User.ID)
	if !ok {
		return
	}
	if err := s.kb.AddTicketMessage(r.Context(), t.ID, sess.User.Username, false, r.FormValue("body")); err != nil {
		switch {
		case errors.Is(err, kb.ErrTicketClosed):
			s.flashRedirect(w, r, "/tickets/"+strconv.FormatInt(t.ID, 10), "error", "This ticket is closed.")
		default:
			s.flashRedirect(w, r, "/tickets/"+strconv.FormatInt(t.ID, 10), "error", "Message required.")
		}
		return
	}
	s.flashRedirect(w, r, "/tickets/"+strconv.FormatInt(t.ID, 10), "success", "Comment added.")
}

func (s *Server) staffTickets(w http.ResponseWriter, r *http.Request) {
	if s.requireMod(w, r) == nil {
		return
	}
	list, err := s.kb.ListOpenTickets(r.Context())
	if err != nil {
		s.log.Error("staff tickets", "err", err)
		s.flashRedirect(w, r, "/staff", "error", "Could not load tickets.")
		return
	}
	s.view(w, r, "staff_tickets.html", "Tickets", "staff", map[string]any{"Tickets": list})
}

func (s *Server) staffTicketView(w http.ResponseWriter, r *http.Request) {
	if s.requireMod(w, r) == nil {
		return
	}
	t, ok := s.loadTicketByPath(w, r)
	if !ok {
		return
	}
	s.view(w, r, "ticket_view.html", t.PublicRef, "staff", map[string]any{
		"Ticket":   t,
		"Staff":    true,
		"Statuses": kb.TicketStatusChoices(),
	})
}

func (s *Server) staffTicketUpdate(w http.ResponseWriter, r *http.Request) {
	sess := s.requireMod(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	t, ok := s.loadTicketByPath(w, r)
	if !ok {
		return
	}
	var notes []string
	if st := strings.TrimSpace(r.FormValue("status")); st != "" && st != t.Status {
		if err := s.kb.SetTicketStatus(r.Context(), t.ID, st, sess.User.Username); err != nil {
			s.flashRedirect(w, r, "/staff/tickets/"+strconv.FormatInt(t.ID, 10), "error", "Could not update status.")
			return
		}
		s.logStaff(sess.User.Username, "ticket-status", t.PublicRef+"="+st)
		notes = append(notes, "Staff changed the status.")
	}
	if body := strings.TrimSpace(r.FormValue("body")); body != "" {
		if err := s.kb.AddTicketMessage(r.Context(), t.ID, sess.User.Username, true, body); err != nil {
			s.flashRedirect(w, r, "/staff/tickets/"+strconv.FormatInt(t.ID, 10), "error", "Could not add comment.")
			return
		}
		notes = append(notes, "Staff replied.")
	}
	if len(notes) > 0 {
		if fresh, err := s.kb.GetTicket(r.Context(), t.ID); err == nil {
			t = fresh
		}
		s.notifyTicketPlayer(t, strings.Join(notes, " "))
	}
	s.flashRedirect(w, r, "/staff/tickets/"+strconv.FormatInt(t.ID, 10), "success", "Ticket updated.")
}

func (s *Server) loadTicketByPath(w http.ResponseWriter, r *http.Request) (kb.Ticket, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return kb.Ticket{}, false
	}
	t, err := s.kb.GetTicket(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return kb.Ticket{}, false
	}
	return t, true
}

func (s *Server) loadOwnTicket(w http.ResponseWriter, r *http.Request, accountID uint32) (kb.Ticket, bool) {
	t, ok := s.loadTicketByPath(w, r)
	if !ok {
		return kb.Ticket{}, false
	}
	if t.AccountID != accountID {
		http.NotFound(w, r)
		return kb.Ticket{}, false
	}
	return t, true
}

func (s *Server) ownedCharacter(r *http.Request, accountID, guid uint32) (status.Character, bool) {
	list, err := s.status.AccountCharactersMany(r.Context(), s.wowAccountIDs(r.Context(), accountID))
	if err != nil {
		return status.Character{}, false
	}
	for _, ch := range list {
		if ch.GUID == guid {
			return ch, true
		}
	}
	return status.Character{}, false
}
