package main

import (
	"testing"

	"github.com/google/uuid"

	"temporal-agents/internal/execstore"
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
		{"steering session", "steering-" + id, "steering"},
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

// TestClassifyWorkflow_AgreesWithRecordedKinds pins the live view and the durable
// record to the same vocabulary: the label `list`/`watch` derive from a workflow
// ID must read the same as the kind that workflow records itself under, so a row
// in `history` and a row in `list` describe the same thing by the same name.
func TestClassifyWorkflow_AgreesWithRecordedKinds(t *testing.T) {
	id := uuid.NewString()
	cases := map[execstore.Kind]string{
		execstore.KindRun:       "run-" + id,
		execstore.KindDevelop:   "develop-" + id,
		execstore.KindReview:    "review-" + id,
		execstore.KindPilot:     "pilot-" + id,
		execstore.KindFleet:     "fleet-" + id,
		execstore.KindFleetPlan: "fleet-plan-" + id,
		execstore.KindSteering:  "steering-" + id,
	}
	// Every recorded kind must be covered, so a kind added later fails here until
	// its workflow-ID prefix is classified too.
	if len(cases) != len(execstore.Kinds()) {
		t.Fatalf("covered %d kinds, but %d are recorded", len(cases), len(execstore.Kinds()))
	}
	for kind, workflowID := range cases {
		if got := classifyWorkflow(workflowID); got != string(kind) {
			t.Errorf("classifyWorkflow(%q) = %q, want the recorded kind %q", workflowID, got, kind)
		}
	}
}
