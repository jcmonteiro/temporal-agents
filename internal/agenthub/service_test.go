package agenthub_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
	"temporal-agents/internal/wfid"
)

// now is the fixed clock the fake wires in, so a written timestamp is assertable.
var now = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

// ago returns a timestamp d before the fixed clock, for readable ordering setup.
func ago(d time.Duration) time.Time { return now.Add(-d) }

// newService wires the service over a fake source.
func newService(t *testing.T, source *agenthubtest.Source) *agenthub.Service {
	t.Helper()
	service, err := agenthub.NewService(source.Dependencies(now))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

// TestNewServiceRequiresEveryPort pins the fail-fast wiring: a missing port would
// silently degrade the overview, which is the one thing a view an operator trusts
// must not do.
func TestNewServiceRequiresEveryPort(t *testing.T) {
	source := agenthubtest.New()
	full := source.Dependencies(now)
	cases := map[string]func(*agenthub.Dependencies){
		"live":        func(d *agenthub.Dependencies) { d.Live = nil },
		"collections": func(d *agenthub.Dependencies) { d.Collections = nil },
		"plans":       func(d *agenthub.Dependencies) { d.Plans = nil },
		"schedules":   func(d *agenthub.Dependencies) { d.Schedules = nil },
		"dismissals":  func(d *agenthub.Dependencies) { d.Dismissals = nil },
	}
	for name, remove := range cases {
		t.Run("without the "+name+" port", func(t *testing.T) {
			deps := full
			remove(&deps)
			if _, err := agenthub.NewService(deps); err == nil {
				t.Fatal("NewService accepted a missing port, want an error")
			}
		})
	}
}

// TestFleetsAggregatesThePlanAgainstItsNodeExecutions is the whole read path for a
// fleet in one case: the plan supplies the graph, the child executions supply what
// happened, and the API answers with the aggregated status and derived progress so
// no consumer has to compute them.
func TestFleetsAggregatesThePlanAgainstItsNodeExecutions(t *testing.T) {
	fleetID := "fleet-" + uuidLike("1")
	source := agenthubtest.New().
		WithRecorded(
			agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(time.Hour)),
			agenthubtest.Node(fleetID, "foundation", agenthub.OutcomeSucceeded, ago(50*time.Minute)),
			agenthubtest.Node(fleetID, "api", agenthub.OutcomeRunning, ago(30*time.Minute)),
		).
		WithRunning(
			agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(time.Hour)),
			agenthubtest.Node(fleetID, "api", agenthub.OutcomeRunning, ago(30*time.Minute)),
		).
		WithPlan(fleetID, agenthub.Plan{Goal: "expose pricing", Nodes: []agenthub.PlanNode{
			{ID: "foundation", Prompt: "the domain"},
			{ID: "api", Prompt: "the REST layer", DependsOn: []string{"foundation"}},
			{ID: "ui", Prompt: "the console", DependsOn: []string{"api"}},
		}})

	fleets, err := newService(t, source).Fleets(context.Background(), 0)
	if err != nil {
		t.Fatalf("Fleets: %v", err)
	}
	if len(fleets) != 1 {
		t.Fatalf("got %d fleets, want 1", len(fleets))
	}
	fleet := fleets[0]
	if fleet.ID != fleetID || fleet.Goal != "expose pricing" {
		t.Fatalf("fleet = %q/%q, want %q/%q", fleet.ID, fleet.Goal, fleetID, "expose pricing")
	}
	if fleet.Status != agenthub.StatusInProgress {
		t.Errorf("status = %q, want in-progress", fleet.Status)
	}
	if fleet.Progress.Done != 1 || fleet.Progress.Total != 3 {
		t.Errorf("progress = %d/%d, want 1/3", fleet.Progress.Done, fleet.Progress.Total)
	}
	want := map[string]agenthub.WorkStatus{
		"foundation": agenthub.StatusDone,
		"api":        agenthub.StatusInProgress,
		"ui":         agenthub.StatusWaiting,
	}
	if len(fleet.Nodes) != len(want) {
		t.Fatalf("got %d nodes, want %d", len(fleet.Nodes), len(want))
	}
	for _, node := range fleet.Nodes {
		if node.Status != want[node.ID] {
			t.Errorf("node %q = %q, want %q", node.ID, node.Status, want[node.ID])
		}
	}
	// The node that never started has no execution to point at, and the API must not
	// invent one.
	for _, node := range fleet.Nodes {
		hasExecution := node.Execution != nil
		if wantExecution := node.ID != "ui"; hasExecution != wantExecution {
			t.Errorf("node %q execution present = %v, want %v", node.ID, hasExecution, wantExecution)
		}
	}
	if got := fleet.Nodes[1].Execution.WorkflowID; got != wfid.FleetNodeWorkflowID(fleetID, "api") {
		t.Errorf("node execution workflow id = %q, want the <fleetID>-<nodeID> convention", got)
	}
}

