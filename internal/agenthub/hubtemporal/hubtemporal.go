// Package hubtemporal is the driven adapter that answers the Agent Hub's live
// read ports from the orchestrator: what is running right now
// (agenthub.ExecutionSource) and which schedules exist (agenthub.ScheduleSource).
//
// It is the only part of the read path that knows Temporal exists. Execution
// statuses, cancellations, timeouts and terminations are all translated into the
// API's own outcome vocabulary here, and the workflow-ID convention supplies each
// execution's class, so the core (and therefore the published contract) is free of
// both. It only ever reads: no workflow is started, signalled, or instrumented to
// make this API possible.
package hubtemporal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/wfid"
)

// runningQuery selects the in-flight executions. It is the same visibility query
// the CLI's live view uses, so the API and `list` answer "what is running" the same
// way.
const runningQuery = "ExecutionStatus='Running'"

// scheduledByAttribute is the search attribute Temporal stamps on a workflow it
// started from a schedule. Reading it is what lets a schedule-fired run be
// attributed to its schedule from the live listing alone, so the overview shows one
// satellite for the schedule rather than one per firing.
const scheduledByAttribute = "TemporalScheduledById"

// WorkflowClient is the slice of the orchestration client this adapter needs. It
// is declared here, as narrowly as possible, so the adapter's own tests can drive
// it with a stand-in instead of a server, and so the dependency is visible rather
// than "the whole SDK client".
type WorkflowClient interface {
	// ListWorkflow returns a page of executions matching a visibility query.
	ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)
	// DescribeWorkflowExecution returns one execution's current state.
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
}

// ScheduleClient is the slice of Temporal's raw service needed for native-token
// schedule paging. The SDK iterator hides this token, so it cannot implement a
// bounded HTTP cursor without restarting the scan on every request.
type ScheduleClient interface {
	ListSchedules(ctx context.Context, request *workflowservice.ListSchedulesRequest, options ...grpc.CallOption) (*workflowservice.ListSchedulesResponse, error)
}

// Executions answers agenthub.ExecutionSource from the orchestrator.
type Executions struct {
	client WorkflowClient
}

// Schedules answers agenthub.ScheduleSource from the orchestrator.
type Schedules struct {
	client    ScheduleClient
	namespace string
}

// Compile-time proof the adapters satisfy the ports they are injected as.
var (
	_ agenthub.ExecutionSource = (*Executions)(nil)
	_ agenthub.ScheduleSource  = (*Schedules)(nil)
)

// NewExecutions returns the live execution source.
func NewExecutions(c WorkflowClient) (*Executions, error) {
	if c == nil {
		return nil, errors.New("the orchestration client is required")
	}
	return &Executions{client: c}, nil
}

// NewSchedules returns the schedule source.
func NewSchedules(c ScheduleClient, namespace string) (*Schedules, error) {
	if c == nil {
		return nil, errors.New("the schedule client is required")
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, errors.New("the Temporal namespace is required")
	}
	return &Schedules{client: c, namespace: namespace}, nil
}

// RunningExecutions implements agenthub.ExecutionSource. It pages through the
// visibility listing until the cap is reached, so no request can start an
// unbounded orchestration scan.
func (e *Executions) RunningExecutions(ctx context.Context, limit int) ([]agenthub.Execution, error) {
	if limit <= 0 {
		limit = agenthub.MaxLimit
	}
	var out []agenthub.Execution
	var next []byte
	for {
		resp, err := e.client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         runningQuery,
			PageSize:      int32(limit - len(out)),
			NextPageToken: next,
		})
		if err != nil {
			return nil, fmt.Errorf("list the running executions: %w", err)
		}
		for _, info := range resp.GetExecutions() {
			out = append(out, executionFrom(info))
			if limit > 0 && len(out) == limit {
				return out, nil
			}
		}
		next = resp.GetNextPageToken()
		if len(next) == 0 {
			return out, nil
		}
	}
}

