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
	"sort"
	"strings"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

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

// ScheduleLister is the slice of the schedule client this adapter needs: listing
// is enough, because a list entry already carries whether a schedule is paused,
// when it fires next and what it fired recently.
type ScheduleLister interface {
	// List returns an iterator over the configured schedules.
	List(ctx context.Context, options client.ScheduleListOptions) (client.ScheduleListIterator, error)
}

// Executions answers agenthub.ExecutionSource from the orchestrator.
type Executions struct {
	client WorkflowClient
}

// Schedules answers agenthub.ScheduleSource from the orchestrator.
type Schedules struct {
	client ScheduleLister
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
func NewSchedules(c ScheduleLister) (*Schedules, error) {
	if c == nil {
		return nil, errors.New("the schedule client is required")
	}
	return &Schedules{client: c}, nil
}

// RunningExecutions implements agenthub.ExecutionSource. It pages through the
// visibility listing until the cap is reached, so one request can never turn into
// an unbounded scan of a busy orchestrator.
func (e *Executions) RunningExecutions(ctx context.Context, limit int) ([]agenthub.Execution, error) {
	if limit <= 0 {
		limit = agenthub.MaxLimit
	}
	var out []agenthub.Execution
	var next []byte
	for {
		resp, err := e.client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         runningQuery,
			NextPageToken: next,
		})
		if err != nil {
			return nil, fmt.Errorf("list the running executions: %w", err)
		}
		for _, info := range resp.GetExecutions() {
			out = append(out, executionFrom(info))
			if len(out) == limit {
				return out, nil
			}
		}
		next = resp.GetNextPageToken()
		if len(next) == 0 {
			return out, nil
		}
	}
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

// Schedules implements agenthub.ScheduleSource.
//
// RunningActions is deliberately left at zero: a list entry does not say whether an
// action is in flight, and the reader already knows — it has the running executions,
// each attributed to the schedule that fired it. Describing every schedule here to
// re-derive that would cost a round trip per schedule for a fact already in hand.
func (s *Schedules) Schedules(ctx context.Context, limit int) ([]agenthub.ScheduleState, error) {
	if limit <= 0 {
		limit = agenthub.MaxLimit
	}
	iter, err := s.client.List(ctx, client.ScheduleListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list the schedules: %w", err)
	}
	var out []agenthub.ScheduleState
	for iter.HasNext() {
		entry, err := iter.Next()
		if err != nil {
			return nil, fmt.Errorf("read a schedule: %w", err)
		}
		if entry == nil {
			continue
		}
		out = append(out, scheduleStateFrom(entry))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// scheduleStateFrom translates a schedule list entry into the port's state type.
func scheduleStateFrom(entry *client.ScheduleListEntry) agenthub.ScheduleState {
	state := agenthub.ScheduleState{
		ID:     entry.ID,
		Spec:   describeSpec(entry.Spec),
		Paused: entry.Paused,
	}
	// RecentActions is sorted oldest first, so the last entry is the latest firing.
	if n := len(entry.RecentActions); n > 0 {
		last := entry.RecentActions[n-1]
		state.LastRunAt = last.ActualTime
		if state.LastRunAt.IsZero() {
			state.LastRunAt = last.ScheduleTime
		}
	}
	// NextActionTimes holds the upcoming firings; the earliest one is what an
	// operator is waiting for.
	if times := entry.NextActionTimes; len(times) > 0 {
		sorted := append([]time.Time(nil), times...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
		state.NextRunAt = sorted[0]
	}
	return state
}

// describeSpec renders when a schedule fires as the text it was configured with:
// its cron expressions, or its interval, or — for a calendar-based schedule nothing
// simpler describes — a compact rendering of the calendar fields. It returns "" when
// there is nothing to say, so a consumer shows no claim rather than an invented one.
func describeSpec(spec *client.ScheduleSpec) string {
	if spec == nil {
		return ""
	}
	if len(spec.CronExpressions) > 0 {
		return strings.Join(spec.CronExpressions, ", ")
	}
	var parts []string
	for _, interval := range spec.Intervals {
		if interval.Every > 0 {
			parts = append(parts, "every "+interval.Every.String())
		}
	}
	for _, calendar := range spec.Calendars {
		if rendered := describeCalendar(calendar); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, ", ")
}

// describeCalendar renders the fields of a calendar spec that were actually set,
// as "field=value" pairs, which says exactly as much as the schedule does.
func describeCalendar(calendar client.ScheduleCalendarSpec) string {
	fields := []struct {
		name   string
		ranges []client.ScheduleRange
	}{
		{"minute", calendar.Minute},
		{"hour", calendar.Hour},
		{"dayOfMonth", calendar.DayOfMonth},
		{"month", calendar.Month},
		{"year", calendar.Year},
		{"dayOfWeek", calendar.DayOfWeek},
	}
	var parts []string
	for _, field := range fields {
		if len(field.ranges) == 0 {
			continue
		}
		var values []string
		for _, r := range field.ranges {
			value := fmt.Sprintf("%d", r.Start)
			if r.End > r.Start {
				value = fmt.Sprintf("%d-%d", r.Start, r.End)
			}
			if r.Step > 1 {
				value += fmt.Sprintf("/%d", r.Step)
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
