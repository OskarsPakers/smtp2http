package main

import (
	"bytes"
	"encoding/json"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blitiri.com.ar/go/spf"
)

// TestGoldenPayloads feeds every corpus message through the parser and payload
// builder and compares the result to the payload the published 1.0.0 image
// produced for it. The webhook JSON is a public contract; this is what makes
// swapping the SMTP server, parser and SPF library reviewable.
//
// Regenerate the goldens with testdata/capture.py -- see testdata/README.md.
func TestGoldenPayloads(t *testing.T) {
	files, err := filepath.Glob("testdata/corpus/*.eml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}

	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".eml")

		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			msg, err := ParseEmail(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			// The envelope addresses capture.py used, and spf.None because
			// sender.invalid publishes no SPF record. Keeping the verdict an
			// input rather than a lookup keeps this test off the network.
			got := buildPayload(&envelope{
				from:    &mail.Address{Address: "alice@sender.invalid"},
				to:      &mail.Address{Address: "bob@example.com"},
				message: msg,
				spf:     spf.None,
			})

			want, err := os.ReadFile(filepath.Join("testdata/golden", name+".json"))
			if err != nil {
				t.Fatalf("golden: %v", err)
			}

			if diff := jsonDiff(t, want, got); diff != "" {
				t.Errorf("payload changed vs 1.0.0:\n%s", diff)
			}
		})
	}
}

// jsonDiff compares two payloads structurally, so key order and formatting do
// not matter -- only the content does.
func jsonDiff(t *testing.T, wantRaw []byte, got any) string {
	t.Helper()

	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	var wantAny, gotAny any
	if err := json.Unmarshal(wantRaw, &wantAny); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(gotRaw, &gotAny); err != nil {
		t.Fatal(err)
	}

	wantNorm, _ := json.MarshalIndent(wantAny, "", "  ")
	gotNorm, _ := json.MarshalIndent(gotAny, "", "  ")

	if bytes.Equal(wantNorm, gotNorm) {
		return ""
	}

	return "--- want (1.0.0)\n" + string(wantNorm) + "\n+++ got\n" + string(gotNorm)
}

// FuzzParseEmail exercises the vendored parser against arbitrary input. It now
// lives in this repository, and it is the component that touches untrusted
// bytes first, so it should not panic on anything.
func FuzzParseEmail(f *testing.F) {
	files, _ := filepath.Glob("testdata/corpus/*.eml")
	for _, path := range files {
		if raw, err := os.ReadFile(path); err == nil {
			f.Add(raw)
		}
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Errors are expected and fine; panics are not.
		_, _ = ParseEmail(bytes.NewReader(raw))
	})
}