func TestActiveWorkUsesParentLivenessInsteadOfFleetAggregateStatus(t *testing.T) {
	todoID := "fleet-" + uuidLike("todo")
	failedID := "fleet-" + uuidLike("fail")
	source := agenthubtest.New().
		WithRecorded(
			agenthubtest.Fleet(todoID, agenthub.OutcomeRunning, ago(time.Hour)),
			agenthubtest.Fleet(failedID, agenthub.OutcomeRunning, ago(2*time.Hour)),
			agenthubtest.Node(failedID, "broken", agenthub.OutcomeFailed, ago(90*time.Minute)),
		).
		WithRunning(
			agenthubtest.Fleet(todoID, agenthub.OutcomeRunning, ago(time.Hour)),
			agenthubtest.Fleet(failedID, agenthub.OutcomeRunning, ago(2*time.Hour)),
		).
		WithPlan(todoID, agenthub.Plan{Goal: "not started", Nodes: []agenthub.PlanNode{{ID: "ready"}}}).
		WithPlan(failedID, agenthub.Plan{Goal: "partly failed", Nodes: []agenthub.PlanNode{{ID: "broken"}, {ID: "remaining"}}})

	page, err := newService(t, source).ActiveWork(context.Background(), agenthub.PageQuery{Limit: 200})
	if err != nil {
		t.Fatalf("ActiveWork: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d active fleets, want 2", len(page.Items))
	}
	statuses := map[string]agenthub.WorkStatus{}
	for _, fleet := range page.Items {
		if !fleet.Running || fleet.Type != agenthub.ActiveWorkFleet {
			t.Errorf("fleet %q type/running = %q/%v, want fleet/true", fleet.ID, fleet.Type, fleet.Running)
		}
		statuses[fleet.ID] = fleet.Status
	}
	if statuses[todoID] != agenthub.StatusTodo || statuses[failedID] != agenthub.StatusFailed {
		t.Errorf("active fleet statuses = %v, want todo and failed", statuses)
	}
}