// RunningPage implements source-native paging with one Temporal visibility call.
func (e *Executions) RunningPage(ctx context.Context, query agenthub.ExecutionPageQuery) (agenthub.ExecutionPage, error) {
	resp, err := e.client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Query:         runningQuery,
		PageSize:      int32(query.Limit),
		NextPageToken: query.Cursor,
	})
	if err != nil {
		var invalid *serviceerror.InvalidArgument
		if errors.As(err, &invalid) {
			return agenthub.ExecutionPage{}, fmt.Errorf("%w: cursor is invalid", agenthub.ErrInvalid)
		}
		return agenthub.ExecutionPage{}, fmt.Errorf("list a page of running executions: %w", err)
	}
	if len(resp.GetExecutions()) > query.Limit {
		return agenthub.ExecutionPage{}, errors.New("the orchestration server returned more executions than requested")
	}
	page := agenthub.ExecutionPage{
		Items: make([]agenthub.Execution, 0, len(resp.GetExecutions())),
		Next:  append([]byte(nil), resp.GetNextPageToken()...),
	}
	for _, info := range resp.GetExecutions() {
		page.Items = append(page.Items, executionFrom(info))
	}
	return page, nil
}

// Execution implements agenthub.ExecutionSource. An execution the orchestrator does
// not know is reported as agenthub.ErrNoExecution rather than as a failure: "it is
// gone" is an answer the reader acts on, while an error would make a whole
// collection unreadable because one item aged out.
func (e *Executions) Execution(ctx context.Context, workflowID string) (agenthub.Execution, error) {
	// An empty run ID asks for the latest iteration of the chain, which is exactly
	// the one whose status the overview shows.
	resp, err := e.client.DescribeWorkflowExecution(ctx, workflowID, "")
	var notFound *serviceerror.NotFound
	switch {
	case errors.As(err, &notFound):
		return agenthub.Execution{}, agenthub.ErrNoExecution
	case err != nil:
		return agenthub.Execution{}, fmt.Errorf("describe the execution %s: %w", workflowID, err)
	}
	info := resp.GetWorkflowExecutionInfo()
	if info == nil {
		return agenthub.Execution{}, agenthub.ErrNoExecution
	}
	return executionFrom(info), nil
}

