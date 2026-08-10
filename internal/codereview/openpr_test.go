package codereview

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/instruction/instructiontest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
)

// The open-PR workflow tests exercise observable behavior — which activities
// run and what the workflow notifies — with every activity mocked.

// OpenPRWorkflow records nothing of its own (its outcome is folded into the
// develop record), but the activity bundle still needs the port satisfied, so the
// environment gets a throwaway store.
func newOpenPREnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: execstoretest.New()})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	env.RegisterActivity(&instruction.Activity{Store: instructiontest.New()})
	env.RegisterWorkflow(OpenPRWorkflow)
	return env
}

func TestOpenPRWorkflow_HappyPath_OpensPRAndRequestsCopilot(t *testing.T) {
	env := newOpenPREnv(t)
	pr := PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}

	env.OnActivity(a.OpenPR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(OpenPRWorkflow, OpenPRInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out OpenPRResult
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out.Summary, "PR #7")
	require.Equal(t, pr.URL, out.URL)
	// The completion notification links to the PR and uses outcome-neutral wording,
	// since EnsureOpen may have returned an already-open PR unchanged.
	require.Equal(t, "Pull request ready", got.Title)
	require.Equal(t, pr.URL, got.URL)
	env.AssertExpectations(t)
}

func TestOpenPRWorkflow_CopilotRequestNotAttemptedWhenOpeningFails(t *testing.T) {
	env := newOpenPREnv(t)

	// Opening the PR fails, so no Copilot review can be requested.
	env.OnActivity(a.OpenPR, mock.Anything, mock.Anything).
		Return(PullRequest{}, temporal.NewNonRetryableApplicationError("push rejected", "OpenPR", nil))
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(OpenPRWorkflow, OpenPRInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// A failure notifies best-effort with the failure title.
	require.Equal(t, "Opening the pull request failed", got.Title)
	env.AssertNotCalled(t, activityName(a.RequestCopilotReview), mock.Anything, mock.Anything)
}