func TestActiveWorkKeepsAContinueAsNewChainWhenItsTimestampChangesBetweenPages(t *testing.T) {
	newer := agenthubtest.Run("run-new", "new", agenthub.OutcomeRunning, ago(time.Minute))
	looping := agenthubtest.Run("run-looping", "looping", agenthub.OutcomeRunning, ago(time.Hour))
	source := agenthubtest.New().WithRunning(newer, looping)
	service := newService(t, source)

	first, err := service.ActiveWork(context.Background(), agenthub.PageQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first ActiveWork: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != newer.WorkflowID || len(first.Next) == 0 {
		t.Fatalf("first page = %+v, want run-new and a source cursor", first)
	}

	// A continue-as-new iteration changes Temporal's current-run start time. The
	// chain must remain after the native cursor instead of moving around a timestamp
	// cursor owned by this service.
	looping.StartedAt = now.Add(time.Minute)
	source.ReplaceRunning(newer, looping)
	second, err := service.ActiveWork(context.Background(), agenthub.PageQuery{Limit: 1, Cursor: first.Next})
	if err != nil {
		t.Fatalf("second ActiveWork: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != looping.WorkflowID {
		t.Fatalf("second page = %+v, want the still-active looping chain", second)
	}
}

func TestActiveWorkContinuesAfterASourcePageWithOnlyNestedWork(t *testing.T) {
	fleetID := "fleet-" + uuidLike("nested")
	source := agenthubtest.New().WithRunning(
		agenthubtest.Node(fleetID, "node", agenthub.OutcomeRunning, ago(time.Minute)),
		agenthubtest.Run("run-visible", "visible", agenthub.OutcomeRunning, ago(time.Hour)),
	)
	service := newService(t, source)

	first, err := service.ActiveWork(context.Background(), agenthub.PageQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first ActiveWork: %v", err)
	}
	if len(first.Items) != 0 || len(first.Next) == 0 {
		t.Fatalf("first page = %+v, want an empty filtered page with a continuation", first)
	}
	second, err := service.ActiveWork(context.Background(), agenthub.PageQuery{Limit: 1, Cursor: first.Next})
	if err != nil {
		t.Fatalf("second ActiveWork: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "run-visible" {
		t.Fatalf("second page = %+v, want the top-level run", second)
	}
}

func TestActiveWorkRejectsAForeignCursor(t *testing.T) {
	_, err := newService(t, agenthubtest.New()).ActiveWork(context.Background(), agenthub.PageQuery{
		Limit: 1, Cursor: []byte("fleet cursor"),
	})
	if !errors.Is(err, agenthub.ErrInvalid) {
		t.Fatalf("ActiveWork error = %v, want ErrInvalid", err)
	}
}

// TestFleetsUsesTheRecordedBreakdownForSkippedAndBlockedNodes pins where the two
// outcomes a child execution cannot express come from: a skipped node has no
// execution at all, and a blocked one is a recoverable stop that needs a human.
func TestFleetsUsesTheRecordedBreakdownForSkippedAndBlockedNodes(t *testing.T) {
	fleetID := "fleet-" + uuidLike("2")
	parent := agenthubtest.Fleet(fleetID, agenthub.OutcomeSucceeded, ago(2*time.Hour))
	parent.EndedAt = ago(time.Hour)
	parent.NodeOutcomes = []agenthub.NodeOutcome{
		{NodeID: "api", Outcome: agenthub.OutcomeBlocked},
		{NodeID: "ui", Outcome: agenthub.OutcomeSkipped},
	}
	source := agenthubtest.New().
		WithRecorded(
			parent,
			agenthubtest.Node(fleetID, "foundation", agenthub.OutcomeSucceeded, ago(110*time.Minute)),
			// The child of a blocked node records itself as failed; only the parent's
			// breakdown knows it was recoverable.
			agenthubtest.Node(fleetID, "api", agenthub.OutcomeFailed, ago(100*time.Minute)),
		).
		WithPlan(fleetID, agenthub.Plan{Nodes: []agenthub.PlanNode{
			{ID: "foundation"},
			{ID: "api", DependsOn: []string{"foundation"}},
			{ID: "ui", DependsOn: []string{"api"}},
		}})

	fleet, err := newService(t, source).Fleet(context.Background(), fleetID)
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	want := map[string]agenthub.WorkStatus{
		"foundation": agenthub.StatusDone,
		"api":        agenthub.StatusWaitingInput,
		"ui":         agenthub.StatusPaused,
	}
	for _, node := range fleet.Nodes {
		if node.Status != want[node.ID] {
			t.Errorf("node %q = %q, want %q", node.ID, node.Status, want[node.ID])
		}
	}
	if fleet.Status != agenthub.StatusWaitingInput {
		t.Errorf("fleet status = %q, want waiting-input", fleet.Status)
	}
	if !fleet.Dismissible() {
		// waiting-input is not terminal: the fleet still needs an operator, so hiding
		// it would hide work that is not finished.
		if fleet.Status.Terminal() {
			t.Error("waiting-input must not be terminal")
		}
	}
	upNext := fleet.UpNext()
	if len(upNext) != 0 {
		t.Errorf("up next = %d nodes, want none (everything has settled or was skipped)", len(upNext))
	}
}

// TestFleetWithoutAResolvablePlan pins the honest fallback: a fleet whose plan the
// store cannot resolve is still reported, with its own execution's status and no
// nodes, rather than omitted or given an invented graph.
func TestFleetWithoutAResolvablePlan(t *testing.T) {
	fleetID := "fleet-" + uuidLike("3")
	source := agenthubtest.New().WithRecorded(agenthubtest.Fleet(fleetID, agenthub.OutcomeFailed, ago(time.Hour)))

	fleet, err := newService(t, source).Fleet(context.Background(), fleetID)
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if fleet.Status != agenthub.StatusFailed {
		t.Errorf("status = %q, want failed", fleet.Status)
	}
	if len(fleet.Nodes) != 0 || fleet.Progress.Total != 0 {
		t.Errorf("nodes = %d, progress total = %d, want none", len(fleet.Nodes), fleet.Progress.Total)
	}
}

// TestFleetWhoseOwnExecutionFailedBeforeAnyNodeRan pins the guard on top of the
// aggregation: an orchestration that could not run has nodes that never started,
// and reporting that fleet as "todo" would hide the failure.
func TestFleetWhoseOwnExecutionFailedBeforeAnyNodeRan(t *testing.T) {
	fleetID := "fleet-" + uuidLike("4")
	source := agenthubtest.New().
		WithRecorded(agenthubtest.Fleet(fleetID, agenthub.OutcomeFailed, ago(time.Hour))).
		WithPlan(fleetID, agenthub.Plan{Nodes: []agenthub.PlanNode{{ID: "a"}, {ID: "b"}}})

	fleet, err := newService(t, source).Fleet(context.Background(), fleetID)
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if fleet.Status != agenthub.StatusFailed {
		t.Fatalf("status = %q, want failed", fleet.Status)
	}
}

// TestFleetStillRunningAfterItsLastNodeSettled pins the other guard: the
// orchestration has its own work left (its summary, its notifications) after the
// last node is done, so the fleet is not done until it is.
func TestFleetStillRunningAfterItsLastNodeSettled(t *testing.T) {
	fleetID := "fleet-" + uuidLike("5")
	source := agenthubtest.New().
		WithRecorded(
			agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(time.Hour)),
			agenthubtest.Node(fleetID, "a", agenthub.OutcomeSucceeded, ago(30*time.Minute)),
		).
		WithRunning(agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(time.Hour))).
		WithPlan(fleetID, agenthub.Plan{Nodes: []agenthub.PlanNode{{ID: "a"}}})

	fleet, err := newService(t, source).Fleet(context.Background(), fleetID)
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if fleet.Status != agenthub.StatusInProgress {
		t.Fatalf("status = %q, want in-progress", fleet.Status)
	}
}

// TestFleetReconciliationIgnoresAnOlderStaleIteration pins that only the newest
// durable iteration can define the current chain state.
func TestFleetReconciliationIgnoresAnOlderStaleIteration(t *testing.T) {
	fleetID := "fleet-" + uuidLike("51")
	older := agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(2*time.Hour))
	older.RunID = "iteration-1"
	newer := agenthubtest.Fleet(fleetID, agenthub.OutcomeSucceeded, ago(time.Hour))
	newer.RunID = "iteration-2"
	newer.EndedAt = ago(30 * time.Minute)

	fleets, err := newService(t, agenthubtest.New().WithRecorded(older, newer)).Fleets(context.Background(), 1)
	if err != nil {
		t.Fatalf("Fleets: %v", err)
	}
	if len(fleets) != 1 || fleets[0].Status != agenthub.StatusDone {
		t.Fatalf("fleets = %+v, want the newest successful iteration", fleets)
	}
}

