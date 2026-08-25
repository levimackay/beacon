package config

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger returns a structured logger that redacts any attribute whose key
// looks like a credential. Beacon's whole security posture rests on one
// bearer token, so it must never reach a log file, a crash report or a
// support bundle.
func NewLogger(w io.Writer, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redact,
	}))
}

var sensitiveKeys = []string{"token", "secret", "password", "authorization", "apikey", "api_key", "credential"}

func redact(_ []string, a slog.Attr) slog.Attr {
	k := strings.ToLower(a.Key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return slog.String(a.Key, "[redacted]")
		}
	}
	return a
}
