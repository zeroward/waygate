package wg

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	HealthFile   = "health"
	HealthMaxAge = 15 * time.Second
)

func WriteHealth(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	p := filepath.Join(dir, HealthFile)
	if err := os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return err
	}
	_ = os.Chown(p, 1000, 1000)
	return nil
}

func HealthFresh(dir string, maxAge time.Duration) bool {
	if strings.TrimSpace(dir) == "" || maxAge <= 0 {
		return false
	}
	// health is written after a successful sync. wg0.conf is rewritten on
	// each agent tick, so its mtime works before that file exists.
	for _, name := range []string{HealthFile, "wg0.conf"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if time.Since(st.ModTime()) <= maxAge {
			return true
		}
	}
	return false
}

// Probe reports whether the WireGuard agent looks alive. Shared volume heartbeat
// is checked first (waygate and wg-agent do not share a network namespace).
func Probe(ctx context.Context, listen, dir string) bool {
	if HealthFresh(dir, HealthMaxAge) {
		return true
	}
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return false
	}
	u := "http://" + listen + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 400 * time.Millisecond}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8))
	return strings.HasPrefix(strings.TrimSpace(string(b)), "ok")
}