// TestFleetUnknown pins the 404 path.
func TestFleetUnknown(t *testing.T) {
	_, err := newService(t, agenthubtest.New()).Fleet(context.Background(), "fleet-nope")
	if !errors.Is(err, agenthub.ErrNotFound) {
		t.Fatalf("Fleet(unknown) = %v, want ErrNotFound", err)
	}
}

// TestRunsCollapseAContinueAsNewChainIntoOneSatellite pins the chain identity: a
// chained run that has looped is one satellite keyed by its workflow ID, showing
// the latest iteration's status — never one satellite per retained iteration.
func TestRunsCollapseAContinueAsNewChainIntoOneSatellite(t *testing.T) {
	id := "run-" + uuidLike("6")
	first := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(3*time.Hour))
	first.RunID, first.Tokens = "iteration-1", 100
	second := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(2*time.Hour))
	second.RunID, second.Tokens = "iteration-2", 50
	latest := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeRunning, ago(time.Hour))
	latest.RunID, latest.Tokens = "iteration-3", 25

	source := agenthubtest.New().WithRecorded(first, second, latest).WithRunning(latest)

	runs, err := newService(t, source).Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1 satellite for the chain", len(runs))
	}
	run := runs[0]
	switch {
	case run.ID != id:
		t.Errorf("id = %q, want the chain's workflow id %q", run.ID, id)
	case run.Status != agenthub.StatusInProgress:
		t.Errorf("status = %q, want the latest iteration's in-progress", run.Status)
	case run.Iterations != 3:
		t.Errorf("iterations = %d, want 3", run.Iterations)
	case run.Tokens != 175:
		t.Errorf("tokens = %d, want the chain's 175", run.Tokens)
	case !run.StartedAt.Equal(ago(3 * time.Hour)):
		t.Errorf("startedAt = %v, want the chain's first iteration %v", run.StartedAt, ago(3*time.Hour))
	}
}

