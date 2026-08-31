package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeroward/waygate/internal/kb"
)

func (s *Server) notifyNewTicket(t kb.Ticket) {
	url := strings.TrimSpace(s.cfg.TicketWebhookURL)
	if url == "" {
		return
	}
	title := t.Title
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:80]) + "…"
	}
	link := strings.TrimRight(s.cfg.SiteURL, "/") + "/staff/tickets/" + strconv.FormatInt(t.ID, 10)
	body := fmt.Sprintf("New ticket **%s** (%s) %s — %s\n%s", t.PublicRef, t.Category, title, t.Username, link)
	body = strings.ReplaceAll(body, "@everyone", "everyone")
	body = strings.ReplaceAll(body, "@here", "here")
	payload, err := json.Marshal(map[string]string{"content": body})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		s.log.Error("ticket webhook", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Error("ticket webhook", "err", err)
		return
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		s.log.Error("ticket webhook", "status", res.StatusCode)
	}
}

func (s *Server) notifyTicketPlayer(t kb.Ticket, kind string) {
	if s.mail == nil || !s.mail.Enabled() || s.id == nil {
		return
	}
	u, err := s.id.GetByID(context.Background(), t.AccountID)
	if err != nil || strings.TrimSpace(u.Email) == "" {
		return
	}
	link := strings.TrimRight(s.cfg.SiteURL, "/") + "/tickets/" + strconv.FormatInt(t.ID, 10)
	subj := fmt.Sprintf("[%s] Ticket %s updated", s.cfg.RealmName, t.PublicRef)
	body := fmt.Sprintf("Your ticket %s (%s) was updated.\n\n%s\nStatus: %s\n\n%s\n",
		t.PublicRef, t.Title, kind, t.StatusLabel(), link)
	if err := s.mail.Send(u.Email, subj, body); err != nil {
		s.log.Error("ticket mail", "err", err, "ref", t.PublicRef)
	}
}
