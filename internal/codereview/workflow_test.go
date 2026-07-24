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

// The workflow tests exercise observable behavior — which activities run and
// what the workflow returns — with every activity mocked. They intentionally
// say nothing about the git/GitHub adapters (covered elsewhere).

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	// Register a zero-value Activities so activity names resolve; the real
	// methods are never invoked because every call is mocked below.
	env.RegisterActivity(&Activities{})
	env.RegisterWorkflow(PilotWorkflow)
	return env
}

var a *Activities // used only to reference method names for OnActivity

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
	env.AssertNotCalled(t, "RunAgent", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "ReplyAndResolve", mock.Anything, mock.Anything)
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

func TestPilotWorkflow_Chain_ContinuesAsNew(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: nil}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	// Chaining loops by continuing as new rather than returning a result.
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &canErr)
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
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return("done", nil)
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
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return("done", nil)
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
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return("nothing", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Nothing changed, so we must not push, answer, or resolve anything.
	env.AssertNotCalled(t, "PushBranch", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "ReplyAndResolve", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RequestCopilotReview", mock.Anything, mock.Anything)
}
