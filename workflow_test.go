package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/piagent"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
	"temporal-agents/internal/wftest"
)

// pna references the notification activity's method name for OnActivity and for
// the negative assertions.
var pna *notification.Activity

// newPromptEnv builds a test environment with every activity PromptWorkflow uses
// registered, around a throwaway store, for the tests that are not about the
// durable record.
func newPromptEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	return newPromptEnvWithStore(t, execstoretest.New())
}

// newPromptEnvWithStore builds it around the given store, so a test asserts on the
// record that was written rather than on the activity call that wrote it — the same
// way the codereview and fleet suites do. The real PersistRunWorkflowState activity
// runs against the in-memory port, which is what makes those assertions possible;
// execstoretest.Failing and FailingAfter stand in for an outage.
func newPromptEnvWithStore(t *testing.T, store *execstoretest.Store) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	// The location probe is registered like any other driven adapter, so the tests
	// exercise the real recording path: a run records the place its probe answered.
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	return env
}

func TestPromptWorkflow_Complete_SendsRunNotification(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "the agent output", Tokens: 12345}, nil)
	var got notification.Notification
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// A finished (non-chained) run notifies with the agent's output plus the
	// total token usage as the body.
	require.Equal(t, "Run complete", got.Title)
	require.Contains(t, got.Body, "the agent output")
	require.Contains(t, got.Body, "Total token usage across all sessions: 12,345 tokens.")
}

func TestPromptWorkflow_Complete_RecordsStartAndTerminalState(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "the agent output", Tokens: 12345}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow,
		PromptRequest{Prompt: "summarize", WorkDir: "/repo", ScheduleID: "schedule-7"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The run records itself twice: as started before the agent runs, and with its
	// terminal outcome once it settles.
	recs := store.Records()
	require.Len(t, recs, 2)

	start := recs[0]
	require.Equal(t, execstore.KindRun, start.Kind)
	require.Equal(t, execstore.StatusRunning, start.Status)
	require.Equal(t, "summarize", start.Prompt)
	require.Equal(t, "schedule-7", start.ScheduleID)
	require.NotEmpty(t, start.WorkflowID)
	require.NotEmpty(t, start.RunID)
	require.NotEmpty(t, start.FirstRunID)
	require.False(t, start.StartedAt.IsZero())
	require.True(t, start.EndedAt.IsZero())
	require.Zero(t, start.Tokens)

	end := recs[1]
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, start.RunID, end.RunID, "both writes key on the same run ID so the second upserts the first")
	require.False(t, end.EndedAt.IsZero())
	require.Equal(t, 12345, end.Tokens)
	require.Empty(t, end.Detail.Error)
}

func TestPromptWorkflow_RecordsWhereTheRunRanFromTheFirstWrite(t *testing.T) {
	// The place is established before the run is recorded as started, so a run is in
	// its place on the overview while it is still running — not only once it settles.
	store := execstoretest.New()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{
		Prober: placetest.New().InWorktree("/srv/worktrees/fix", "/srv/repos/pricing"),
	})
	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow,
		PromptRequest{Prompt: "summarize", WorkDir: "/srv/worktrees/fix"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	start := store.Records()[0]
	require.Equal(t, "/srv/worktrees/fix", start.Detail.Directory)
	require.Equal(t, "/srv/repos/pricing", start.Detail.Repository)
}

func TestPromptWorkflow_AProbeThatCannotAnswerLeavesTheRunPlaceless(t *testing.T) {
	// A place is bookkeeping. A run outside any repository still runs, still records
	// itself, and is simply shown as being in the unknown place.
	store := execstoretest.New()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.Failing(errors.New("git is not available"))})
	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/tmp/scratch"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a failed probe must never fail the run it describes")
	require.Len(t, store.Records(), 2)
	require.Empty(t, store.Last(t).Detail.Directory)
}

func TestPromptWorkflow_RecordsOwnTokensNotTheChainTotal(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output", Tokens: 500}, nil)

	// A chained iteration inherits the accumulated total of every earlier
	// iteration; the record must carry only this iteration's own usage so summing
	// the rows of a chain gives a true total instead of counting earlier runs again.
	env.ExecuteWorkflow(PromptWorkflow,
		PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true, TokensSoFar: 9000})

	require.True(t, env.IsWorkflowCompleted())
	require.Len(t, store.Records(), 2)
	end := store.Last(t)
	require.Equal(t, 500, end.Tokens)
	// Continuing as new is a control signal, not a failure: this iteration's work
	// landed, so it is recorded as succeeded and the next iteration becomes a row
	// of its own.
	require.Equal(t, execstore.StatusSucceeded, end.Status)
}

