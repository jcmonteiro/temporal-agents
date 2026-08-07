package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestParseServeFlagsDefaultsToLoopback pins the security boundary: the API is
// unauthenticated and contains prompts, so running the command with no option must
// never expose it to the network.
func TestParseServeFlagsDefaultsToLoopback(t *testing.T) {
	got, err := parseServeFlags(nil)
	if err != nil {
		t.Fatalf("parseServeFlags: %v", err)
	}
	if got.address != "127.0.0.1:8973" {
		t.Fatalf("address = %q, want the loopback default 127.0.0.1:8973", got.address)
	}
	if got.webDir != "web/dist" {
		t.Errorf("web directory = %q, want web/dist", got.webDir)
	}
	if len(got.allowedOrigins) != 0 {
		t.Errorf("allowed origins = %v, want none by default", got.allowedOrigins)
	}
}

// TestParseServeFlagsMakesExposureExplicit pins the only way the bind can widen: an
// operator must type --addr, and every browser origin must likewise be listed.
func TestParseServeFlagsMakesExposureExplicit(t *testing.T) {
	got, err := parseServeFlags([]string{
		"--addr", "0.0.0.0:9000",
		"--web-dir=",
		"--allow-origin", "http://localhost:5173",
		"--allow-origin=https://hub.example.test",
	})
	if err != nil {
		t.Fatalf("parseServeFlags: %v", err)
	}
	if got.address != "0.0.0.0:9000" {
		t.Errorf("address = %q, want 0.0.0.0:9000", got.address)
	}
	if got.webDir != "" {
		t.Errorf("web directory = %q, want JSON-only", got.webDir)
	}
	want := []string{"http://localhost:5173", "https://hub.example.test"}
	if len(got.allowedOrigins) != len(want) {
		t.Fatalf("allowed origins = %v, want %v", got.allowedOrigins, want)
	}
	for i := range want {
		if got.allowedOrigins[i] != want[i] {
			t.Errorf("allowed origin %d = %q, want %q", i, got.allowedOrigins[i], want[i])
		}
	}
}

// TestParseServeFlagsRefusesAmbiguousInput pins that an empty listener, an empty
// origin and positional arguments are all rejected rather than interpreted.
func TestParseServeFlagsRefusesAmbiguousInput(t *testing.T) {
	cases := map[string][]string{
		"empty address":       {"--addr="},
		"empty origin":        {"--allow-origin", "  "},
		"positional argument": {"unexpected"},
		"unknown option":      {"--listen", ":9000"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseServeFlags(args); err == nil {
				t.Fatalf("parseServeFlags(%v) = nil error, want a refusal", args)
			}
		})
	}
}

// TestServeHelpExplainsTheSecurityBoundary keeps the operational contract in the
// command itself: loopback, the versioned API path, the contract location and the
// environment required to run it must be visible without reading this repository.
func TestServeHelpExplainsTheSecurityBoundary(t *testing.T) {
	var out bytes.Buffer
	serveHelp(&out)
	for _, want := range []string{
		"127.0.0.1:8973",
		"/api/v1/openapi.json",
		"DATABASE_URL",
		"TEMPORAL_ADDRESS",
		"--allow-origin",
		"explicit\nexposure opt-in",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not contain %q:\n%s", want, out.String())
		}
	}
}
