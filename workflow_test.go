package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
	"temporal-agents/internal/piagent"
)

func TestPromptWorkflow_Complete_SendsRunNotification(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

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

func TestPromptWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

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
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

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
}

func TestPromptWorkflow_Chain_ContinuesAsNewWithoutNotifying(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).
		Return(piagent.Result{Output: "output"}, nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Chaining loops via continue-as-new, so the run has not terminated and must
	// not notify yet.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertNotCalled(t, "Notify", mock.Anything, mock.Anything)
}
