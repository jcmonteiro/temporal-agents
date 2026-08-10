package hubrecords

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/fleet"
	"temporal-agents/internal/wfid"
)

// The adapter is exercised through the in-memory execstore fake rather than a
// database: what it owns is the translation between the record and the API's model,
// and the SQL underneath is covered by the store's own suite.

// TestNewRequiresBothPorts pins the wiring: a reader without a plan store would
// report every fleet as having no plan, which is indistinguishable from a fleet
// whose plan was lost.
func TestNewRequiresBothPorts(t *testing.T) {
	store := execstoretest.New()
	if _, err := New(nil, store); err == nil {
		t.Error("New without an execution reader = nil, want an error")
	}
	if _, err := New(store, nil); err == nil {
		t.Error("New without a plan store = nil, want an error")
	}
}

// TestRecordedExecutionsTranslatesARecord pins the field-by-field translation,
// including the class derived from the workflow-ID convention and the prompt
// becoming the item's label.
func TestRecordedExecutionsTranslatesARecord(t *testing.T) {
	started := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	ended := started.Add(time.Hour)
	store := execstoretest.New()
	save(t, store, execstore.Execution{
		WorkflowID: "develop-1", RunID: "r1", Kind: execstore.KindDevelop,
		Prompt: "add the endpoint", StartedAt: started, EndedAt: ended,
		Status: execstore.StatusSucceeded, Tokens: 1234,
		Detail: execstore.Detail{Branch: "feature", PlanID: "plan-1"},
	})

	got := recordedExecutions(t, store, agenthub.RecordQuery{})
	if len(got) != 1 {
		t.Fatalf("got %d executions, want 1", len(got))
	}
	e := got[0]
	switch {
	case e.WorkflowID != "develop-1" || e.RunID != "r1":
		t.Errorf("identity = %q/%q, want develop-1/r1", e.WorkflowID, e.RunID)
	case e.Class != wfid.ClassDevelop:
		t.Errorf("class = %q, want develop", e.Class)
	case e.Outcome != agenthub.OutcomeSucceeded:
		t.Errorf("outcome = %q, want succeeded", e.Outcome)
	case e.Label != "add the endpoint":
		t.Errorf("label = %q, want the prompt", e.Label)
	case e.Tokens != 1234:
		t.Errorf("tokens = %d, want 1234", e.Tokens)
	case e.PlanID != "plan-1":
		t.Errorf("plan id = %q, want plan-1", e.PlanID)
	case !e.StartedAt.Equal(started) || !e.EndedAt.Equal(ended):
		t.Errorf("times = %v/%v, want %v/%v", e.StartedAt, e.EndedAt, started, ended)
	}
}

// TestARecordedPlaceCrossesAsFactsNotAsALocation pins where the place is decided:
// the adapter hands the core the two facts the probe wrote, and nothing else. If it
// built a location here, the rule that a repository is a parent only when git said
// the two differ would live in an adapter instead of in the core.
func TestARecordedPlaceCrossesAsFactsNotAsALocation(t *testing.T) {
	store := execstoretest.New()
	save(t, store, execstore.Execution{
		WorkflowID: "develop-1", RunID: "r1", Kind: execstore.KindDevelop,
		Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{
			Directory:  "/srv/worktrees/pricing-fix",
			Repository: "/srv/repos/pricing",
		},
	})

	got := recordedExecutions(t, store, agenthub.RecordQuery{})

	if len(got) != 1 {
		t.Fatalf("got %d executions, want 1", len(got))
	}
	want := agenthub.RecordedPlace{
		Directory:  "/srv/worktrees/pricing-fix",
		Repository: "/srv/repos/pricing",
	}
	if got[0].Place != want {
		t.Errorf("place = %+v, want %+v", got[0].Place, want)
	}
}

