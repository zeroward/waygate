package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zeroward/waygate/internal/armory"
)

func (s *Server) armorySearch(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits, mine []armory.SearchHit
	if q != "" {
		hits = s.armory.Search(r.Context(), q)
	} else {
		chars, err := s.status.AccountCharactersMany(r.Context(), s.wowAccountIDs(r.Context(), sess.User.ID))
		if err != nil {
			s.log.Error("armory account characters", "err", err, "account", sess.User.ID)
		}
		for _, ch := range chars {
			mine = append(mine, armory.SearchHit{
				Name:    ch.Name,
				Level:   ch.Level,
				Race:    ch.Race,
				Class:   ch.Class,
				ClassID: ch.ClassID,
				Faction: ch.Faction,
			})
		}
	}
	s.view(w, r, "armory.html", "Armory", "armory", map[string]any{
		"Query": q,
		"Hits":  hits,
		"Mine":  mine,
	})
}

func (s *Server) armoryInspect(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	p, ok := s.armory.Inspect(r.Context(), name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "gear", "talents", "professions", "reputations", "achievements", "pvp", "guild":
	default:
		tab = "sheet"
	}
	s.view(w, r, "armory_char.html", p.Name, "armory", map[string]any{
		"Tab":     tab,
		"Profile": p,
	})
}

func (s *Server) armoryGuild(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	raw := strings.TrimSpace(r.PathValue("name"))
	name, err := url.PathUnescape(raw)
	if err != nil {
		name = raw
	}
	name = strings.ReplaceAll(name, "+", " ")
	g, ok := s.armory.Guild(r.Context(), name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.view(w, r, "armory_guild.html", g.Name, "armory", g)
}
