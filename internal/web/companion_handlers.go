package web

import (
	"net/http"
	"strconv"

	"github.com/zeroward/waygate/internal/companion"
)

func (s *Server) companionPage(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	ids := s.wowAccountIDs(r.Context(), sess.User.ID)
	picks := s.companion.List(r.Context(), ids)
	raw := r.URL.Query().Get("guid")
	var requested uint32
	if raw != "" {
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || n == 0 {
			http.NotFound(w, r)
			return
		}
		requested = uint32(n)
		owned := false
		for _, p := range picks {
			if p.GUID == requested {
				owned = true
				break
			}
		}
		if !owned {
			http.NotFound(w, r)
			return
		}
	}
	guid := companion.SelectGUID(picks, requested)
	for i := range picks {
		picks[i].Selected = picks[i].GUID == guid
	}
	var zone uint32
	if zraw := r.URL.Query().Get("zone"); zraw != "" {
		zn, err := strconv.ParseUint(zraw, 10, 32)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		zone = uint32(zn)
	}
	var snap *companion.Snapshot
	if guid != 0 {
		if p, ok := s.companion.Snapshot(r.Context(), ids, guid, zone); ok {
			snap = &p
		}
	}
	s.view(w, r, "companion.html", "Companion", "companion", map[string]any{
		"Chars": picks,
		"Snap":  snap,
	})
}

func (s *Server) companionLive(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLoginJSON(w, r)
	if sess == nil {
		return
	}
	n, err := strconv.ParseUint(r.URL.Query().Get("guid"), 10, 32)
	if err != nil || n == 0 {
		writeJSONError(w, http.StatusNotFound, "Character not found.")
		return
	}
	var zone uint32
	if zraw := r.URL.Query().Get("zone"); zraw != "" {
		zn, zerr := strconv.ParseUint(zraw, 10, 32)
		if zerr == nil {
			zone = uint32(zn)
		}
	}
	ids := s.wowAccountIDs(r.Context(), sess.User.ID)
	snap, ok := s.companion.Snapshot(r.Context(), ids, uint32(n), zone)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "Character not found.")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
