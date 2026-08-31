package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	mvMaxBytes         = 16 << 20
	mvTimeout          = 15 * time.Second
	displayMapMaxBytes = 4096
	displayMapTimeout  = 8 * time.Second
	displayMapCacheMax = 4096
)

var (
	// Live CDN: WotLK display IDs are mapped first; hd viewer loads m2 meshes
	// (wrath/classic hd:false asks for mo3 files that are not on zamimg).
	mvUpstream = "https://wow.zamimg.com/modelviewer/live/"
	mvPathRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	mvClient   = &http.Client{
		Timeout: mvTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != "wow.zamimg.com" && !strings.HasSuffix(req.URL.Host, ".zamimg.com") {
				return errors.New("redirect")
			}
			if len(via) > 3 {
				return errors.New("redirect")
			}
			return nil
		},
	}

	displayMapUpstream = "https://wotlk.murlocvillage.com/api/items"
	displayMapClient   = &http.Client{
		Timeout: displayMapTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := req.URL.Host
			if host != "wotlk.murlocvillage.com" && !strings.HasSuffix(host, ".murlocvillage.com") {
				return errors.New("redirect")
			}
			if len(via) > 3 {
				return errors.New("redirect")
			}
			return nil
		},
	}

	errMVNotFound = errors.New("model not found")

	displayMapMu    sync.Mutex
	displayMapCache = map[string]uint32{}
)

func mvPathOK(p string) bool {
	_, ok := mvCleanPath(p)
	return ok
}

func mvCleanPath(p string) (string, bool) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || len(p) > 256 {
		return "", false
	}
	if strings.Contains(p, "..") || strings.Contains(p, "//") {
		return "", false
	}
	clean := strings.TrimPrefix(path.Clean("/"+p), "/")
	if clean == "" || clean == "." || !mvPathRe.MatchString(clean) {
		return "", false
	}
	return clean, true
}

// mvFallback lists same-schema CDN paths to try after a 404.
// Cloaks: paperdoll slot 15, zamimg stores them under InventoryType 16.
func mvFallback(p string) []string {
	const cloak = "meta/armor/15/"
	if strings.HasPrefix(p, cloak) && strings.HasSuffix(p, ".json") {
		alt := "meta/armor/16/" + strings.TrimPrefix(p, cloak)
		if _, ok := mvCleanPath(alt); ok {
			return []string{alt}
		}
	}
	return nil
}

func (s *Server) armoryModelProxy(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	p := r.PathValue("path")
	if p == "" {
		p = strings.TrimPrefix(r.URL.Path, "/armory/mv/")
	}
	clean, ok := mvCleanPath(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mvTimeout)
	defer cancel()
	res, err := mvFetch(ctx, clean, r.Header.Get("Accept"))
	if err != nil {
		if errors.Is(err, errMVNotFound) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("armory model proxy", "err", err, "path", clean)
		http.Error(w, "Could not load the model.", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		http.Error(w, "Could not load the model.", http.StatusBadGateway)
		return
	}
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(res.Body, mvMaxBytes))
}

func mvFetch(ctx context.Context, p, accept string) (*http.Response, error) {
	paths := append([]string{p}, mvFallback(p)...)
	for _, candidate := range paths {
		if candidate != p {
			if _, ok := mvCleanPath(candidate); !ok {
				continue
			}
		}
		res, err := mvGetOnce(ctx, candidate, accept)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusNotFound {
			return res, nil
		}
		res.Body.Close()
	}
	return nil, errMVNotFound
}

func mvGetOnce(ctx context.Context, p, accept string) (*http.Response, error) {
	u, err := url.JoinPath(mvUpstream, p)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GatehouseArmory/1.0")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return mvClient.Do(req)
}

func parseU32(s string) (uint32, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	if strconv.FormatUint(n, 10) != s {
		return 0, false
	}
	return uint32(n), true
}

func (s *Server) armoryDisplayMap(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	entry, ok1 := parseU32(r.PathValue("entry"))
	displayID, ok2 := parseU32(r.PathValue("displayId"))
	if !ok1 || !ok2 {
		http.NotFound(w, r)
		return
	}
	mapped, err := mapWotLKDisplay(r.Context(), entry, displayID)
	if err != nil {
		s.log.Error("armory display map", "err", err, "entry", entry, "display", displayID)
		http.Error(w, "Could not map the item.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(struct {
		NewDisplayID uint32 `json:"newDisplayId"`
	}{mapped})
}

func mapWotLKDisplay(ctx context.Context, entry, displayID uint32) (uint32, error) {
	key := strconv.FormatUint(uint64(entry), 10) + "/" + strconv.FormatUint(uint64(displayID), 10)
	if v, ok := displayMapLookup(key); ok {
		return v, nil
	}
	mapped, err := fetchWotLKDisplay(ctx, entry, displayID)
	if err != nil {
		return 0, err
	}
	displayMapStore(key, mapped)
	return mapped, nil
}

func displayMapLookup(key string) (uint32, bool) {
	displayMapMu.Lock()
	defer displayMapMu.Unlock()
	v, ok := displayMapCache[key]
	return v, ok
}

func displayMapStore(key string, v uint32) {
	displayMapMu.Lock()
	defer displayMapMu.Unlock()
	if len(displayMapCache) >= displayMapCacheMax {
		displayMapCache = map[string]uint32{}
	}
	displayMapCache[key] = v
}

func displayMapReset() {
	displayMapMu.Lock()
	displayMapCache = map[string]uint32{}
	displayMapMu.Unlock()
}

func fetchWotLKDisplay(ctx context.Context, entry, displayID uint32) (uint32, error) {
	u, err := url.JoinPath(
		displayMapUpstream,
		strconv.FormatUint(uint64(entry), 10),
		strconv.FormatUint(uint64(displayID), 10),
	)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, displayMapTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "GatehouseArmory/1.0")
	req.Header.Set("Accept", "application/json")
	res, err := displayMapClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return displayID, nil
	}
	if res.StatusCode >= 400 {
		return 0, errors.New("display map status")
	}
	var parsed struct {
		NewDisplayID uint32 `json:"newDisplayId"`
		Data         *struct {
			NewDisplayID uint32 `json:"newDisplayId"`
		} `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, displayMapMaxBytes))
	if err := dec.Decode(&parsed); err != nil {
		return 0, err
	}
	if parsed.Data != nil && parsed.Data.NewDisplayID != 0 {
		return parsed.Data.NewDisplayID, nil
	}
	if parsed.NewDisplayID != 0 {
		return parsed.NewDisplayID, nil
	}
	return displayID, nil
}
