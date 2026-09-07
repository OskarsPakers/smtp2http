package main

import (
	"log/slog"
	"os"
	"strings"
)

// newLogger builds the process logger from -log.level and -log.format. An
// unrecognised value falls back to the default rather than refusing to start:
// a typo in a log flag should not stop a mail server from accepting mail.
func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level

	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	if strings.EqualFold(format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}

	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
