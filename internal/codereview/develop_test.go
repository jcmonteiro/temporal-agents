package codereview

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/notification"
)

// The develop workflow tests exercise observable behavior — which activities
// run and whether the review loop is triggered — with every activity and the
// child review workflow mocked.

func newDevelopEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterWorkflow(DevelopWorkflow)
	env.RegisterWorkflow(ReviewWorkflow)
	return env
}

func TestDevelopWorkflow_HappyPath_DevelopsThenTriggersReview(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return("base", nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1", "sha2"}, nil)
	// The review loop is triggered as a child workflow; mock it so the child
	// starts and completes without running its own activities.
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "feat/x")
	require.Contains(t, out, "started review")
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_SeedsReviewWithDevelopTokenUsageAndReportsItInResult(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return("base", nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "done", Tokens: 4200}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	// The child review loop must be seeded with the develop session's usage so
	// its own result can report the whole tree's usage ("including parent
	// workflows").
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.MatchedBy(func(in ReviewInput) bool {
		return in.TokensSoFar == 4200
	})).Return("reviewed", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "Total token usage across all sessions: 4,200 tokens.")
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_Complete_NotifiesReviewWillCommence(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return("base", nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Finishing development notifies that it succeeded and review will commence.
	require.Equal(t, "Development complete", got.Title)
	require.Contains(t, got.Body, "review cycle")
}

func TestDevelopWorkflow_DirtyWorktree_FailsBeforeRunningAgent(t *testing.T) {
	env := newDevelopEnv(t)

	// CreateBranch refuses to proceed on a dirty working tree.
	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return("", temporal.NewNonRetryableApplicationError("dirty", errDirtyWorktree, nil))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Nothing should run once the branch could not be created.
	env.AssertNotCalled(t, activityName(a.RunDevelopAgent), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.EnsureDeveloped), mock.Anything, mock.Anything)
}

func TestDevelopWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newDevelopEnv(t)

	// Simulate CreateBranch failing; the specific cause is immaterial here — this
	// test only asserts that a failure produces a notification.
	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return("", temporal.NewNonRetryableApplicationError("dirty", errDirtyWorktree, nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed development notifies best-effort that it failed.
	require.Equal(t, "Development failed", got.Title)
}

func TestDevelopWorkflow_NoCommits_FailsWithoutTriggeringReview(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return("base", nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing"}, nil)
	// The agent produced no commits: the develop pass has nothing to review.
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}
