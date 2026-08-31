package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	mvMaxBytes = 16 << 20
	mvTimeout  = 15 * time.Second
)

var (
	mvUpstream = "https://wow.zamimg.com/modelviewer/wrath/"
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
	p = clean
	u, err := url.JoinPath(mvUpstream, p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mvTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		http.Error(w, "Could not load the model.", http.StatusBadGateway)
		return
	}
	req.Header.Set("User-Agent", "GatehouseArmory/1.0")
	if ae := r.Header.Get("Accept"); ae != "" {
		req.Header.Set("Accept", ae)
	}
	res, err := mvClient.Do(req)
	if err != nil {
		s.log.Error("armory model proxy", "err", err, "path", p)
		http.Error(w, "Could not load the model.", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
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
