package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/status"
)

// These DTOs are the v1 wire contract, deliberately independent of template data.
type managementResponse struct {
	GeneratedAt     time.Time         `json:"generated_at"`
	CacheTTLSeconds int               `json:"cache_ttl_seconds"`
	Demo            bool              `json:"demo"`
	Partial         bool              `json:"partial"`
	Data            any               `json:"data"`
	Errors          []managementError `json:"errors"`
}

type managementError struct {
	Section string `json:"section,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type managementDashboard struct {
	Status       *managementStatus       `json:"status"`
	Players      []managementPlayer      `json:"players"`
	Leaderboards *managementLeaderboards `json:"leaderboards"`
	Modules      []managementModule      `json:"modules"`
}

type managementStatus struct {
	RealmName    string               `json:"realm_name"`
	RealmUp      bool                 `json:"realm_up"`
	CoreName     string               `json:"core_name"`
	Expansion    string               `json:"expansion"`
	WorldVersion string               `json:"world_version"`
	PublicHost   string               `json:"public_host"`
	PublicAuth   int                  `json:"public_auth_port"`
	PublicWorld  int                  `json:"public_world_port"`
	Population   managementPopulation `json:"population"`
}

type managementPopulation struct {
	Humans   int `json:"humans"`
	Bots     int `json:"bots"`
	GMs      int `json:"gms"`
	Total    int `json:"total"`
	Alliance int `json:"alliance"`
	Horde    int `json:"horde"`
}

type managementPlayer struct {
	Name    string `json:"name"`
	Level   uint8  `json:"level"`
	Race    string `json:"race"`
	RaceID  uint8  `json:"race_id"`
	Class   string `json:"class"`
	ClassID uint8  `json:"class_id"`
	Zone    string `json:"zone"`
	Faction string `json:"faction"`
}

type managementLeaderboards struct {
	Playtime []managementBoardRow `json:"playtime"`
	Kills    []managementBoardRow `json:"kills"`
	Honor    []managementBoardRow `json:"honor"`
	Arena    []managementBoardRow `json:"arena"`
	Gold     []managementBoardRow `json:"gold"`
}

type managementBoardRow struct {
	Rank  int    `json:"rank"`
	Name  string `json:"name"`
	Level uint8  `json:"level"`
	Class string `json:"class"`
	Value string `json:"value"`
	Raw   uint32 `json:"raw"`
}

type managementModule struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func isManagementAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

func isAdminAPIPath(path string) bool {
	return path == "/api/v1/admin" || strings.HasPrefix(path, "/api/v1/admin/")
}

func matchBearerToken(expected, candidate string) bool {
	if expected == "" || candidate == "" {
		return false
	}
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func extractBearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(auth[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

func (s *Server) managementAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isAdminAPIPath(r.URL.Path) {
		s.adminAPI(w, r)
		return
	}
	if s.cfg.ManagementAPIToken == "" && s.cfg.AdminAPIToken == "" {
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}
	token, ok := extractBearerToken(r)
	if !ok || (!matchBearerToken(s.cfg.ManagementAPIToken, token) && !matchBearerToken(s.cfg.AdminAPIToken, token)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="management"`)
		writeManagementError(w, r, http.StatusUnauthorized, "unauthorized", "A valid management API bearer token is required.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeManagementError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and HEAD are supported.")
		return
	}
	var section string
	switch r.URL.Path {
	case "/api/v1/dashboard":
		section = "dashboard"
	case "/api/v1/status":
		section = "status"
	case "/api/v1/players":
		section = "players"
	case "/api/v1/leaderboards":
		section = "leaderboards"
	case "/api/v1/modules":
		section = "modules"
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}

	snap := s.status.Get(r.Context())
	if snap.Unavailable[section] {
		writeManagementError(w, r, http.StatusServiceUnavailable, section+"_unavailable", "Requested data could not be collected.")
		return
	}
	dashboard := managementData(snap)
	res := managementResponse{
		GeneratedAt: snap.GeneratedAt.UTC(), CacheTTLSeconds: int(s.cfg.StatusCache / time.Second),
		Demo: snap.Demo, Errors: make([]managementError, 0),
	}
	switch section {
	case "dashboard":
		res.Data = dashboard
		for _, key := range []string{"status", "players", "leaderboards", "modules"} {
			if snap.Unavailable[key] {
				res.Errors = append(res.Errors, managementError{Section: key, Code: key + "_unavailable", Message: "Requested data could not be collected."})
			}
		}
		res.Partial = len(res.Errors) > 0
	case "status":
		res.Data = dashboard.Status
	case "players":
		res.Data = dashboard.Players
	case "leaderboards":
		res.Data = dashboard.Leaderboards
	case "modules":
		res.Data = dashboard.Modules
	}
	writeManagementJSON(w, r, http.StatusOK, res)
}

func managementData(s status.Snapshot) managementDashboard {
	var d managementDashboard
	if !s.Unavailable["status"] {
		d.Status = &managementStatus{
			RealmName: s.RealmName, RealmUp: s.RealmUp, CoreName: s.CoreName, Expansion: s.Expansion,
			WorldVersion: s.WorldVersion, PublicHost: s.PublicHost, PublicAuth: s.PublicAuth, PublicWorld: s.PublicWorld,
			Population: managementPopulation{Humans: s.Online, Bots: s.OnlineBots, GMs: s.OnlineGMs, Total: s.OnlineTotal, Alliance: s.Alliance, Horde: s.Horde},
		}
	}
	if !s.Unavailable["players"] {
		d.Players = make([]managementPlayer, 0, len(s.Players))
		for _, p := range s.Players {
			d.Players = append(d.Players, managementPlayer{Name: p.Name, Level: p.Level, Race: p.Race, RaceID: p.RaceID, Class: p.Class, ClassID: p.ClassID, Zone: p.Zone, Faction: p.Faction})
		}
	}
	if !s.Unavailable["leaderboards"] {
		d.Leaderboards = &managementLeaderboards{
			Playtime: managementRows(s.Playtime), Kills: managementRows(s.Kills), Honor: managementRows(s.Honor),
			Arena: managementRows(s.Arena), Gold: managementRows(s.Gold),
		}
	}
	if !s.Unavailable["modules"] {
		d.Modules = make([]managementModule, 0, len(s.Modules))
		for _, m := range s.Modules {
			d.Modules = append(d.Modules, managementModule{ID: m.ID, Title: m.Title})
		}
	}
	return d
}

func managementRows(rows []status.BoardRow) []managementBoardRow {
	out := make([]managementBoardRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, managementBoardRow{Rank: r.Rank, Name: r.Name, Level: r.Level, Class: r.Class, Value: r.Value, Raw: r.Raw})
	}
	return out
}

func writeManagementError(w http.ResponseWriter, r *http.Request, code int, name, message string) {
	writeManagementJSON(w, r, code, struct {
		Error managementError `json:"error"`
	}{Error: managementError{Code: name, Message: message}})
}

func writeManagementJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"internal_error","message":"Request could not be completed."}}`)
	}
	data = append(data, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