// Executions implements the batch half of agenthub.ExecutionSource with bounded
// concurrency, so a page of stale durable records does not become sequential
// orchestration round trips.
func (e *Executions) Executions(ctx context.Context, workflowIDs []string) (map[string]agenthub.Execution, error) {
	out := make(map[string]agenthub.Execution, len(workflowIDs))
	if len(workflowIDs) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	errs := make(chan error, 1)
	var mu sync.Mutex
	var workers sync.WaitGroup
	workerCount := min(len(workflowIDs), 16)
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for workflowID := range jobs {
				execution, err := e.Execution(ctx, workflowID)
				switch {
				case errors.Is(err, agenthub.ErrNoExecution):
					continue
				case err != nil:
					select {
					case errs <- err:
						cancel()
					default:
					}
					return
				}
				mu.Lock()
				out[workflowID] = execution
				mu.Unlock()
			}
		}()
	}
	for _, workflowID := range workflowIDs {
		select {
		case jobs <- workflowID:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Schedules implements the capped non-paged overview read with one source call.
func (s *Schedules) Schedules(ctx context.Context, limit int) ([]agenthub.ScheduleState, error) {
	if limit <= 0 {
		limit = agenthub.MaxLimit
	}
	page, err := s.SchedulePage(ctx, agenthub.SchedulePageQuery{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

const (
	scheduleRetryAttempts = 3
	scheduleRetryDelay    = 25 * time.Millisecond
)

// SchedulePage implements source-native paging with one bounded Temporal service
// call and bounded retries for transient raw-service failures.
func (s *Schedules) SchedulePage(ctx context.Context, query agenthub.SchedulePageQuery) (agenthub.ScheduleStatePage, error) {
	resp, err := s.listSchedules(ctx, &workflowservice.ListSchedulesRequest{
		Namespace:       s.namespace,
		MaximumPageSize: int32(query.Limit),
		NextPageToken:   query.Cursor,
	})
	if err != nil {
		var invalid *serviceerror.InvalidArgument
		if errors.As(err, &invalid) {
			return agenthub.ScheduleStatePage{}, fmt.Errorf("%w: cursor is invalid", agenthub.ErrInvalid)
		}
		return agenthub.ScheduleStatePage{}, fmt.Errorf("list a page of schedules: %w", err)
	}
	if len(resp.GetSchedules()) > query.Limit {
		return agenthub.ScheduleStatePage{}, errors.New("the orchestration server returned more schedules than requested")
	}
	page := agenthub.ScheduleStatePage{
		Items: make([]agenthub.ScheduleState, 0, len(resp.GetSchedules())),
		Next:  append([]byte(nil), resp.GetNextPageToken()...),
	}
	for _, entry := range resp.GetSchedules() {
		if entry != nil {
			page.Items = append(page.Items, scheduleStateFrom(entry))
		}
	}
	return page, nil
}

func (s *Schedules) listSchedules(ctx context.Context, request *workflowservice.ListSchedulesRequest) (*workflowservice.ListSchedulesResponse, error) {
	var lastErr error
	for attempt := 0; attempt < scheduleRetryAttempts; attempt++ {
		response, err := s.client.ListSchedules(ctx, request)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if !retryableScheduleError(err) || attempt == scheduleRetryAttempts-1 {
			return nil, err
		}
		delay := scheduleRetryDelay << attempt
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func retryableScheduleError(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// scheduleStateFrom translates a raw schedule list entry into the port's state.
func scheduleStateFrom(entry *schedulepb.ScheduleListEntry) agenthub.ScheduleState {
	info := entry.GetInfo()
	state := agenthub.ScheduleState{
		ID:     entry.GetScheduleId(),
		Spec:   describeScheduleSpec(info.GetSpec()),
		Paused: info.GetPaused(),
	}
	actions := info.GetRecentActions()
	if n := len(actions); n > 0 {
		last := actions[n-1]
		if actual := last.GetActualTime(); actual != nil {
			state.LastRunAt = actual.AsTime()
		} else if scheduled := last.GetScheduleTime(); scheduled != nil {
			state.LastRunAt = scheduled.AsTime()
		}
	}
	for i := len(actions) - 1; i >= 0; i-- {
		outcome, known := scheduleActionOutcome(actions[i].GetStartWorkflowStatus())
		if !known {
			continue
		}
		if outcome == agenthub.OutcomeRunning {
			state.RunningActions++
			continue
		}
		if state.LastOutcome == "" {
			state.LastOutcome = outcome
		}
	}
	for _, future := range info.GetFutureActionTimes() {
		if future == nil {
			continue
		}
		candidate := future.AsTime()
		if state.NextRunAt.IsZero() || candidate.Before(state.NextRunAt) {
			state.NextRunAt = candidate
		}
	}
	return state
}

func scheduleActionOutcome(status enums.WorkflowExecutionStatus) (agenthub.ExecutionOutcome, bool) {
	if status == enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED {
		return "", false
	}
	return outcomeFrom(status), true
}

func describeScheduleSpec(spec *schedulepb.ScheduleSpec) string {
	if spec == nil {
		return ""
	}
	if cron := spec.GetCronString(); len(cron) > 0 {
		return strings.Join(cron, ", ")
	}
	var parts []string
	for _, interval := range spec.GetInterval() {
		if every := interval.GetInterval(); every != nil && every.AsDuration() > 0 {
			parts = append(parts, "every "+every.AsDuration().String())
		}
	}
	for _, calendar := range spec.GetCalendar() {
		fields := []struct {
			name  string
			value string
		}{
			{"minute", calendar.GetMinute()},
			{"hour", calendar.GetHour()},
			{"dayOfMonth", calendar.GetDayOfMonth()},
			{"month", calendar.GetMonth()},
			{"year", calendar.GetYear()},
			{"dayOfWeek", calendar.GetDayOfWeek()},
		}
		var values []string
		for _, field := range fields {
			if field.value != "" {
				values = append(values, field.name+"="+field.value)
			}
		}
		if len(values) > 0 {
			parts = append(parts, strings.Join(values, " "))
		}
	}
	for _, calendar := range spec.GetStructuredCalendar() {
		if rendered := describeStructuredCalendar(calendar); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, ", ")
}

func describeStructuredCalendar(calendar *schedulepb.StructuredCalendarSpec) string {
	if calendar == nil {
		return ""
	}
	fields := []struct {
		name   string
		ranges []*schedulepb.Range
	}{
		{"minute", calendar.GetMinute()},
		{"hour", calendar.GetHour()},
		{"dayOfMonth", calendar.GetDayOfMonth()},
		{"month", calendar.GetMonth()},
		{"year", calendar.GetYear()},
		{"dayOfWeek", calendar.GetDayOfWeek()},
	}
	var parts []string
	for _, field := range fields {
		if len(field.ranges) == 0 {
			continue
		}
		values := make([]string, 0, len(field.ranges))
		for _, item := range field.ranges {
			value := fmt.Sprintf("%d", item.GetStart())
			if item.GetEnd() > item.GetStart() {
				value = fmt.Sprintf("%d-%d", item.GetStart(), item.GetEnd())
			}
			if item.GetStep() > 1 {
				value += fmt.Sprintf("/%d", item.GetStep())
			}
			values = append(values, value)
		}
		parts = append(parts, field.name+"="+strings.Join(values, "/"))
	}
	return strings.Join(parts, " ")
}

// executionFrom translates one execution's visibility info into the API's execution
// type. The label is deliberately left empty: what was asked of the agent lives in
// the execution's input, which reading would mean pulling a history per item, and
// the durable record already carries it.
func executionFrom(info *workflowpb.WorkflowExecutionInfo) agenthub.Execution {
	execution := agenthub.Execution{
		WorkflowID: info.GetExecution().GetWorkflowId(),
		RunID:      info.GetExecution().GetRunId(),
		FirstRunID: info.GetFirstRunId(),
		Outcome:    outcomeFrom(info.GetStatus()),
		ScheduleID: scheduleIDFrom(info),
	}
	if started := info.GetStartTime(); started != nil {
		execution.StartedAt = started.AsTime()
	}
	execution.Class = wfid.Classify(execution.WorkflowID)
	if parent := info.GetParentExecution(); parent != nil {
		execution.ParentWorkflowID = parent.GetWorkflowId()
	}
	if closed := info.GetCloseTime(); closed != nil {
		execution.EndedAt = closed.AsTime()
	}
	return execution
}

// outcomeFrom maps an orchestration status onto the API's outcome vocabulary.
//
// A cancellation, a timeout and a termination are all failures: the work did not
// complete, and the distinction between the ways it stopped is not one the overview
// draws. An execution that continued as new is still running — the chain is the
// item, and its next iteration has taken over.
func outcomeFrom(status enums.WorkflowExecutionStatus) agenthub.ExecutionOutcome {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING, enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return agenthub.OutcomeRunning
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return agenthub.OutcomeSucceeded
	default:
		return agenthub.OutcomeFailed
	}
}

// scheduleIDFrom reads the schedule that started an execution out of its search
// attributes, returning "" for an execution nothing scheduled.
func scheduleIDFrom(info *workflowpb.WorkflowExecutionInfo) string {
	payload, ok := info.GetSearchAttributes().GetIndexedFields()[scheduledByAttribute]
	if !ok {
		return ""
	}
	var scheduleID string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &scheduleID); err != nil {
		// The attribute is a plain keyword; anything else is not a schedule ID this
		// adapter can use, and guessing would attribute a run to the wrong schedule.
		return ""
	}
	return scheduleID
}
