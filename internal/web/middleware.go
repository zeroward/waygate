package web

import (
	"net/http"
	"runtime/debug"
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
	script := "'self'"
	frame := "'none'"
	switch s.captcha.Provider() {
	case "turnstile":
		script += " https://challenges.cloudflare.com"
		frame = "https://challenges.cloudflare.com"
	case "hcaptcha":
		script += " https://js.hcaptcha.com https://newassets.hcaptcha.com"
		frame = "https://newassets.hcaptcha.com https://hcaptcha.com"
	}
	return "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; style-src 'self'; script-src " + script + "; frame-src " + frame + "; connect-src 'self'"
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}
