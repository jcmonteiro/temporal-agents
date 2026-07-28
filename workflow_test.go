package main

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
)

func TestPromptWorkflow_Complete_SendsRunNotification(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).Return("the agent output", nil)
	var got notification.Notification
	var na *notification.Activity
	env.OnActivity(na.Notify, mock.Anything, mock.MatchedBy(func(n notification.Notification) bool {
		got = n
		return true
	})).Return(nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "summarize", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// A finished (non-chained) run notifies with the agent's output as the body.
	require.Equal(t, "Run complete", got.Title)
	require.Equal(t, "the agent output", got.Body)
}

func TestPromptWorkflow_Chain_ContinuesAsNewWithoutNotifying(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(RunPiAgent)
	env.RegisterActivity(&notification.Activity{})

	env.OnActivity(RunPiAgent, mock.Anything, mock.Anything).Return("output", nil)

	env.ExecuteWorkflow(PromptWorkflow, PromptRequest{Prompt: "watch", WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Chaining loops via continue-as-new, so the run has not terminated and must
	// not notify yet.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertNotCalled(t, "Notify", mock.Anything, mock.Anything)
}
