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

func TestReviewWorkflow_NoPayload_JustReviewsAndStopsWhenClean(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("looks good", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(`{"review":[]}`, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: `{"review":[]}`, ItemCount: 0}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "no actionable items")
	// Without a payload it must not implement or touch the git HEAD.
	env.AssertNotCalled(t, "MarkHeadAndStash", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RunImplementAgent", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsureHeadAdvanced", mock.Anything, mock.Anything)
}

func TestReviewWorkflow_NoPayload_ActionableItems_ContinuesAsNewWithPayload(t *testing.T) {
	env := newReviewEnv(t)
	structured := `{"review":[{"itemName":"rename foo"}]}`

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("rename foo", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(structured, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: structured, ItemCount: 1}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	// Actionable items loop by continuing as new, carrying the structured payload.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// The first pass has no payload, so it must only review.
	env.AssertNotCalled(t, activityName(a.RunImplementAgent), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_ActionableItems_AtPassCap_StopsInsteadOfLooping(t *testing.T) {
	env := newReviewEnv(t)
	structured := `{"review":[{"itemName":"still something"}]}`

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("still something", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(structured, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: structured, ItemCount: 1}, nil)

	// The next pass would be MaxReviewPasses, so the loop must stop rather than
	// continue as new, even though actionable items remain.
	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "stopped after")
}

func TestReviewWorkflow_WithPayload_ImplementsCheckingHeadThenReviews(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("looks good now", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(`{"review":[]}`, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: `{"review":[]}`, ItemCount: 0}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: `{"review":[{"itemName":"fix"}]}`})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The implement path ran, bracketed by the HEAD-before/after checks, and the
	// clean re-review ended the chain.
	env.AssertExpectations(t)
}

func TestReviewWorkflow_WithPayload_RestoresStashedChanges(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("looks good now", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(`{"review":[]}`, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: `{"review":[]}`, ItemCount: 0}, nil)
	// The changes stashed before the implement agent ran are restored at the end.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: `{"review":[{"itemName":"fix"}]}`})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReviewWorkflow_StashRestoreFailure_StillSucceeds(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("looks good now", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return(`{"review":[]}`, nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{Payload: `{"review":[]}`, ItemCount: 0}, nil)
	// The stash pop conflicts, but the pass has already succeeded.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).
		Return(errors.New("CONFLICT: merge conflict"))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: `{"review":[{"itemName":"fix"}]}`})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestReviewWorkflow_WithPayload_NoNewCommits_Fails(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return("nothing", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: `{"review":[{"itemName":"fix"}]}`})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed implement must stop before reviewing again.
	env.AssertNotCalled(t, "RunReviewAgent", mock.Anything, mock.Anything)
}

func TestReviewWorkflow_InvalidStructuredJSON_Fails(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return("some review", nil)
	env.OnActivity(a.StructureReview, mock.Anything, mock.Anything).Return("not json", nil)
	env.OnActivity(a.ValidateReviewJSON, mock.Anything, mock.Anything).
		Return(ValidateReviewResult{}, temporal.NewNonRetryableApplicationError(
			"invalid review JSON", errInvalidReviewJSON, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}
