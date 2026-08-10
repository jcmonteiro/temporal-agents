package codereview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/instruction/instructiontest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
)

// The review workflow tests exercise observable behavior — which activities run
// and whether the workflow continues as new — with every activity mocked.

// newReviewEnv builds the review test environment with a throwaway store, for the
// tests that are not about the durable execution record.
func newReviewEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	return newReviewEnvWithStore(t, execstoretest.New())
}

// newReviewEnvWithStore builds it around the given store, so a recording test can
// assert on what was written (see recording_test.go). Every workflow records
// itself, so the store is a required dependency rather than an option.
func newReviewEnvWithStore(t *testing.T, store *execstoretest.Store) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	env.RegisterActivity(&instruction.Activity{Store: instructiontest.New()})
	env.RegisterWorkflow(ReviewWorkflow)
	return env
}

func TestReviewWorkflow_NoPayload_ReviewsThenContinuesAsNewWithReview(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "rename foo"}, nil)

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
	// Continue-as-new is a control signal, not a failure or completion: it must
	// not notify.
	env.AssertNotCalled(t, activityName(na.Notify), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_AtPassCap_StopsInsteadOfLooping(t *testing.T) {
	env := newReviewEnv(t)

	// Reached only via the implement path, so those steps run once before the
	// re-review; the next pass would be MaxReviewPasses, so it must stop.
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more feedback"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out ReviewOutcome
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out.Summary, "stopped after")
	// Stopping at the pass cap is not convergence: the outcome must say so
	// explicitly so a fleet-gating parent does not read it as a clean success.
	require.False(t, out.Converged)
}

func TestReviewWorkflow_Result_ReportsAccumulatedTokenUsageAcrossSessions(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "done", Tokens: 200}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "more feedback", Tokens: 150}, nil)

	// At the pass cap the loop stops and reports the total: prior passes/parent
	// (1000) + this pass's implement (200) + review (150).
	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review", Pass: MaxReviewPasses - 1, TokensSoFar: 1000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out ReviewOutcome
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out.Summary, "Total token usage across all sessions: 1,350 tokens.")
}

func TestReviewWorkflow_WithPayload_ImplementsCheckingHeadThenReviewsAndLoops(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "new feedback"}, nil)

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
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing to change"}, nil)
	// The implement pass produced no commits: the branch has converged.
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out ReviewOutcome
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out.Summary, "nothing to commit")
	// The implement pass found nothing to change: the outcome is an explicit
	// convergence so a fleet-gating parent can start dependents.
	require.True(t, out.Converged)
	// Converged: it must not review again or loop.
	env.AssertNotCalled(t, activityName(a.RunReviewAgent), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_WithPayload_RestoresStashedChanges(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "new feedback"}, nil)
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
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "new feedback"}, nil)
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
		Return(AgentResult{}, errors.New("pi failed"))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed implement must stop before reviewing again.
	env.AssertNotCalled(t, activityName(a.RunReviewAgent), mock.Anything, mock.Anything)
}

func TestReviewWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).
		Return(AgentResult{}, temporal.NewNonRetryableApplicationError("review agent crashed", "AgentError", nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed review pass notifies best-effort with the error as the body.
	require.Equal(t, "Local review chain failed", got.Title)
	require.Contains(t, got.Body, "review agent crashed")
}

func TestReviewWorkflow_Summary_SetsWebhookBodyFromLastRunSummary(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing to change"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("review summary for webhook", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review", Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The last run's summary is delivered to the webhook only.
	require.Equal(t, "review summary for webhook", got.WebhookBody)
	require.Contains(t, got.Body, "nothing to commit")
}

func TestReviewWorkflow_NoSummaryFlag_DoesNotSummarize(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing to change"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Without --summary the final summary step never runs even though an agent
	// did, and the webhook body falls back to the plain Body.
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Empty(t, got.WebhookBody)
}

func TestReviewWorkflow_Complete_SendsLocalChainNotification(t *testing.T) {
	env := newReviewEnv(t)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing to change"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "prior review"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Converging the local review loop notifies that its chain is done.
	require.Equal(t, "Local review chain complete", got.Title)
}