func TestPromptWorkflow_Failure_RecordsFailedState(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{}, errors.New("pi crashed"))
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Len(t, store.Records(), 2)
	end := store.Last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	require.Contains(t, end.Detail.Error, "pi crashed")
}

func TestPromptWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	// Recording is a hard dependency, not best-effort: a store that cannot be
	// written must fail the run rather than let it complete unrecorded.
	env := newPromptEnvWithStore(t, execstoretest.Failing(errors.New("postgres is down")))

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	// The agent never runs: the run is recorded as started first, so an
	// unrecordable run does no work at all.
	env.AssertNotCalled(t, wftest.ActivityName(RunPiAgent), mock.Anything, mock.Anything)
}

func TestPromptWorkflow_TerminalRecordFailure_StillReturnsTheResult(t *testing.T) {
	// The start write lands, the terminal one does not. The agent has done its work
	// by then, so the bookkeeping failure must not throw the result away.
	env := newPromptEnvWithStore(t, execstoretest.FailingAfter(1, errors.New("postgres is down")))

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "the agent output", Tokens: 12345}, nil)
	var got notification.Notification
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "the agent output")
	// The failure is reported rather than swallowed, and the notification carries
	// the result the record no longer holds.
	require.Contains(t, got.Title, "Record not written")
	require.Contains(t, got.Body, "postgres is down")
	require.Contains(t, got.Body, "the agent output")
}

func TestPromptWorkflow_Chain_TerminalRecordFailure_StillContinuesTheChain(t *testing.T) {
	env := newPromptEnvWithStore(t, execstoretest.FailingAfter(1, errors.New("postgres is down")))

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output", Tokens: 500}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// An unrecordable iteration must not break the chain: continue-as-new is the
	// control signal that keeps it running.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
}

func TestPromptWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{}, errors.New("pi crashed"))
	var got notification.Notification
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed run notifies best-effort with the error as the body.
	require.Equal(t, "Run failed", got.Title)
	require.Contains(t, got.Body, "pi crashed")
}

func TestPromptWorkflow_Cancelled_StillSendsFailureNotification(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	// Keep the agent in flight so the workflow is still running when it is
	// cancelled; the in-flight activity then fails with a cancellation error.
	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		After(time.Hour).
		Return(piagent.Result{}, nil)
	var got notification.Notification
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	// Cancel the workflow while the agent activity is still running.
	env.RegisterDelayedCallback(func() { env.CancelWorkflow() }, time.Second)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Cancellation schedules the failure notify on a disconnected context, so it
	// still fires even though the workflow context itself was cancelled.
	require.Equal(t, "Run failed", got.Title)
	// The terminal record is written on a disconnected context for the same
	// reason, so a cancelled run settles instead of being left "running" forever.
	require.Len(t, store.Records(), 2)
	require.Equal(t, execstore.StatusFailed, store.Last(t).Status)
}

func TestPromptWorkflow_Chain_ContinuesAsNewWithoutNotifying(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Chaining loops via continue-as-new, so the run has not terminated and must
	// not notify yet.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertNotCalled(t, wftest.ActivityName(pna.Notify), mock.Anything, mock.Anything)
}

func TestPromptWorkflow_ScheduleFiredRun_RecordsItsSchedule(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	// A schedule fires the same workflow `run` uses, so the fired execution is a
	// run carrying the schedule that produced it — not a kind of its own.
	env.ExecuteWorkflow(PromptWorkflow, scheduleAction("schedule-9", "digest", "/repo", false).Args[0])

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	recs := store.Records()
	require.Len(t, recs, 2)
	require.Equal(t, "schedule-9", recs[0].ScheduleID)
	require.Equal(t, "schedule-9", recs[1].ScheduleID)
}

func TestPromptWorkflow_DirectRun_RecordsNoSchedule(t *testing.T) {
	store := execstoretest.New()
	env := newPromptEnvWithStore(t, store)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, runRequest("summarize", "/repo", false))

	require.True(t, env.IsWorkflowCompleted())
	require.Len(t, store.Records(), 2)
	require.Empty(t, store.Last(t).ScheduleID, "nothing fired this run, so it belongs to no schedule")
}
