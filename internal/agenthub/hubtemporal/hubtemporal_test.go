package hubtemporal

import (
	"context"
	"errors"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/timestamppb"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/wfid"
)

// The adapter is driven through the narrow client interfaces it declares rather
// than a running orchestrator: what it owns is the translation of a visibility
// answer into the API's model, and a stand-in exercises exactly that — including the
// paging and the "the orchestrator has never heard of it" path, which a real server
// makes awkward to reach on purpose.

// fakeWorkflows is a stand-in for the orchestration client: it hands out prepared
// pages and records the queries it was asked.
type fakeWorkflows struct {
	pages    []*workflowservice.ListWorkflowExecutionsResponse
	calls    int
	queries  []string
	describe *workflowpb.WorkflowExecutionInfo
	err      error
}

func (f *fakeWorkflows) ListWorkflow(_ context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.queries = append(f.queries, request.GetQuery())
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func (f *fakeWorkflows) DescribeWorkflowExecution(_ context.Context, workflowID, _ string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.describe == nil || f.describe.GetExecution().GetWorkflowId() != workflowID {
		return nil, serviceerror.NewNotFound("workflow execution not found")
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: f.describe}, nil
}

// fakeSchedules is a stand-in for the schedule client.
type fakeSchedules struct {
	entries []*client.ScheduleListEntry
	err     error
}

func (f *fakeSchedules) List(context.Context, client.ScheduleListOptions) (client.ScheduleListIterator, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &fakeIterator{entries: f.entries}, nil
}

// fakeIterator walks the prepared schedule entries.
type fakeIterator struct {
	entries []*client.ScheduleListEntry
	at      int
}

func (f *fakeIterator) HasNext() bool { return f.at < len(f.entries) }

func (f *fakeIterator) Next() (*client.ScheduleListEntry, error) {
	entry := f.entries[f.at]
	f.at++
	return entry, nil
}

// TestConstructorsRequireAClient pins the fail-fast wiring.
func TestConstructorsRequireAClient(t *testing.T) {
	if _, err := NewExecutions(nil); err == nil {
		t.Error("NewExecutions(nil) = nil, want an error")
	}
	if _, err := NewSchedules(nil); err == nil {
		t.Error("NewSchedules(nil) = nil, want an error")
	}
}

// TestOutcomeFromEveryOrchestrationStatus pins the whole status mapping. A
// cancellation, a timeout and a termination are failures — the work did not
// complete — and an execution that continued as new is still running, because the
// chain is the item.
func TestOutcomeFromEveryOrchestrationStatus(t *testing.T) {
	cases := map[enums.WorkflowExecutionStatus]agenthub.ExecutionOutcome{
		enums.WORKFLOW_EXECUTION_STATUS_RUNNING:          agenthub.OutcomeRunning,
		enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW: agenthub.OutcomeRunning,
		enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:        agenthub.OutcomeSucceeded,
		enums.WORKFLOW_EXECUTION_STATUS_FAILED:           agenthub.OutcomeFailed,
		enums.WORKFLOW_EXECUTION_STATUS_CANCELED:         agenthub.OutcomeFailed,
		enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:       agenthub.OutcomeFailed,
		enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:        agenthub.OutcomeFailed,
		enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED:      agenthub.OutcomeFailed,
	}
	for status, want := range cases {
		if got := outcomeFrom(status); got != want {
			t.Errorf("outcomeFrom(%v) = %q, want %q", status, got, want)
		}
	}
}

// TestExecutionFromTranslatesTheVisibilityInfo pins the field mapping, including
// the class taken from the workflow-ID convention and the parent that keeps a
// fleet's node from being listed as a satellite of its own.
func TestExecutionFromTranslatesTheVisibilityInfo(t *testing.T) {
	started := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	closed := started.Add(time.Hour)
	fleetID := "fleet-00000000-0000-4000-8000-000000000000"
	info := &workflowpb.WorkflowExecutionInfo{
		Execution:       &commonpb.WorkflowExecution{WorkflowId: fleetID + "-api", RunId: "run-1"},
		Status:          enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		StartTime:       timestamppb.New(started),
		CloseTime:       timestamppb.New(closed),
		ParentExecution: &commonpb.WorkflowExecution{WorkflowId: fleetID},
	}

	got := executionFrom(info)
	switch {
	case got.WorkflowID != fleetID+"-api" || got.RunID != "run-1":
		t.Errorf("identity = %q/%q, want the node's", got.WorkflowID, got.RunID)
	case got.Class != wfid.ClassFleetNode:
		t.Errorf("class = %q, want fleet-node", got.Class)
	case got.Outcome != agenthub.OutcomeSucceeded:
		t.Errorf("outcome = %q, want succeeded", got.Outcome)
	case got.ParentWorkflowID != fleetID:
		t.Errorf("parent = %q, want %q", got.ParentWorkflowID, fleetID)
	case !got.StartedAt.Equal(started) || !got.EndedAt.Equal(closed):
		t.Errorf("times = %v/%v, want %v/%v", got.StartedAt, got.EndedAt, started, closed)
	case got.Label != "":
		t.Errorf("label = %q, want empty: the prompt lives in the durable record", got.Label)
	}
}

// TestExecutionFromAttributesAScheduledRun pins the search-attribute read: a run a
// schedule fired must be attributable to that schedule from the live listing alone,
// or the overview would show a satellite per firing next to the schedule's own.
func TestExecutionFromAttributesAScheduledRun(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("schedule-7")
	if err != nil {
		t.Fatalf("encode the search attribute: %v", err)
	}
	info := &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{WorkflowId: "run-1", RunId: "r1"},
		Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		StartTime: timestamppb.New(time.Unix(1, 0)),
		SearchAttributes: &commonpb.SearchAttributes{
			IndexedFields: map[string]*commonpb.Payload{scheduledByAttribute: payload},
		},
	}
	if got := executionFrom(info).ScheduleID; got != "schedule-7" {
		t.Fatalf("schedule id = %q, want schedule-7", got)
	}

	bare := &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{WorkflowId: "run-2"},
		StartTime: timestamppb.New(time.Unix(1, 0)),
	}
	if got := executionFrom(bare).ScheduleID; got != "" {
		t.Fatalf("schedule id of an unscheduled run = %q, want empty", got)
	}
}

