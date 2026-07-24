package codereview

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
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
	return env
}

var a *Activities // used only to reference method names for OnActivity

func TestPilotWorkflow_NoUnresolvedComments_ExitsEarly(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 7}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
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

func TestPilotWorkflow_HappyPath_AddressesResolvesAndRequestsReview(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42, Owner: "acme", Repo: "widgets"}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}, {ID: "t2", Body: "also fix"}}
	commits := []string{"sha1", "sha2"}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: threads}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).
		Return(Checkpoint{HeadSHA: "base", Stashed: true}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return("done", nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return(commits, nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "PR #42")
	env.AssertExpectations(t)
}

func TestPilotWorkflow_NoNewCommits_FailsAndStopsBeforeReplying(t *testing.T) {
	env := newEnv(t)
	pr := PullRequest{Number: 42}
	threads := []ReviewThread{{ID: "t1", Body: "fix"}}

	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
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
	// The comments must not be answered or resolved when nothing changed.
	env.AssertNotCalled(t, "ReplyAndResolve", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RequestCopilotReview", mock.Anything, mock.Anything)
}
