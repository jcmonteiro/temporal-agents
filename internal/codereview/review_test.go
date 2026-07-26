package codereview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// The review workflow tests exercise observable behavior — which activities run
// and whether the workflow continues as new — with every activity mocked.

func newReviewEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	env.RegisterWorkflow(ReviewWorkflow)
	return env
}

func TestReviewWorkflow_NoPayload_ReviewsThenContinuesAsNewWithReview(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("rename foo", nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	// With no payload the first pass only reviews, then loops by continuing as
	// new to hand the review output to an implement pass.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// It must not implement or touch the git HEAD on the review-only pass.
	env.AssertNotCalled(t, activityName(a.MarkHeadAndStash), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.RunImplementAgent), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.EnsureHeadAdvanced), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_AtPassCap_StopsInsteadOfLooping(t *testing.T) {
	env := newReviewEnv(t)

	// Reached only via the implement path, so those steps run once before the
	// re-review; the next pass would be MaxReviewPasses, so it must stop.
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("more feedback", nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "stopped after")
}

func TestReviewWorkflow_WithPayload_ImplementsCheckingHeadThenReviewsAndLoops(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("new feedback", nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	// A pass that committed loops by continuing as new with the fresh review.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertExpectations(t)
}

func TestReviewWorkflow_WithPayload_NoNewCommits_SucceedsAndStops(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("nothing to change", nil)
	// The implement pass produced no commits: the branch has converged.
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "nothing to commit")
	// Converged: it must not review again or loop.
	env.AssertNotCalled(t, activityName(a.RunReviewAgent), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_WithPayload_RestoresStashedChanges(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("new feedback", nil)
	// The changes stashed before the implement agent ran are restored at the end.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	env.AssertExpectations(t)
}

func TestReviewWorkflow_StashRestoreFailure_StillLoops(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("new feedback", nil)
	// The stash pop conflicts, but the pass has already done its work.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).
		Return(errors.New("CONFLICT: merge conflict"))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
}

func TestReviewWorkflow_WithPayload_ImplementAgentError_Fails(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Return("", errors.New("pi failed"))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed implement must stop before reviewing again.
	env.AssertNotCalled(t, activityName(a.RunReviewAgent), mock.Anything, mock.Anything)
}