// TestExecutionsReturnsKnownStatesAndOmitsUnknownOnes pins the batch port contract
// used to reconcile stale durable records in one service operation.
func TestExecutionsReturnsKnownStatesAndOmitsUnknownOnes(t *testing.T) {
	known := &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{WorkflowId: "run-known", RunId: "r1"},
		Status:    enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
	}
	adapter, err := NewExecutions(&fakeWorkflows{describe: known})
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}

	states, err := adapter.Executions(context.Background(), []string{"run-known", "run-unknown"})
	if err != nil {
		t.Fatalf("Executions: %v", err)
	}
	if len(states) != 1 || states["run-known"].Outcome != agenthub.OutcomeSucceeded {
		t.Fatalf("states = %+v, want only the known completed execution", states)
	}
}

// TestRunningExecutionsPagesUntilTheCap pins that the listing is bounded: it follows
// the orchestrator's page tokens but stops at the limit, so one request cannot turn
// into an unbounded scan of a busy server.
func TestRunningExecutionsPagesUntilTheCap(t *testing.T) {
	page := func(token string, ids ...string) *workflowservice.ListWorkflowExecutionsResponse {
		resp := &workflowservice.ListWorkflowExecutionsResponse{NextPageToken: []byte(token)}
		for _, id := range ids {
			resp.Executions = append(resp.Executions, &workflowpb.WorkflowExecutionInfo{
				Execution: &commonpb.WorkflowExecution{WorkflowId: id},
				Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
				StartTime: timestamppb.New(time.Unix(1, 0)),
			})
		}
		return resp
	}
	fake := &fakeWorkflows{pages: []*workflowservice.ListWorkflowExecutionsResponse{
		page("more", "run-1", "run-2"),
		page("", "run-3", "run-4"),
	}}
	source, err := NewExecutions(fake)
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}

	all, err := source.RunningExecutions(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunningExecutions: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("got %d executions, want all 4 across both pages", len(all))
	}
	if fake.queries[0] != runningQuery {
		t.Errorf("query = %q, want %q", fake.queries[0], runningQuery)
	}

	fake.calls = 0
	capped, err := source.RunningExecutions(context.Background(), 3)
	if err != nil {
		t.Fatalf("RunningExecutions: %v", err)
	}
	if len(capped) != 3 {
		t.Fatalf("got %d executions, want the cap of 3", len(capped))
	}
}

// TestRunningExecutionsReportsAFailure pins that an unreachable orchestrator is an
// error, not an empty overview: "nothing is running" and "I could not ask" must not
// look the same.
func TestRunningExecutionsReportsAFailure(t *testing.T) {
	source, err := NewExecutions(&fakeWorkflows{err: errors.New("connection refused")})
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}
	if _, err := source.RunningExecutions(context.Background(), 0); err == nil {
		t.Fatal("RunningExecutions during an outage = nil, want an error")
	}
}

