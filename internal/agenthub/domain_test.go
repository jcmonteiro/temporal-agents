package agenthub

import (
	"strings"
	"testing"
)

// TestAggregateStatus pins the published precedence, including the empty fleet and
// the mixed states the precedence exists to settle. A consumer reads the
// aggregated status straight off the API, so this table is the contract.
func TestAggregateStatus(t *testing.T) {
	cases := []struct {
		name string
		in   []WorkStatus
		want WorkStatus
	}{
		{"no nodes at all", nil, StatusTodo},
		{"a failure outranks everything", []WorkStatus{StatusDone, StatusInProgress, StatusFailed, StatusWaitingInput}, StatusFailed},
		{"needing a human outranks progress", []WorkStatus{StatusInProgress, StatusWaitingInput, StatusDone}, StatusWaitingInput},
		{"progress outranks a skip", []WorkStatus{StatusPaused, StatusInProgress, StatusTodo}, StatusInProgress},
		{"a skip outranks waiting", []WorkStatus{StatusPaused, StatusWaiting, StatusTodo}, StatusPaused},
		{"every node done", []WorkStatus{StatusDone, StatusDone}, StatusDone},
		{"started but nothing running", []WorkStatus{StatusDone, StatusTodo, StatusWaiting}, StatusInProgress},
		{"nothing started yet", []WorkStatus{StatusTodo, StatusWaiting}, StatusTodo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateStatus(nodesWith(tc.in...)); got != tc.want {
				t.Fatalf("AggregateStatus(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNodeProgress pins that a skipped or blocked node counts against the plan but
// never for it: progress must not creep up because work was abandoned.
func TestNodeProgress(t *testing.T) {
	got := NodeProgress(nodesWith(StatusDone, StatusDone, StatusPaused, StatusWaitingInput, StatusWaiting))
	if got.Done != 2 || got.Total != 5 {
		t.Fatalf("NodeProgress = %d/%d, want 2/5", got.Done, got.Total)
	}
	if fraction := got.Fraction(); fraction != 0.4 {
		t.Fatalf("Fraction = %v, want 0.4", fraction)
	}
	if empty := (Progress{}).Fraction(); empty != 0 {
		t.Fatalf("an empty plan's Fraction = %v, want 0", empty)
	}
}

// TestDeriveNodeStatuses pins the derivation for the nodes that have no execution:
// the only honest source for them is their dependencies, and a chain of nodes
// behind a failed prerequisite must all be reported as skipped, not just the first.
func TestDeriveNodeStatuses(t *testing.T) {
	plan := Plan{Nodes: []PlanNode{
		{ID: "foundation"},
		{ID: "api", DependsOn: []string{"foundation"}},
		{ID: "ui", DependsOn: []string{"api"}},
		{ID: "docs", DependsOn: []string{"foundation"}},
		{ID: "standalone"},
	}}

	cases := []struct {
		name     string
		outcomes map[string]ExecutionOutcome
		want     map[string]WorkStatus
	}{
		{
			name:     "nothing has run",
			outcomes: nil,
			want: map[string]WorkStatus{
				"foundation": StatusTodo, "api": StatusWaiting, "ui": StatusWaiting,
				"docs": StatusWaiting, "standalone": StatusTodo,
			},
		},
		{
			name:     "a prerequisite is running",
			outcomes: map[string]ExecutionOutcome{"foundation": OutcomeRunning},
			want: map[string]WorkStatus{
				"foundation": StatusInProgress, "api": StatusWaiting, "ui": StatusWaiting,
				"docs": StatusWaiting, "standalone": StatusTodo,
			},
		},
		{
			name:     "a prerequisite succeeded, so its dependents are runnable",
			outcomes: map[string]ExecutionOutcome{"foundation": OutcomeSucceeded},
			want: map[string]WorkStatus{
				"foundation": StatusDone, "api": StatusTodo, "ui": StatusWaiting,
				"docs": StatusTodo, "standalone": StatusTodo,
			},
		},
		{
			name:     "a failure propagates down the whole chain",
			outcomes: map[string]ExecutionOutcome{"foundation": OutcomeFailed},
			want: map[string]WorkStatus{
				"foundation": StatusFailed, "api": StatusPaused, "ui": StatusPaused,
				"docs": StatusPaused, "standalone": StatusTodo,
			},
		},
		{
			name:     "a node needing a human blocks its dependents",
			outcomes: map[string]ExecutionOutcome{"foundation": OutcomeSucceeded, "api": OutcomeBlocked},
			want: map[string]WorkStatus{
				"foundation": StatusDone, "api": StatusWaitingInput, "ui": StatusPaused,
				"docs": StatusTodo, "standalone": StatusTodo,
			},
		},
		{
			name:     "a recorded skip is a skip, and so is everything after it",
			outcomes: map[string]ExecutionOutcome{"foundation": OutcomeSucceeded, "api": OutcomeSkipped},
			want: map[string]WorkStatus{
				"foundation": StatusDone, "api": StatusPaused, "ui": StatusPaused,
				"docs": StatusTodo, "standalone": StatusTodo,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveNodeStatuses(plan, tc.outcomes)
			if len(got) != len(tc.want) {
				t.Fatalf("derived %d statuses, want %d", len(got), len(tc.want))
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("node %q = %q, want %q", id, got[id], want)
				}
			}
		})
	}
}

// TestDeriveNodeStatusesTerminatesOnACycle guards the reader against a plan the
// validation gate would reject: a cycle must be reported, not looped on.
func TestDeriveNodeStatusesTerminatesOnACycle(t *testing.T) {
	plan := Plan{Nodes: []PlanNode{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c"},
	}}
	got := DeriveNodeStatuses(plan, nil)
	if got["a"] != StatusWaiting || got["b"] != StatusWaiting {
		t.Fatalf("nodes in a cycle = %q/%q, want both waiting", got["a"], got["b"])
	}
	if got["c"] != StatusTodo {
		t.Fatalf("node outside the cycle = %q, want todo", got["c"])
	}
}

// TestDeriveNodeStatusesWithAnUnknownDependency keeps a plan whose edge points at
// a node that is not in it readable: the node is reported as waiting — it does
// depend on something that has not succeeded — and the rest of the graph is still
// derived instead of stalling behind an edge nothing can satisfy.
func TestDeriveNodeStatusesWithAnUnknownDependency(t *testing.T) {
	plan := Plan{Nodes: []PlanNode{
		{ID: "a", DependsOn: []string{"gone"}},
		{ID: "b"},
	}}
	got := DeriveNodeStatuses(plan, nil)
	if got["a"] != StatusWaiting {
		t.Fatalf("node with an unknown dependency = %q, want waiting", got["a"])
	}
	if got["b"] != StatusTodo {
		t.Fatalf("the rest of the plan = %q, want todo", got["b"])
	}
}

// TestScheduleStatus pins the schedule mapping, including that pausedness wins:
// what an operator needs to see about a paused schedule is that it will not fire,
// not how its last run went.
func TestScheduleStatus(t *testing.T) {
	cases := []struct {
		name    string
		paused  bool
		running int
		last    ExecutionOutcome
		want    WorkStatus
	}{
		{"paused outranks a successful action", true, 0, OutcomeSucceeded, StatusPaused},
		{"paused outranks a running action", true, 1, OutcomeSucceeded, StatusPaused},
		{"an action in flight", false, 1, OutcomeFailed, StatusInProgress},
		{"never fired", false, 0, "", StatusTodo},
		{"latest action succeeded", false, 0, OutcomeSucceeded, StatusDone},
		{"latest action failed", false, 0, OutcomeFailed, StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScheduleStatus(tc.paused, tc.running, tc.last); got != tc.want {
				t.Fatalf("ScheduleStatus(%v, %d, %q) = %q, want %q", tc.paused, tc.running, tc.last, got, tc.want)
			}
		})
	}
}

// TestOutcomeWorkStatus pins the one place the two vocabularies meet, including
// that an outcome nothing recognises is reported as failed rather than invented
// into a friendlier state.
func TestOutcomeWorkStatus(t *testing.T) {
	cases := map[ExecutionOutcome]WorkStatus{
		OutcomeRunning:   StatusInProgress,
		OutcomeSucceeded: StatusDone,
		OutcomeFailed:    StatusFailed,
		OutcomeBlocked:   StatusWaitingInput,
		OutcomeSkipped:   StatusPaused,
		"something-new":  StatusFailed,
	}
	for outcome, want := range cases {
		if got := outcome.WorkStatus(); got != want {
			t.Errorf("%q.WorkStatus() = %q, want %q", outcome, got, want)
		}
	}
}

// TestUpNext pins the "up next" queue: runnable work first, then work that is
// merely waiting, and nothing that has started or finished.
func TestUpNext(t *testing.T) {
	fleet := Fleet{Nodes: []FleetNode{
		{ID: "running", Status: StatusInProgress},
		{ID: "waiting", Status: StatusWaiting},
		{ID: "done", Status: StatusDone},
		{ID: "ready", Status: StatusTodo},
		{ID: "skipped", Status: StatusPaused},
	}}
	got := fleet.UpNext()
	if len(got) != 2 || got[0].ID != "ready" || got[1].ID != "waiting" {
		t.Fatalf("UpNext = %v, want [ready waiting]", ids(got))
	}
}

// TestDismissalIdentity pins the derived identifier and its parsing: dismissing
// the same item twice must address the same resource, which is what makes the
// write idempotent for a client that retries.
func TestDismissalIdentity(t *testing.T) {
	d := Dismissal{Kind: KindRun, ItemID: "run-1"}
	if d.ID() != "run:run-1" {
		t.Fatalf("ID() = %q, want %q", d.ID(), "run:run-1")
	}
	kind, itemID, err := ParseDismissalID(d.ID())
	if err != nil || kind != KindRun || itemID != "run-1" {
		t.Fatalf("ParseDismissalID(%q) = (%q, %q, %v), want (run, run-1, nil)", d.ID(), kind, itemID, err)
	}
}

// TestValidateDismissalTarget pins what may be dismissed at all: a schedule never
// can (it has no finished state), and an identity that could not appear in a URL
// is refused before it reaches the store.
func TestValidateDismissalTarget(t *testing.T) {
	if err := ValidateDismissalTarget(KindFleet, "fleet-1"); err != nil {
		t.Fatalf("a fleet must be dismissible: %v", err)
	}
	if err := ValidateItemID("run-1"); err != nil {
		t.Fatalf("a workflow id must be addressable: %v", err)
	}
	cases := map[string]struct {
		kind   ItemKind
		itemID string
	}{
		"a schedule has no finished state": {KindSchedule, "schedule-1"},
		"an unknown kind":                  {ItemKind("satellite"), "x"},
		"an empty item":                    {KindRun, "  "},
		"a path separator":                 {KindRun, "run/1"},
		"an escape":                        {KindRun, "run%2f1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateDismissalTarget(tc.kind, tc.itemID); err == nil {
				t.Fatalf("ValidateDismissalTarget(%q, %q) = nil, want an error", tc.kind, tc.itemID)
			}
		})
	}
	for _, itemID := range []string{"", "run/1", "run%2f1", "run\x1b[2J", "run\u00851", strings.Repeat("x", maxItemIDLength+1)} {
		if err := ValidateItemID(itemID); err == nil {
			t.Errorf("ValidateItemID(%q) = nil, want an error", itemID)
		}
	}
}

// TestValidateLimit pins the paging contract: no limit means the default, and a
// limit above the cap is refused rather than quietly reduced, so a response never
// silently disagrees with the request.
func TestValidateLimit(t *testing.T) {
	if got, err := ValidateLimit(0); err != nil || got != DefaultLimit {
		t.Fatalf("ValidateLimit(0) = (%d, %v), want (%d, nil)", got, err, DefaultLimit)
	}
	if got, err := ValidateLimit(10); err != nil || got != 10 {
		t.Fatalf("ValidateLimit(10) = (%d, %v), want (10, nil)", got, err)
	}
	for _, in := range []int{-1, MaxLimit + 1} {
		if _, err := ValidateLimit(in); err == nil {
			t.Errorf("ValidateLimit(%d) = nil error, want a refusal", in)
		}
	}
}

// TestTerminalStatuses pins which statuses end an item's life, since that is what
// decides whether it can be dismissed.
func TestTerminalStatuses(t *testing.T) {
	terminal := map[WorkStatus]bool{StatusDone: true, StatusFailed: true}
	for _, status := range WorkStatuses() {
		if got := status.Terminal(); got != terminal[status] {
			t.Errorf("%q.Terminal() = %v, want %v", status, got, terminal[status])
		}
	}
	if (Schedule{Status: StatusDone}).Dismissible() {
		t.Error("a schedule must never be dismissible")
	}
}

// nodesWith builds a node list carrying the given statuses, for the aggregation
// tables.
func nodesWith(statuses ...WorkStatus) []FleetNode {
	nodes := make([]FleetNode, 0, len(statuses))
	for i, status := range statuses {
		nodes = append(nodes, FleetNode{ID: string(rune('a' + i)), Status: status})
	}
	return nodes
}

// ids renders node IDs for a failure message.
func ids(nodes []FleetNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
