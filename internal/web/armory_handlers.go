package web

import (
	"net/http"
	"strings"

	"github.com/zeroward/waygate/internal/armory"
)

func (s *Server) armorySearch(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []armory.SearchHit
	if q != "" {
		hits = s.armory.Search(r.Context(), q)
	}
	s.view(w, r, "armory.html", "Armory", "armory", map[string]any{
		"Query": q,
		"Hits":  hits,
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
	case "gear", "talents", "achievements", "pvp":
	default:
		tab = "sheet"
	}
	s.view(w, r, "armory_char.html", p.Name, "armory", map[string]any{
		"Tab":     tab,
		"Profile": p,
	})
}