// TestRunsAggregateEveryIterationBeforeLimitingChains pins that a row cap cannot
// hide another resource or truncate a continue-as-new chain.
func TestRunsAggregateEveryIterationBeforeLimitingChains(t *testing.T) {
	longID := "run-" + uuidLike("62")
	otherID := "run-" + uuidLike("63")
	source := agenthubtest.New()
	for i := 0; i < 1001; i++ {
		iteration := agenthubtest.Run(longID, "long chain", agenthub.OutcomeSucceeded,
			now.Add(-time.Duration(i)*time.Second))
		iteration.RunID = fmt.Sprintf("iteration-%04d", i)
		iteration.Tokens = 1
		source.WithRecorded(iteration)
	}
	source.WithRecorded(agenthubtest.Run(otherID, "other chain", agenthub.OutcomeSucceeded, ago(2*time.Hour)))

	runs, err := newService(t, source).Runs(context.Background(), 2)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want both chains", len(runs))
	}
	for _, run := range runs {
		if run.ID == longID && (run.Iterations != 1001 || run.Tokens != 1001) {
			t.Errorf("long chain = %d iterations/%d tokens, want 1001/1001", run.Iterations, run.Tokens)
		}
	}
}

// TestRunsAggregatesALiveChainOutsideTheDurablePage pins the union boundary: a
// running iteration can belong to an old chain whose durable aggregate falls outside
// the requested page. Its full aggregate must be loaded before the union is sorted,
// or the live iteration's recent start can make that old chain displace a newer item.
func TestRunsAggregatesALiveChainOutsideTheDurablePage(t *testing.T) {
	oldID := "run-" + uuidLike("64")
	newerID := "run-" + uuidLike("65")
	first := agenthubtest.Run(oldID, "long-running chain", agenthub.OutcomeSucceeded, ago(4*time.Hour))
	first.RunID, first.Tokens = "old-iteration-1", 100
	second := agenthubtest.Run(oldID, "long-running chain", agenthub.OutcomeSucceeded, ago(3*time.Hour))
	second.RunID, second.Tokens = "old-iteration-2", 50
	live := agenthubtest.Run(oldID, "", agenthub.OutcomeRunning, ago(time.Minute))
	live.RunID = "old-iteration-3"
	newer := agenthubtest.Run(newerID, "newer chain", agenthub.OutcomeSucceeded, ago(90*time.Minute))

	runs, err := newService(t, agenthubtest.New().WithRecorded(first, second, newer).WithRunning(live)).Runs(context.Background(), 1)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != newerID {
		t.Fatalf("runs = %v, want the correctly ordered newer chain %q", runIDs(runs), newerID)
	}
}

// TestRunsCountALiveIterationBeforeItsRecordLands pins the join at the live edge: a
// continue-as-new iteration can be visible to the orchestrator before its durable
// start write lands, and it is still one more known iteration of the chain.
func TestRunsCountALiveIterationBeforeItsRecordLands(t *testing.T) {
	id := "run-" + uuidLike("61")
	recorded := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(2*time.Hour))
	recorded.RunID = "iteration-1"
	live := agenthubtest.Run(id, "", agenthub.OutcomeRunning, ago(time.Hour))
	live.RunID = "iteration-2"
	source := agenthubtest.New().WithRecorded(recorded).WithRunning(live)

	runs, err := newService(t, source).Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Iterations != 2 || runs[0].Status != agenthub.StatusInProgress {
		t.Fatalf("runs = %+v, want one in-progress chain with two iterations", runs)
	}
}

// TestRunsExcludeChildrenAndScheduledRuns pins that the overview shows each piece
// of work once: a fleet's node belongs to its fleet, a review to the develop run
// that started it, and a schedule-fired run to its schedule.
func TestRunsExcludeChildrenAndScheduledRuns(t *testing.T) {
	fleetID := "fleet-" + uuidLike("7")
	standalone := agenthubtest.Run("run-"+uuidLike("8"), "summarize the README", agenthub.OutcomeSucceeded, ago(time.Hour))
	scheduled := agenthubtest.Run("run-"+uuidLike("9"), "the daily digest", agenthub.OutcomeSucceeded, ago(2*time.Hour))
	scheduled.ScheduleID = "schedule-" + uuidLike("a")
	child := agenthubtest.Run("review-"+uuidLike("b"), "review", agenthub.OutcomeSucceeded, ago(3*time.Hour))
	child.ParentWorkflowID = "develop-" + uuidLike("c")

	source := agenthubtest.New().WithRecorded(
		standalone, scheduled, child,
		agenthubtest.Node(fleetID, "a", agenthub.OutcomeSucceeded, ago(4*time.Hour)),
	)

	runs, err := newService(t, source).Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != standalone.WorkflowID {
		t.Fatalf("runs = %v, want only the standalone run %q", runIDs(runs), standalone.WorkflowID)
	}
	if runs[0].Type != agenthub.RunTypePrompt || runs[0].Label != "summarize the README" {
		t.Errorf("run = %q/%q, want run/%q", runs[0].Type, runs[0].Label, "summarize the README")
	}
}

