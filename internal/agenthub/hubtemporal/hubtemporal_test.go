package hubtemporal

import (
	"context"
	"errors"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
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
	pages      []*workflowservice.ListWorkflowExecutionsResponse
	calls      int
	queries    []string
	pageSizes  []int32
	pageTokens [][]byte
	describe   *workflowpb.WorkflowExecutionInfo
	err        error
}

func (f *fakeWorkflows) ListWorkflow(_ context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.queries = append(f.queries, request.GetQuery())
	f.pageSizes = append(f.pageSizes, request.GetPageSize())
	f.pageTokens = append(f.pageTokens, append([]byte(nil), request.GetNextPageToken()...))
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

// fakeSchedules is a stand-in for Temporal's raw schedule service.
type fakeSchedules struct {
	pages      []*workflowservice.ListSchedulesResponse
	calls      int
	pageSizes  []int32
	pageTokens [][]byte
	namespaces []string
	err        error
}

func (f *fakeSchedules) ListSchedules(_ context.Context, request *workflowservice.ListSchedulesRequest, _ ...grpc.CallOption) (*workflowservice.ListSchedulesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.pageSizes = append(f.pageSizes, request.GetMaximumPageSize())
	f.pageTokens = append(f.pageTokens, append([]byte(nil), request.GetNextPageToken()...))
	f.namespaces = append(f.namespaces, request.GetNamespace())
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

// TestConstructorsRequireAClient pins the fail-fast wiring.
func TestConstructorsRequireAClient(t *testing.T) {
	if _, err := NewExecutions(nil); err == nil {
		t.Error("NewExecutions(nil) = nil, want an error")
	}
	if _, err := NewSchedules(nil, client.DefaultNamespace); err == nil {
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
		FirstRunId:      "first-run-1",
		Status:          enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		StartTime:       timestamppb.New(started),
		CloseTime:       timestamppb.New(closed),
		ParentExecution: &commonpb.WorkflowExecution{WorkflowId: fleetID},
	}

	got := executionFrom(info)
	switch {
	case got.WorkflowID != fleetID+"-api" || got.RunID != "run-1" || got.FirstRunID != "first-run-1":
		t.Errorf("identity = %q/%q/%q, want the node's", got.WorkflowID, got.RunID, got.FirstRunID)
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

func TestRunningPageReadsOneBoundedTemporalPage(t *testing.T) {
	response := &workflowservice.ListWorkflowExecutionsResponse{
		Executions: []*workflowpb.WorkflowExecutionInfo{{
			Execution: &commonpb.WorkflowExecution{WorkflowId: "run-1"},
			Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		}},
		NextPageToken: []byte("next-native-token"),
	}
	fake := &fakeWorkflows{pages: []*workflowservice.ListWorkflowExecutionsResponse{response}}
	source, err := NewExecutions(fake)
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}

	page, err := source.RunningPage(context.Background(), agenthub.ExecutionPageQuery{
		Limit: 1, Cursor: []byte("current-native-token"),
	})
	if err != nil {
		t.Fatalf("RunningPage: %v", err)
	}
	if fake.calls != 1 || fake.pageSizes[0] != 1 || string(fake.pageTokens[0]) != "current-native-token" {
		t.Fatalf("Temporal requests = %d size=%v token=%q, want one size-1 request with the source token",
			fake.calls, fake.pageSizes, fake.pageTokens[0])
	}
	if len(page.Items) != 1 || page.Items[0].WorkflowID != "run-1" || string(page.Next) != "next-native-token" {
		t.Fatalf("page = %+v, want run-1 and the native next token", page)
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

// TestSourcePagesClassifyRejectedNativeTokensAsInvalid pins that a bad opaque
// source token is a caller error rather than a dependency outage.
func TestSourcePagesClassifyRejectedNativeTokensAsInvalid(t *testing.T) {
	invalid := serviceerror.NewInvalidArgument("bad page token")
	executions, err := NewExecutions(&fakeWorkflows{err: invalid})
	if err != nil {
		t.Fatalf("NewExecutions: %v", err)
	}
	_, err = executions.RunningPage(context.Background(), agenthub.ExecutionPageQuery{Limit: 1, Cursor: []byte("bad")})
	if !errors.Is(err, agenthub.ErrInvalid) {
		t.Fatalf("RunningPage error = %v, want ErrInvalid", err)
	}

	schedules, err := NewSchedules(&fakeSchedules{err: invalid}, client.DefaultNamespace)
	if err != nil {
		t.Fatalf("NewSchedules: %v", err)
	}
	_, err = schedules.SchedulePage(context.Background(), agenthub.SchedulePageQuery{Limit: 1, Cursor: []byte("bad")})
	if !errors.Is(err, agenthub.ErrInvalid) {
		t.Fatalf("SchedulePage error = %v, want ErrInvalid", err)
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
	unknownFired := lastFired.Add(time.Hour)
	next := lastFired.Add(24 * time.Hour)
	entry := &schedulepb.ScheduleListEntry{
		ScheduleId: "schedule-1",
		Info: &schedulepb.ScheduleListInfo{
			Paused: true,
			Spec:   &schedulepb.ScheduleSpec{CronString: []string{"0 9 * * *"}},
			RecentActions: []*schedulepb.ScheduleActionResult{
				{ScheduleTime: timestamppb.New(lastFired.Add(-24 * time.Hour)), ActualTime: timestamppb.New(lastFired.Add(-24 * time.Hour)), StartWorkflowStatus: enums.WORKFLOW_EXECUTION_STATUS_RUNNING},
				{ScheduleTime: timestamppb.New(lastFired), ActualTime: timestamppb.New(lastFired), StartWorkflowStatus: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED},
				{ScheduleTime: timestamppb.New(unknownFired), ActualTime: timestamppb.New(unknownFired), StartWorkflowStatus: enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED},
			},
			FutureActionTimes: []*timestamppb.Timestamp{timestamppb.New(next.Add(48 * time.Hour)), timestamppb.New(next)},
		},
	}
	source, err := NewSchedules(&fakeSchedules{pages: []*workflowservice.ListSchedulesResponse{{Schedules: []*schedulepb.ScheduleListEntry{entry}}}}, client.DefaultNamespace)
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
	case !state.LastRunAt.Equal(unknownFired):
		t.Errorf("lastRunAt = %v, want the most recent action %v", state.LastRunAt, unknownFired)
	case !state.NextRunAt.Equal(next):
		t.Errorf("nextRunAt = %v, want the earliest upcoming action %v", state.NextRunAt, next)
	case state.RunningActions != 1 || state.LastOutcome != agenthub.OutcomeSucceeded:
		t.Errorf("action state = running %d, outcome %q; want 1/succeeded", state.RunningActions, state.LastOutcome)
	}
}

// TestDescribeScheduleSpecRendersWhatWasConfigured pins raw schedule spec
// translation for cron, interval, structured calendar, and empty forms.
func TestDescribeScheduleSpecRendersWhatWasConfigured(t *testing.T) {
	cases := []struct {
		name string
		in   *schedulepb.ScheduleSpec
		want string
	}{
		{"nothing at all", nil, ""},
		{"a cron expression", &schedulepb.ScheduleSpec{CronString: []string{"0 9 * * *"}}, "0 9 * * *"},
		{"an interval", &schedulepb.ScheduleSpec{Interval: []*schedulepb.IntervalSpec{{Interval: durationpb.New(90 * time.Minute)}}}, "every 1h30m0s"},
		{
			"a calendar",
			&schedulepb.ScheduleSpec{StructuredCalendar: []*schedulepb.StructuredCalendarSpec{{
				Minute: []*schedulepb.Range{{Start: 0}},
				Hour:   []*schedulepb.Range{{Start: 9, End: 17, Step: 2}},
			}}},
			"minute=0 hour=9-17/2",
		},
		{"an empty spec", &schedulepb.ScheduleSpec{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeScheduleSpec(tc.in); got != tc.want {
				t.Fatalf("describeScheduleSpec = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSchedulePageReadsOneBoundedTemporalPage(t *testing.T) {
	fake := &fakeSchedules{pages: []*workflowservice.ListSchedulesResponse{{
		Schedules:     []*schedulepb.ScheduleListEntry{{ScheduleId: "schedule-2"}},
		NextPageToken: []byte("next-native-token"),
	}}}
	source, err := NewSchedules(fake, "team-namespace")
	if err != nil {
		t.Fatalf("NewSchedules: %v", err)
	}

	page, err := source.SchedulePage(context.Background(), agenthub.SchedulePageQuery{
		Limit: 1, Cursor: []byte("current-native-token"),
	})
	if err != nil {
		t.Fatalf("SchedulePage: %v", err)
	}
	if fake.calls != 1 || fake.pageSizes[0] != 1 || string(fake.pageTokens[0]) != "current-native-token" || fake.namespaces[0] != "team-namespace" {
		t.Fatalf("Temporal requests = %d size=%v token=%q namespace=%q, want one bounded native-token request",
			fake.calls, fake.pageSizes, fake.pageTokens[0], fake.namespaces[0])
	}
	if len(page.Items) != 1 || page.Items[0].ID != "schedule-2" || string(page.Next) != "next-native-token" {
		t.Fatalf("page = %+v, want schedule-2 and the native next token", page)
	}
}
