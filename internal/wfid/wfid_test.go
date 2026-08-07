package wfid

import (
	"testing"

	"github.com/google/uuid"
)

// TestClassify pins the ID convention, in particular the fleet parent/child
// disambiguation: a parent is "fleet-<uuid>" while a per-node child is
// "fleet-<uuid>-<nodeID>", told apart by whether the text after the prefix parses
// as a bare UUID.
func TestClassify(t *testing.T) {
	id := uuid.NewString()

	cases := []struct {
		name string
		in   string
		want Class
	}{
		{"fleet parent (bare uuid)", "fleet-" + id, ClassFleet},
		{"fleet node (uuid + node suffix)", "fleet-" + id + "-core", ClassFleetNode},
		{"fleet node (multi-segment suffix)", "fleet-" + id + "-rest-api", ClassFleetNode},
		{"fleet plan", "fleet-plan-" + id, ClassFleetPlan},
		{"develop", "develop-" + id, ClassDevelop},
		{"review", "review-" + id, ClassReview},
		{"pilot", "pilot-" + id, ClassPilot},
		{"open-pr", "open-pr-" + id, ClassOpenPR},
		{"schedule", "schedule-" + id, ClassSchedule},
		{"unknown falls back to a run", "something-else", ClassRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in); got != tc.want {
				t.Fatalf("Classify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFleetNodeIDRoundTrip pins the composition and its inverse against each
// other: whatever FleetNodeWorkflowID builds, FleetNodeID must take apart, since
// the read side matches plan nodes to executions by exactly that pairing.
func TestFleetNodeIDRoundTrip(t *testing.T) {
	fleetID := "fleet-" + uuid.NewString()
	for _, nodeID := range []string{"core", "rest-api", "a_b-1"} {
		wf := FleetNodeWorkflowID(fleetID, nodeID)
		got, ok := FleetNodeID(fleetID, wf)
		if !ok || got != nodeID {
			t.Fatalf("FleetNodeID(%q, %q) = (%q, %v), want (%q, true)", fleetID, wf, got, ok, nodeID)
		}
	}
}

// TestFleetNodeIDRejectsForeignIDs keeps the reconciliation honest: an execution
// that is not a child of this fleet must not be attributed to one of its nodes.
func TestFleetNodeIDRejectsForeignIDs(t *testing.T) {
	fleetID := "fleet-" + uuid.NewString()
	cases := []string{
		fleetID,                            // the parent itself, not a node
		"fleet-" + uuid.NewString() + "-x", // another fleet's node
		"run-" + uuid.NewString(),
		"",
	}
	for _, in := range cases {
		if got, ok := FleetNodeID(fleetID, in); ok {
			t.Errorf("FleetNodeID(%q, %q) = (%q, true), want ok=false", fleetID, in, got)
		}
	}
}

// TestFleetNodeIDWithoutAFleet guards the empty-fleet-ID case: every workflow ID
// starts with "-" prefixed by nothing, so an unguarded prefix match would claim
// every execution as a node of the empty fleet.
func TestFleetNodeIDWithoutAFleet(t *testing.T) {
	if got, ok := FleetNodeID("", "-core"); ok {
		t.Fatalf(`FleetNodeID("", "-core") = (%q, true), want ok=false`, got)
	}
}
