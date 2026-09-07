package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// secrets that must never survive redaction, in the places providers put them
var secretURLs = []string{
	"https://hooks.slack.com/services/T00000/B00000/SUPERSECRETTOKEN",
	"https://discord.com/api/webhooks/123456/aVerySecretWebhookToken",
	"https://webhook.site/8f14e45f-ceea-467a-9e0f-0f4c9b2b3a11",
	"https://api.example.com/hook?api_key=SUPERSECRETTOKEN",
	"https://api.example.com/hook?token=SUPERSECRETTOKEN&x=1",
	"https://user:SUPERSECRETTOKEN@api.example.com/hook",
	"http://example.com/hook#SUPERSECRETTOKEN",
}

func TestRedactURLHidesSecrets(t *testing.T) {
	for _, raw := range secretURLs {
		got := redactURL(raw)

		for _, secret := range []string{"SUPERSECRETTOKEN", "aVerySecretWebhookToken",
			"8f14e45f-ceea-467a-9e0f-0f4c9b2b3a11", "T00000", "B00000"} {
			if strings.Contains(got, secret) {
				t.Errorf("redactURL(%q) = %q, leaks %q", raw, got, secret)
			}
		}

		if strings.Contains(got, "?") || strings.Contains(got, "#") {
			t.Errorf("redactURL(%q) = %q, kept query or fragment", raw, got)
		}
	}
}

func TestRedactURLKeepsHost(t *testing.T) {
	cases := map[string]string{
		"https://hooks.slack.com/services/T0/B0/tok": "https://hooks.slack.com",
		"http://localhost:8080/my/webhook":           "http://localhost:8080",
		"https://user:pw@api.example.com/hook?k=v":   "https://api.example.com",
		"not a url at all":                           "(unparseable)",
		"":                                           "(unparseable)",
	}

	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Go embeds the full request URL in *url.Error, so a transport failure would
// otherwise publish the webhook secret on every failed delivery.
func TestRedactErrHidesSecrets(t *testing.T) {
	for _, raw := range secretURLs {
		err := &url.Error{
			Op:  "Post",
			URL: raw,
			Err: fmt.Errorf("dial tcp 10.0.0.1:443: connect: connection refused"),
		}

		got := redactErr(err)
		if strings.Contains(got, "SUPERSECRETTOKEN") || strings.Contains(got, "aVerySecretWebhookToken") {
			t.Errorf("redactErr for %q leaked: %s", raw, got)
		}

		if !strings.Contains(got, "connection refused") {
			t.Errorf("redactErr dropped the useful part: %s", got)
		}
	}
}