// TestExecutionOfAnUnknownWorkflow pins the answer the reader depends on: an
// execution the orchestrator has never heard of is reported as gone, so a record
// that still says "running" can be resolved instead of believed.
func TestExecutionOfAnUnknownWorkflow(t *testing.T) {
	source, err := NewExecutions(&fakeWorkflows{})
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}
	if _, err := source.Execution(context.Background(), "run-gone"); !errors.Is(err, agenthub.ErrNoExecution) {
		t.Fatalf("Execution(unknown) = %v, want ErrNoExecution", err)
	}
}

// TestExecutionOfAKnownWorkflow pins the describe path.
func TestExecutionOfAKnownWorkflow(t *testing.T) {
	source, err := NewExecutions(&fakeWorkflows{describe: &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{WorkflowId: "run-1", RunId: "r9"},
		Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		StartTime: timestamppb.New(time.Unix(1, 0)),
	}})
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}
	got, err := source.Execution(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if got.RunID != "r9" || got.Outcome != agenthub.OutcomeRunning {
		t.Fatalf("execution = %q/%q, want r9/running", got.RunID, got.Outcome)
	}
}

// TestSchedulesTranslateAListEntry pins the schedule mapping: the list entry
// already says whether a schedule is paused, when it last fired and when it fires
// next, so no per-schedule round trip is needed to answer the overview.
func TestSchedulesTranslateAListEntry(t *testing.T) {
	lastFired := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	next := lastFired.Add(24 * time.Hour)
	source, err := NewSchedules(&fakeSchedules{entries: []*client.ScheduleListEntry{{
		ID:     "schedule-1",
		Paused: true,
		Spec:   &client.ScheduleSpec{CronExpressions: []string{"0 9 * * *"}},
		RecentActions: []client.ScheduleActionResult{
			{ScheduleTime: lastFired.Add(-24 * time.Hour), ActualTime: lastFired.Add(-24 * time.Hour)},
			{ScheduleTime: lastFired, ActualTime: lastFired},
		},
		NextActionTimes: []time.Time{next.Add(48 * time.Hour), next},
	}}})
	if err != nil {
		t.Fatalf("NewSchedules: %v", err)
	}

	got, err := source.Schedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d schedules, want 1", len(got))
	}
	state := got[0]
	switch {
	case state.ID != "schedule-1" || !state.Paused:
		t.Errorf("schedule = %q paused=%v, want schedule-1 paused", state.ID, state.Paused)
	case state.Spec != "0 9 * * *":
		t.Errorf("spec = %q, want the cron expression", state.Spec)
	case !state.LastRunAt.Equal(lastFired):
		t.Errorf("lastRunAt = %v, want the most recent action %v", state.LastRunAt, lastFired)
	case !state.NextRunAt.Equal(next):
		t.Errorf("nextRunAt = %v, want the earliest upcoming action %v", state.NextRunAt, next)
	case state.RunningActions != 0:
		t.Errorf("runningActions = %d, want 0: the reader counts them from the live executions", state.RunningActions)
	}
}

// TestDescribeSpecRendersWhatWasConfigured pins the spec rendering for the two
// forms the CLI creates (a cron expression and an interval), a calendar spec, and
// the "nothing to say" case, which must stay empty rather than invent a claim.
func TestDescribeSpecRendersWhatWasConfigured(t *testing.T) {
	cases := []struct {
		name string
		in   *client.ScheduleSpec
		want string
	}{
		{"nothing at all", nil, ""},
		{"a cron expression", &client.ScheduleSpec{CronExpressions: []string{"0 9 * * *"}}, "0 9 * * *"},
		{"an interval", &client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: 90 * time.Minute}}}, "every 1h30m0s"},
		{
			"a calendar",
			&client.ScheduleSpec{Calendars: []client.ScheduleCalendarSpec{{
				Minute: []client.ScheduleRange{{Start: 0}},
				Hour:   []client.ScheduleRange{{Start: 9, End: 17, Step: 2}},
			}}},
			"minute=0 hour=9-17/2",
		},
		{"an empty spec", &client.ScheduleSpec{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeSpec(tc.in); got != tc.want {
				t.Fatalf("describeSpec = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSchedulesRespectTheCap pins that the schedule listing is bounded too.
func TestSchedulesRespectTheCap(t *testing.T) {
	source, err := NewSchedules(&fakeSchedules{entries: []*client.ScheduleListEntry{
		{ID: "schedule-1"}, {ID: "schedule-2"}, {ID: "schedule-3"},
	}})
	if err != nil {
		t.Fatalf("NewSchedules: %v", err)
	}
	got, err := source.Schedules(context.Background(), 2)
	if err != nil {
		t.Fatalf("Schedules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d schedules, want the cap of 2", len(got))
	}
}