// TestRunRecordedAsRunningButGoneFromTheOrchestrator pins the honesty rule that
// needs both sources: an execution that never settled and that the orchestrator no
// longer knows is not in progress, and the API must not keep claiming it is.
func TestRunRecordedAsRunningButGoneFromTheOrchestrator(t *testing.T) {
	id := "run-" + uuidLike("d")
	source := agenthubtest.New().WithRecorded(
		agenthubtest.Run(id, "terminated by hand", agenthub.OutcomeRunning, ago(time.Hour)),
	)

	runs, err := newService(t, source).Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != agenthub.StatusFailed {
		t.Fatalf("runs = %v, want one failed run", runs)
	}
}

// TestRunsShowLiveWorkTheRecordDoesNotHave pins the union of the two sources: an
// execution older than the durable record is still real work.
func TestRunsShowLiveWorkTheRecordDoesNotHave(t *testing.T) {
	id := "run-" + uuidLike("e")
	source := agenthubtest.New().WithRunning(
		agenthubtest.Run(id, "", agenthub.OutcomeRunning, ago(time.Minute)),
	)

	runs, err := newService(t, source).Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != id || runs[0].Status != agenthub.StatusInProgress {
		t.Fatalf("runs = %v, want the live run %q in progress", runIDs(runs), id)
	}
}

// TestDismissedItemsLeaveTheOverviewButNotTheStore pins the dismissal read path: a
// dismissed run is gone from the collection while the item itself is untouched, so
// dismissing is view state and never a change to the work.
func TestDismissedItemsLeaveTheOverviewButNotTheStore(t *testing.T) {
	kept := agenthubtest.Run("run-"+uuidLike("f"), "kept", agenthub.OutcomeSucceeded, ago(time.Hour))
	hidden := agenthubtest.Run("run-"+uuidLike("10"), "hidden", agenthub.OutcomeSucceeded, ago(2*time.Hour))
	source := agenthubtest.New().WithRecorded(kept, hidden).
		WithDismissal(agenthub.KindRun, hidden.WorkflowID)
	service := newService(t, source)

	runs, err := service.Runs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != kept.WorkflowID {
		t.Fatalf("runs = %v, want only %q", runIDs(runs), kept.WorkflowID)
	}
	if _, err := service.Run(context.Background(), hidden.WorkflowID); err != nil {
		t.Fatalf("a dismissed run must still be readable by id: %v", err)
	}
}

