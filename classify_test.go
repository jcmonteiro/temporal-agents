package main

import (
	"testing"

	"github.com/google/uuid"
)

// TestClassifyWorkflow pins the ID-prefix labeling, in particular the
// fleet parent/child disambiguation: a fleet parent ID is "fleet-<uuid>" while
// a per-node child is "fleet-<uuid>-<nodeid>". The two are told apart by whether
// the text after the "fleet-" prefix parses as a bare UUID, so the child suffix
// (which makes uuid.Parse fail) is what distinguishes a node from its parent.
func TestClassifyWorkflow(t *testing.T) {
	// A stable canonical UUID as google/uuid renders it, matching how fleet IDs
	// are constructed at runtime.
	id := uuid.NewString()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"fleet parent (bare uuid)", "fleet-" + id, "fleet"},
		{"fleet node (uuid + node suffix)", "fleet-" + id + "-core", "fleet-node"},
		{"fleet node (multi-segment suffix)", "fleet-" + id + "-rest-api", "fleet-node"},
		{"fleet plan", "fleet-plan-" + id, "fleet-plan"},
		{"develop", "develop-" + id, "develop"},
		{"review", "review-" + id, "review"},
		{"pilot", "pilot-" + id, "pilot"},
		{"open-pr", "open-pr-" + id, "open-pr"},
		{"schedule", "schedule-daily", "schedule"},
		{"unknown", "something-else", "run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyWorkflow(tc.in); got != tc.want {
				t.Fatalf("classifyWorkflow(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
