package web

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
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
		start := time.Now()
		lw := &statusWriter{ResponseWriter: w, code: 200}
		if r.URL.Path != "/healthz" && !strings.HasPrefix(r.URL.Path, "/static/") {
			sess := s.sessions.GetOrCreate(w, r)
			defer s.sessions.SaveLatest(sess)
		}
		next.ServeHTTP(lw, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lw.code,
			"ms", time.Since(start).Milliseconds(),
			"ip", s.ip(r),
		)
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
