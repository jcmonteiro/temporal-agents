package codereview

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
)

// The workflow tests exercise observable behavior — which activities run and
// what the workflow returns — with every activity mocked. They intentionally
// say nothing about the git/GitHub adapters (covered elsewhere).

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	// Register a zero-value Activities so activity names resolve; the real
	// methods are never invoked because every call is mocked below.
	env.RegisterActivity(&Activities{})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterWorkflow(PilotWorkflow)
	return env
}

var a *Activities // used only to reference method names for OnActivity

var na *notification.Activity // used only to reference Notify for OnActivity

// activityName returns the Temporal-registered activity name for a *Activities
// method value. Negative assertions (AssertNotCalled) take a method-name
// string, and testify passes for any name it does not find — so a typo would
// silently defeat the assertion. Deriving the name from the method symbol makes
// a typo a compile error instead.
func activityName(method any) string {
	full := runtime.FuncForPC(reflect.ValueOf(method).Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	return full
}

func TestPilotWorkflow_NoUnresolvedComments_ExitsEarly(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "nothing to do")
	// The agent and mutating steps must not run when there is nothing to fix.
	env.AssertNotCalled(t, activityName(a.RunAgent), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.ReplyAndResolve), mock.Anything, mock.Anything)
}

func TestPilotWorkflow_WaitsForOngoingReview(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	// First probe: a review is still in flight; the workflow sleeps and checks
	// again, and the second probe reports it has settled.
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(true, nil).Once()
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil).Once()
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Both probes must have run: it did not act on the first, in-flight check.
	env.AssertExpectations(t)
}

func TestPilotWorkflow_Chain_ContinuesAsNewAfterAddressing(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Having addressed comments, chaining loops by continuing as new.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
}

func TestPilotWorkflow_Chain_StopsWhenNoUnresolvedComments(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Nothing to address ends the chain: it completes normally instead of
	// continuing as new.
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "nothing to do")
}

func TestPilotWorkflow_HappyPath_AddressesResolvesAndRequestsReview(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42, Owner: "acme", Repo: "widgets"}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}, {ID: "t2", Body: "also fix"}}
	commits := []string{"sha1", "sha2"}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return(commits, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	// The stash taken earlier is restored best-effort at the end.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "PR #42")
	env.AssertExpectations(t)
}

func TestPilotWorkflow_StashRestoreFailure_StillSucceeds(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	// The stash pop conflicts, but the run has already succeeded.
	env.OnActivity(a.RestoreStash, mock.Anything, mock.Anything).
		Return(errors.New("CONFLICT: merge conflict"))

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPilotWorkflow_NoNewCommits_FailsAndStopsBeforeReplying(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Nothing changed, so we must not push, answer, or resolve anything.
	env.AssertNotCalled(t, activityName(a.PushBranch), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.ReplyAndResolve), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.RequestCopilotReview), mock.Anything, mock.Anything)
}

func TestPilotWorkflow_Result_ReportsAccumulatedTokenUsage(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "done", Tokens: 500}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)

	// Seed prior-pass usage so the result reports the whole chain's total.
	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", TokensSoFar: 1000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	// 1000 carried in + 500 this pass.
	require.Contains(t, out, "Total token usage across all sessions: 1,500 tokens.")
}

func TestPilotWorkflow_Chain_CarriesTokenUsageForward(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "done", Tokens: 700}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, TokensSoFar: 300})

	require.True(t, env.IsWorkflowCompleted())
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// The continued run carries the running total (300 carried + 700 this pass).
	var next PilotInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(canErr.Input, &next))
	require.Equal(t, 1000, next.TokensSoFar)
}

func TestPilotWorkflow_ChainSummary_CarriesSummaryForwardOnAddressingPass(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("addressed-pass summary", nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// When chaining with --summary, an addressing pass summarizes its own (live) Pi
	// session and carries the text forward so the later no-comments pass, which
	// runs no agent under a fresh RunID, can still attach it.
	var next PilotInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(canErr.Input, &next))
	require.Equal(t, "addressed-pass summary", next.ChainSummary)
}

