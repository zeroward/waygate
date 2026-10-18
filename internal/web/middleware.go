package web

import (
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/session"
)

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &statusWriter{ResponseWriter: w, code: 200}
		w = lw
		defer func() {
			s.log.Info("http", "method", r.Method, "path", r.URL.Path,
				"status", lw.code, "ms", time.Since(start).Milliseconds(), "ip", s.ip(r))
		}()
		defer func() {
			if rec := recover(); rec != nil {
				if isManagementAPIPath(r.URL.Path) {
					s.log.Error("management API panic", "path", r.URL.Path, "stack", string(debug.Stack()))
					writeManagementError(w, r, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, session.ErrRevoked) {
					http.Error(w, "Please sign in again.", 401)
					return
				}
				s.log.Error("panic", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
				http.Error(w, "The Frozen Throne is silent. Try again shortly.", http.StatusInternalServerError)
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), publickey-credentials-get=(self), publickey-credentials-create=(self)")
		w.Header().Set("Content-Security-Policy", s.csp())
		if s.cfg.SessionSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// Machine credentials never enter browser session or identity handling.
		if isManagementAPIPath(r.URL.Path) {
			s.managementAPI(lw, r)
			return
		}
		if r.URL.Path != "/healthz" && !strings.HasPrefix(r.URL.Path, "/static/") {
			r = s.sessions.Bind(w, r)
			w.Header().Set("Cache-Control", "no-store")
			if !s.validateSession(w, r) {
				return
			}
		}
		h := sessionForLocalCheck(s, w, r)
		if h != nil && h.Snapshot().User != nil && localAuthPath(r.URL.Path) {
			current, err := s.id.GetByID(r.Context(), h.Snapshot().User.ID)
			if err != nil {
				http.Error(w, "Account verification unavailable.", 503)
				return
			}
			if current.AuthMode == identity.AuthSSO {
				http.Error(w, "Use single sign-on to manage authentication.", 403)
				return
			}
		}
		next.ServeHTTP(lw, r)
	})
}

func (s *Server) csp() string {
	const zam = "https://wow.zamimg.com"
	// *.hcaptcha.com is one DNS label; challenge assets are on *.w.hcaptcha.com.
	const hcap = "https://hcaptcha.com https://*.hcaptcha.com https://*.w.hcaptcha.com"
	script := "'self'"
	frame := "'none'"
	connect := "'self'"
	style := "'self' " + zam
	img := "'self' data: blob: " + zam
	font := "'self' data: " + zam
	switch s.captcha.Provider() {
	case "turnstile":
		script += " https://challenges.cloudflare.com"
		frame = "https://challenges.cloudflare.com"
		connect += " https://challenges.cloudflare.com"
	case "hcaptcha":
		script += " " + hcap
		frame = hcap
		connect += " " + hcap
		style += " " + hcap
		img += " " + hcap
	}
	// ZamModelViewer loads viewer.css (and related images/fonts) from wow.zamimg.com
	// even when CONTENT_PATH is our same-origin proxy. Scripts stay 'self'.
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"img-src " + img,
		"font-src " + font,
		"worker-src 'self' blob:",
		"style-src " + style,
		"script-src " + script,
		"frame-src " + frame,
		"connect-src " + connect,
	}, "; ")
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// Defense in depth for every local credential management route.
func localAuthPath(p string) bool {
	return p == "/account/password" || p == "/account/wow/unlock" || p == "/account/login" || strings.HasPrefix(p, "/account/login/") || strings.HasPrefix(p, "/account/totp/") || strings.HasPrefix(p, "/account/passkey/") || strings.HasPrefix(p, "/account/reauth/") || strings.HasPrefix(p, "/account/reset")
}
func sessionForLocalCheck(s *Server, w http.ResponseWriter, r *http.Request) *session.Session {
	if !localAuthPath(r.URL.Path) {
		return nil
	}
	return s.sessions.GetOrCreate(w, r)
}