// TestOutcomeFromEveryRecordedStatus pins the whole status mapping, including that
// an unrecognised status is reported as failed rather than as success: the API
// never claims an outcome it cannot evidence.
func TestOutcomeFromEveryRecordedStatus(t *testing.T) {
	cases := map[execstore.Status]agenthub.ExecutionOutcome{
		execstore.StatusRunning:   agenthub.OutcomeRunning,
		execstore.StatusSucceeded: agenthub.OutcomeSucceeded,
		execstore.StatusFailed:    agenthub.OutcomeFailed,
		execstore.StatusSkipped:   agenthub.OutcomeSkipped,
		execstore.Status("new"):   agenthub.OutcomeFailed,
	}
	for status, want := range cases {
		if got := outcomeFrom(status); got != want {
			t.Errorf("outcomeFrom(%q) = %q, want %q", status, got, want)
		}
	}
}

// TestNodeOutcomeCarriesBlockedThrough pins the one outcome only the fleet parent's
// breakdown knows: a blocked node is a recoverable stop that needs a human, while
// its own child execution recorded a plain failure.
func TestNodeOutcomeCarriesBlockedThrough(t *testing.T) {
	cases := map[fleet.NodeStatus]agenthub.ExecutionOutcome{
		fleet.StatusSucceeded: agenthub.OutcomeSucceeded,
		fleet.StatusFailed:    agenthub.OutcomeFailed,
		fleet.StatusBlocked:   agenthub.OutcomeBlocked,
		fleet.StatusSkipped:   agenthub.OutcomeSkipped,
	}
	for status, want := range cases {
		if got := nodeOutcomeFrom(string(status)); got != want {
			t.Errorf("nodeOutcomeFrom(%q) = %q, want %q", status, got, want)
		}
	}

	store := execstoretest.New()
	save(t, store, execstore.Execution{
		WorkflowID: "fleet-1", RunID: "r1", Kind: execstore.KindFleet,
		Status: execstore.StatusSucceeded, StartedAt: time.Unix(1, 0).UTC(),
		Detail: execstore.Detail{Nodes: []execstore.NodeOutcome{
			{ID: "api", Status: string(fleet.StatusBlocked)},
			{ID: "ui", Status: string(fleet.StatusSkipped)},
		}},
	})
	got := recordedExecutions(t, store, agenthub.RecordQuery{Class: wfid.ClassFleet})
	if len(got) != 1 || len(got[0].NodeOutcomes) != 2 {
		t.Fatalf("got %d executions with %d node outcomes, want 1 with 2", len(got), len(got[0].NodeOutcomes))
	}
	if got[0].NodeOutcomes[0].Outcome != agenthub.OutcomeBlocked {
		t.Errorf("node outcome = %q, want blocked", got[0].NodeOutcomes[0].Outcome)
	}
}

// TestKindForEveryClass pins the class-to-kind mapping, in particular that a fleet
// node is recorded as the develop execution it is, and that every recorded kind is
// reachable from some class — otherwise a collection would silently read nothing.
func TestKindForEveryClass(t *testing.T) {
	if got := kindFor(wfid.ClassFleetNode); got != execstore.KindDevelop {
		t.Errorf("kindFor(fleet-node) = %q, want develop", got)
	}
	reached := map[execstore.Kind]bool{}
	for _, class := range []wfid.Class{
		wfid.ClassRun, wfid.ClassDevelop, wfid.ClassReview, wfid.ClassPilot,
		wfid.ClassFleet, wfid.ClassFleetPlan,
	} {
		reached[kindFor(class)] = true
	}
	for _, kind := range execstore.Kinds() {
		if !reached[kind] {
			t.Errorf("no execution class maps to the recorded kind %q", kind)
		}
	}
	if got := kindFor(wfid.ClassSchedule); got != "" {
		t.Errorf("kindFor(schedule) = %q, want no constraint", got)
	}
}

