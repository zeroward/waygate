package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/kb"
)

func (s *Server) maintenanceBanner(ctx context.Context) string {
	if s.kb == nil {
		return ""
	}
	return s.kb.ActiveBanner(ctx)
}

func (s *Server) bannerMessage(ctx context.Context) string {
	if s.kb == nil {
		return ""
	}
	v, _ := s.kb.GetSetting(ctx, kb.SettingMaintenanceMessage)
	return v
}

func (s *Server) bannerUntilLocal(ctx context.Context) string {
	if s.kb == nil {
		return ""
	}
	v, _ := s.kb.GetSetting(ctx, kb.SettingMaintenanceUntil)
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04")
}

func (s *Server) staffBannerPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	msg := kb.ClipBanner(r.FormValue("message"))
	untilRaw := strings.TrimSpace(r.FormValue("until"))
	until := ""
	if untilRaw != "" {
		parsed, err := parseBannerUntil(untilRaw)
		if err != nil {
			s.flashRedirect(w, r, "/staff", "error", "Until must be a date and time.")
			return
		}
		until = parsed
	}
	if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceMessage, msg); err != nil {
		s.log.Error("banner", "err", err)
		s.flashRedirect(w, r, "/staff", "error", "Could not save the banner.")
		return
	}
	if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceUntil, until); err != nil {
		s.log.Error("banner until", "err", err)
		s.flashRedirect(w, r, "/staff", "error", "Could not save the banner.")
		return
	}
	if msg == "" {
		s.logStaff(sess.User.Username, "banner-clear", "")
		s.flashRedirect(w, r, "/staff", "success", "Maintenance banner cleared.")
		return
	}
	s.logStaff(sess.User.Username, "banner-set", msg)
	s.flashRedirect(w, r, "/staff", "success", "Maintenance banner set.")
}

func parseBannerUntil(raw string) (string, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", errBadUntil
}

var errBadUntil = errUntil{}

type errUntil struct{}

func (errUntil) Error() string { return "bad until" }
