package logx

import (
	"log/slog"
	"os"
	"strings"
)

func New(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}

// Redact returns a copy of s with secrets replaced. Used if a SOAP/SQL error
// string might echo a command. Passwords must never be logged.
func Redact(s string, secrets ...string) string {
	out := s
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		out = strings.ReplaceAll(out, sec, "***")
	}
	return out
}