// TestDismissRefusesWorkThatIsStillActive pins that the "only finished work can be
// hidden" rule is the server's: a client that offers the affordance anyway cannot
// hide a running run.
func TestDismissRefusesWorkThatIsStillActive(t *testing.T) {
	id := "run-" + uuidLike("11")
	source := agenthubtest.New().
		WithRecorded(agenthubtest.Run(id, "still going", agenthub.OutcomeRunning, ago(time.Minute))).
		WithRunning(agenthubtest.Run(id, "still going", agenthub.OutcomeRunning, ago(time.Minute)))
	service := newService(t, source)

	if _, err := service.Dismiss(context.Background(), agenthub.KindRun, id); !errors.Is(err, agenthub.ErrNotDismissible) {
		t.Fatalf("Dismiss(running) = %v, want ErrNotDismissible", err)
	}
	if _, err := service.Dismiss(context.Background(), agenthub.KindRun, "run-nope"); !errors.Is(err, agenthub.ErrNotFound) {
		t.Fatalf("Dismiss(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := service.Dismiss(context.Background(), agenthub.KindSchedule, "schedule-1"); err == nil {
		t.Fatal("Dismiss(schedule) = nil, want a refusal")
	}
}

// TestDismissIsIdempotentAndUndoable pins the write contract: dismissing twice is
// the same dismissal (so a client that retries a lost response is not punished),
// and undismissing an item that was not dismissed is reported as missing rather
// than silently accepted.
func TestDismissIsIdempotentAndUndoable(t *testing.T) {
	id := "run-" + uuidLike("12")
	source := agenthubtest.New().
		WithRecorded(agenthubtest.Run(id, "finished", agenthub.OutcomeSucceeded, ago(time.Hour)))
	clock := now
	deps := source.Dependencies(now)
	deps.Now = func() time.Time { return clock }
	service, err := agenthub.NewService(deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	first, err := service.Dismiss(ctx, agenthub.KindRun, id)
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if !first.DismissedAt.Equal(now) {
		t.Errorf("dismissedAt = %v, want the injected clock %v", first.DismissedAt, now)
	}
	clock = now.Add(time.Hour)
	second, err := service.Dismiss(ctx, agenthub.KindRun, id)
	if err != nil {
		t.Fatalf("a repeated Dismiss must succeed: %v", err)
	}
	if !second.DismissedAt.Equal(first.DismissedAt) {
		t.Errorf("repeated dismissal time = %v, want original %v", second.DismissedAt, first.DismissedAt)
	}
	dismissals, err := service.Dismissals(ctx)
	if err != nil {
		t.Fatalf("Dismissals: %v", err)
	}
	if len(dismissals) != 1 || dismissals[0].ID() != "run:"+id {
		t.Fatalf("dismissals = %v, want exactly one for %q", dismissals, id)
	}
	if err := service.Undismiss(ctx, agenthub.KindRun, id); err != nil {
		t.Fatalf("Undismiss: %v", err)
	}
	if err := service.Undismiss(ctx, agenthub.KindRun, id); !errors.Is(err, agenthub.ErrNotFound) {
		t.Fatalf("Undismiss(twice) = %v, want ErrNotFound", err)
	}
}

// TestSchedulesTakeTheirLabelFromTheRunsTheyFired pins the honest label source: a
// schedule has no prompt of its own, so what it asks of the agent is only visible
// in the runs it fired.
func TestSchedulesTakeTheirLabelFromTheRunsTheyFired(t *testing.T) {
	scheduleID := "schedule-" + uuidLike("13")
	fired := agenthubtest.Run("run-"+uuidLike("14"), "post the daily digest", agenthub.OutcomeSucceeded, ago(time.Hour))
	fired.ScheduleID = scheduleID
	source := agenthubtest.New().
		WithRecorded(fired).
		WithSchedules(agenthub.ScheduleState{
			ID: scheduleID, Spec: "0 9 * * *", LastRunAt: ago(time.Hour), NextRunAt: now.Add(time.Hour),
		})

	schedules, err := newService(t, source).Schedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("got %d schedules, want 1", len(schedules))
	}
	schedule := schedules[0]
	switch {
	case schedule.Label != "post the daily digest":
		t.Errorf("label = %q, want the fired run's prompt", schedule.Label)
	case schedule.Status != agenthub.StatusDone:
		t.Errorf("status = %q, want the latest action's done", schedule.Status)
	case schedule.Dismissible():
		t.Error("a schedule must never be dismissible")
	}
}

// TestSchedulePausedWhileAnActionRuns pins the precedence of the schedule mapping
// end to end, through the service rather than only the pure rule.
func TestSchedulePausedWhileAnActionRuns(t *testing.T) {
	source := agenthubtest.New().WithSchedules(agenthub.ScheduleState{
		ID: "schedule-" + uuidLike("15"), Paused: true, RunningActions: 1,
		LastOutcome: agenthub.OutcomeSucceeded,
	})
	schedules, err := newService(t, source).Schedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if schedules[0].Status != agenthub.StatusPaused {
		t.Fatalf("status = %q, want paused", schedules[0].Status)
	}
}

// TestScheduledActionRecordedAsRunningButGoneFromTheOrchestrator pins that a
// scheduled execution follows the same reconciliation rule as a standalone run:
// a stale running record is a failed action, not evidence that the schedule never
// fired.
func TestScheduledActionRecordedAsRunningButGoneFromTheOrchestrator(t *testing.T) {
	scheduleID := "schedule-" + uuidLike("16")
	action := agenthubtest.Run("run-"+uuidLike("17"), "daily digest", agenthub.OutcomeRunning, ago(time.Hour))
	action.ScheduleID = scheduleID
	source := agenthubtest.New().WithRecorded(action).WithSchedules(agenthub.ScheduleState{ID: scheduleID})

	schedules, err := newService(t, source).Schedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if len(schedules) != 1 || schedules[0].Status != agenthub.StatusFailed {
		t.Fatalf("schedules = %+v, want the stale action reported as failed", schedules)
	}
}

// TestScheduledActionSettlesBetweenListAndDescribe pins the race at the live
// boundary: an action omitted from the running listing can be returned as closed by
// the batch describe, and that terminal state must define the schedule.
func TestScheduledActionSettlesBetweenListAndDescribe(t *testing.T) {
	scheduleID := "schedule-" + uuidLike("18")
	action := agenthubtest.Run("run-"+uuidLike("19"), "daily digest", agenthub.OutcomeRunning, ago(time.Hour))
	action.ScheduleID = scheduleID
	settled := action
	settled.RunID = "closed-run"
	settled.Outcome = agenthub.OutcomeSucceeded
	settled.EndedAt = ago(30 * time.Minute)
	source := agenthubtest.New().
		WithRecorded(action).
		WithExecutionState(settled).
		WithSchedules(agenthub.ScheduleState{ID: scheduleID})

	schedules, err := newService(t, source).Schedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if len(schedules) != 1 || schedules[0].Status != agenthub.StatusDone {
		t.Fatalf("schedules = %+v, want the closed action reported as done", schedules)
	}
}

// TestAnUnavailableDependencyIsReportedAsRetryable pins that a port failure is
// never answered with a half-empty overview: it is marked as the retryable
// condition it is, so the transport can say "come back in a moment" instead of
// showing work that is not there.
func TestAnUnavailableDependencyIsReportedAsRetryable(t *testing.T) {
	source := agenthubtest.Failing(errors.New("connection refused"))
	service := newService(t, source)
	ctx := context.Background()

	reads := map[string]func() error{
		"fleets":    func() error { _, err := service.Fleets(ctx, 0); return err },
		"fleet":     func() error { _, err := service.Fleet(ctx, "fleet-1"); return err },
		"runs":      func() error { _, err := service.Runs(ctx, 0); return err },
		"schedules": func() error { _, err := service.Schedules(ctx, 0); return err },
		"active work": func() error {
			_, err := service.ActiveWork(ctx, agenthub.PageQuery{Limit: 1})
			return err
		},
		"dismissals": func() error { _, err := service.Dismissals(ctx); return err },
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			if err := read(); !errors.Is(err, agenthub.ErrUnavailable) {
				t.Fatalf("%s = %v, want ErrUnavailable", name, err)
			}
		})
	}
}

