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
	"temporal-agents/internal/notification"
	"temporal-agents/internal/piagent"
)

// ra references the root activity bundle's method names for OnActivity; the real
// methods are never invoked because every call is mocked.
var ra *Activities

// newPromptEnv builds a test environment with every activity PromptWorkflow uses
// registered, and the durable recording activity mocked as succeeding. Tests that
// care about the records override the mock or capture what it received.
func newPromptEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&Activities{})
	env.RegisterActivity(&notification.Activity{})
	return env
}

// recordRunStates captures every RunState the workflow persists, in order, so a
// test can assert on the start and terminal writes.
func recordRunStates(env *testsuite.TestWorkflowEnvironment, states *[]RunState) {
	env.OnActivity(ra.PersistRunWorkflowState, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			*states = append(*states, args.Get(1).(RunState))
		}).Return(nil)
}

func TestPromptWorkflow_Complete_SendsRunNotification(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "the agent output", Tokens: 12345}, nil)
	env.OnActivity(ra.PersistRunWorkflowState, mock.Anything, mock.Anything).Return(nil)
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
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "the agent output", Tokens: 12345}, nil)
	var states []RunState
	recordRunStates(env, &states)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow,
		PromptRequest{Prompt: "summarize", WorkDir: "/repo", ScheduleID: "schedule-7"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The run records itself twice: as started before the agent runs, and with its
	// terminal outcome once it settles.
	require.Len(t, states, 2)

	start := states[0]
	require.Equal(t, execstore.StatusRunning, start.Status)
	require.Equal(t, "summarize", start.Prompt)
	require.Equal(t, "schedule-7", start.ScheduleID)
	require.NotEmpty(t, start.WorkflowID)
	require.NotEmpty(t, start.RunID)
	require.False(t, start.StartedAt.IsZero())
	require.True(t, start.EndedAt.IsZero())
	require.Zero(t, start.Tokens)

	end := states[1]
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, start.RunID, end.RunID, "both writes key on the same run ID so the second upserts the first")
	require.False(t, end.EndedAt.IsZero())
	require.Equal(t, 12345, end.Tokens)
	require.Empty(t, end.Error)
}

func TestPromptWorkflow_RecordsOwnTokensNotTheChainTotal(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output", Tokens: 500}, nil)
	var states []RunState
	recordRunStates(env, &states)

	// A chained iteration inherits the accumulated total of every earlier
	// iteration; the record must carry only this iteration's own usage so summing
	// the rows of a chain gives a true total instead of counting earlier runs again.
	env.ExecuteWorkflow(PromptWorkflow,
		PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true, TokensSoFar: 9000})

	require.True(t, env.IsWorkflowCompleted())
	require.Len(t, states, 2)
	require.Equal(t, 500, states[1].Tokens)
	// Continuing as new is a control signal, not a failure: this iteration's work
	// landed, so it is recorded as succeeded and the next iteration becomes a row
	// of its own.
	require.Equal(t, execstore.StatusSucceeded, states[1].Status)
}

func TestPromptWorkflow_Failure_RecordsFailedState(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{}, errors.New("pi crashed"))
	var states []RunState
	recordRunStates(env, &states)
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Len(t, states, 2)
	require.Equal(t, execstore.StatusFailed, states[1].Status)
	require.Contains(t, states[1].Error, "pi crashed")
}

func TestPromptWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	// Recording is a hard dependency, not best-effort: a store that cannot be
	// written must fail the run rather than let it complete unrecorded.
	env.OnActivity(ra.PersistRunWorkflowState, mock.Anything, mock.Anything).
		Return(errors.New("postgres is down"))
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	// The agent never runs: the run is recorded as started first, so an
	// unrecordable run does no work at all.
	env.AssertNotCalled(t, "RunPiAgent", mock.Anything, mock.Anything)
}

func TestPromptWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{}, errors.New("pi crashed"))
	env.OnActivity(ra.PersistRunWorkflowState, mock.Anything, mock.Anything).Return(nil)
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
	env := newPromptEnv(t)

	// Keep the agent in flight so the workflow is still running when it is
	// cancelled; the in-flight activity then fails with a cancellation error.
	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		After(time.Hour).
		Return(piagent.Result{}, nil)
	var states []RunState
	recordRunStates(env, &states)
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
	require.Len(t, states, 2)
	require.Equal(t, execstore.StatusFailed, states[1].Status)
}

func TestPromptWorkflow_Chain_ContinuesAsNewWithoutNotifying(t *testing.T) {
	env := newPromptEnv(t)

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)
	env.OnActivity(ra.PersistRunWorkflowState, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Chaining loops via continue-as-new, so the run has not terminated and must
	// not notify yet.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertNotCalled(t, "Notify", mock.Anything, mock.Anything)
}