func TestPilotWorkflow_ChainSummary_TerminalPassUsesCarriedSummary(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	// No unresolved comments: this terminal pass runs no agent and ends the chain.
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{
		WorkDir: "/repo", Chain: true, Summary: true, ChainSummary: "carried summary",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The terminal no-comments pass ran no agent, so it must not summarize a
	// fresh, empty session; it attaches the summary preserved from the last
	// addressed pass to the completion webhook instead.
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Equal(t, "carried summary", got.WebhookBody)
	require.Equal(t, "Copilot review chain complete", got.Title)
}

func TestPilotWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newEnv(t)

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).
		Return(PullRequest{}, temporal.NewNonRetryableApplicationError("no open PR", "NoPR", nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failed pilot loop notifies best-effort with the error as the body.
	require.Equal(t, "Copilot review chain failed", got.Title)
	require.Contains(t, got.Body, "no open PR")
}

func TestPilotWorkflow_Chain_DoesNotSendFailureNotification(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// Continue-as-new is a control signal, not a failure: it must not notify.
	env.AssertNotCalled(t, activityName(na.Notify), mock.Anything, mock.Anything)
}

func TestPilotWorkflow_Summary_SetsWebhookBodyOnAddressingPassBeforeChaining(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("short summary for webhook", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	// The chained path (Chain: true) is what this test exercises: a pass that
	// addresses comments continues as new, so the summary must be delivered on the
	// addressing pass rather than at a terminal step that never summarizes.
	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	// The pass addressed comments, so the loop continues as new.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
	// Even though it continues as new, --summary makes the addressing pass emit a
	// webhook body summarizing the agent's work: it is the WebhookBody, while the
	// plain Body (used by other channels) keeps the pass text.
	require.Equal(t, "short summary for webhook", got.WebhookBody)
	require.Contains(t, got.Body, "PR #7")
}

func TestPilotWorkflow_Summary_CancellationDuringSummary_FailsInsteadOfCompleting(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	// A workflow cancellation while the terminal summary step is running surfaces
	// as a cancellation error on its Get. It must propagate as a workflow failure
	// rather than being swallowed into a successful completion.
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).
		Return("", temporal.NewCanceledError())
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.True(t, temporal.IsCanceledError(err), "expected cancellation to propagate, got %v", err)
}

func TestPilotWorkflow_Summary_SetsWebhookBodyOnFailureAfterAgentRan(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	// The push fails after the agent already ran, so a Pi session exists to
	// summarize for the failure webhook body.
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("push rejected", "PushError", nil))
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("failure summary", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failure after the agent ran summarizes that (real) session for the webhook.
	require.Equal(t, "Copilot review chain failed", got.Title)
	require.Equal(t, "failure summary", got.WebhookBody)
}

func TestPilotWorkflow_Summary_NoAgentRan_DoesNotSummarizeOnCompletion(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	// No unresolved comments: the pass exits before running the agent, so no Pi
	// session exists in this run.
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	// No comments means addressed is false, so even on the always-chained
	// production path this reaches the terminal completion (no continue-as-new)
	// with no agent having run—nothing to summarize.
	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// With no agent run there is nothing real to summarize: the step is skipped
	// and the webhook falls back to the plain Body rather than a fabricated one.
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Empty(t, got.WebhookBody)
}

func TestPilotWorkflow_Summary_NoAgentRan_DoesNotSummarizeOnFailure(t *testing.T) {
	env := newEnv(t)

	// The workflow fails before any agent step, so there is no Pi session.
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).
		Return(PullRequest{}, temporal.NewNonRetryableApplicationError("no open PR", "NoPR", nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true, Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// The failure still notifies, but summarizing a non-existent session is
	// skipped so the webhook body is not fabricated.
	require.Equal(t, "Copilot review chain failed", got.Title)
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Empty(t, got.WebhookBody)
}

func TestPilotWorkflow_NoSummaryFlag_DoesNotSummarize(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Without --summary the final summary step never runs and the webhook body
	// falls back to the plain Body.
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Empty(t, got.WebhookBody)
}

func TestPilotWorkflow_Complete_SendsCopilotChainNotification(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(notification.Notification)
		}).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Finishing the pilot loop notifies that the Copilot review chain is done.
	require.Equal(t, "Copilot review chain complete", got.Title)
	require.Contains(t, got.Body, "nothing to do")
	// The notification carries a hyperlink to the PR the loop operated on.
	require.Equal(t, pr.URL, got.URL)
}