// TestDismissedFleetsAreExcludedBeforeTheLimit pins that any number of newer
// dismissals cannot permanently hide an older visible fleet.
func TestDismissedFleetsAreExcludedBeforeTheLimit(t *testing.T) {
	source := agenthubtest.New()
	for i := 0; i < 201; i++ {
		id := "fleet-" + uuidLike(fmt.Sprintf("%x", 0x100+i))
		source.WithRecorded(agenthubtest.Fleet(id, agenthub.OutcomeSucceeded, now.Add(-time.Duration(i)*time.Second)))
		source.WithDismissal(agenthub.KindFleet, id)
	}
	visibleID := "fleet-" + uuidLike("2ff")
	source.WithRecorded(agenthubtest.Fleet(visibleID, agenthub.OutcomeSucceeded, ago(time.Hour)))

	fleets, err := newService(t, source).Fleets(context.Background(), 1)
	if err != nil {
		t.Fatalf("Fleets: %v", err)
	}
	if len(fleets) != 1 || fleets[0].ID != visibleID {
		t.Fatalf("fleets = %v, want older visible fleet %q", fleets, visibleID)
	}
}

// TestFleetsRespectTheLimit pins the cap: the API is an overview, not a history
// browser, so a page is a page.
func TestFleetsRespectTheLimit(t *testing.T) {
	source := agenthubtest.New()
	for i := 0; i < 5; i++ {
		id := "fleet-" + uuidLike(string(rune('A'+i)))
		source.WithRecorded(agenthubtest.Fleet(id, agenthub.OutcomeSucceeded, ago(time.Duration(i)*time.Hour)))
	}
	fleets, err := newService(t, source).Fleets(context.Background(), 2)
	if err != nil {
		t.Fatalf("Fleets: %v", err)
	}
	if len(fleets) != 2 {
		t.Fatalf("got %d fleets, want 2", len(fleets))
	}
	// Newest first, so the limit keeps the most recent work.
	if !fleets[0].StartedAt.After(fleets[1].StartedAt) {
		t.Errorf("fleets are not newest-first: %v then %v", fleets[0].StartedAt, fleets[1].StartedAt)
	}
}

// uuidLike builds a canonical-looking UUID from a short seed, so a test ID is
// classified as a fleet parent (a bare UUID after the prefix) rather than a node.
func uuidLike(seed string) string {
	const filler = "00000000-0000-4000-8000-000000000000"
	padded := seed
	for len(padded) < 4 {
		padded = "0" + padded
	}
	return padded[:4] + filler[4:]
}

// runIDs renders run IDs for a failure message.
func runIDs(runs []agenthub.Run) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}