// TestPlanForFollowsTheFleetsOwnPlanHandle is the core of the plan source: the
// handle comes from the run's own record, so the plan returned is the one that run
// executed — not whichever document happens to be lying around.
func TestPlanForFollowsTheFleetsOwnPlanHandle(t *testing.T) {
	store := execstoretest.New()
	plan := fleet.FleetPlan{Goal: "expose pricing", Nodes: []fleet.FleetNode{
		{ID: "domain", Prompt: "the domain"},
		{ID: "rest", Prompt: "the REST layer", DependsOn: []string{"domain"}},
	}}
	savePlan(t, store, execstore.Plan{ID: "plan-7", Goal: plan.Goal, Nodes: len(plan.Nodes), Document: mustJSON(t, plan)})
	save(t, store, execstore.Execution{
		WorkflowID: "fleet-1", RunID: "r1", Kind: execstore.KindFleet,
		Status: execstore.StatusRunning, StartedAt: time.Unix(1, 0).UTC(),
		Detail: execstore.Detail{PlanID: "plan-7"},
	})

	records, err := New(store, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := records.PlanFor(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if got.Goal != "expose pricing" || len(got.Nodes) != 2 {
		t.Fatalf("plan = %q with %d nodes, want the stored plan", got.Goal, len(got.Nodes))
	}
	if got.Nodes[1].ID != "rest" || len(got.Nodes[1].DependsOn) != 1 || got.Nodes[1].DependsOn[0] != "domain" {
		t.Errorf("node = %+v, want rest depending on domain", got.Nodes[1])
	}
}

// TestPlanForWithoutAResolvablePlan pins the honest "no plan" answers: a fleet
// whose record carries no handle, a handle the store no longer has, and an empty
// fleet ID are all reported as ErrNoPlan — which the core renders as a fleet
// without a graph rather than as a failed read.
func TestPlanForWithoutAResolvablePlan(t *testing.T) {
	store := execstoretest.New()
	save(t, store, execstore.Execution{
		WorkflowID: "fleet-nohandle", RunID: "r1", Kind: execstore.KindFleet,
		Status: execstore.StatusSucceeded, StartedAt: time.Unix(1, 0).UTC(),
	})
	save(t, store, execstore.Execution{
		WorkflowID: "fleet-lostplan", RunID: "r2", Kind: execstore.KindFleet,
		Status: execstore.StatusSucceeded, StartedAt: time.Unix(2, 0).UTC(),
		Detail: execstore.Detail{PlanID: "gone"},
	})
	records, err := New(store, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, id := range []string{"fleet-nohandle", "fleet-lostplan", "", "fleet-unknown"} {
		if _, err := records.PlanFor(context.Background(), id); !errors.Is(err, agenthub.ErrNoPlan) {
			t.Errorf("PlanFor(%q) = %v, want ErrNoPlan", id, err)
		}
	}
}

// TestPlanForReportsAStoreOutage pins that an unreachable store is not mistaken
// for a missing plan: one hides a fleet's graph, the other means the read cannot be
// trusted at all.
func TestPlanForReportsAStoreOutage(t *testing.T) {
	outage := errors.New("connection refused")
	records, err := New(execstoretest.Failing(outage), execstoretest.Failing(outage))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = records.PlanFor(context.Background(), "fleet-1")
	if err == nil || errors.Is(err, agenthub.ErrNoPlan) {
		t.Fatalf("PlanFor during an outage = %v, want the outage", err)
	}
}

// recordedExecutions runs the adapter's read and fails the test on error.
func recordedExecutions(t *testing.T, store *execstoretest.Store, q agenthub.RecordQuery) []agenthub.Execution {
	t.Helper()
	records, err := New(store, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := records.RecordedExecutions(context.Background(), q)
	if err != nil {
		t.Fatalf("RecordedExecutions: %v", err)
	}
	return got
}

// save writes an execution into the fake store.
func save(t *testing.T, store *execstoretest.Store, e execstore.Execution) {
	t.Helper()
	if err := store.SaveExecution(context.Background(), e); err != nil {
		t.Fatalf("SaveExecution: %v", err)
	}
}

// savePlan writes a plan into the fake store.
func savePlan(t *testing.T, store *execstoretest.Store, p execstore.Plan) {
	t.Helper()
	if err := store.SavePlan(context.Background(), p); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
}

// mustJSON encodes v, failing the test if it cannot.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
