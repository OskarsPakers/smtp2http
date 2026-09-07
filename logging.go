package main

import (
	"errors"
	"log/slog"
	"net/url"
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

// redactURL reduces a webhook URL to scheme://host for logging.
//
// Everything else is withheld deliberately. For most webhook providers the
// secret *is* the URL: Slack and Discord put the token in the path, and query
// strings routinely carry ?key= or ?token=. Userinfo carries credentials
// outright. The host is what an operator needs to confirm they are pointed at
// the right place; the rest is a credential.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unparseable)"
	}

	return u.Scheme + "://" + u.Host
}

// redactErr strips the URL out of a *url.Error before it reaches a log. Go
// puts the full request URL in the error text, so logging a transport failure
// verbatim would publish the webhook secret on every failed delivery.
func redactErr(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Op + " " + redactURL(uerr.URL) + ": " + uerr.Err.Error()
	}

	return err.Error()
}
