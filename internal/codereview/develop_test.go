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
	env.RegisterWorkflow(OpenPRWorkflow)
	env.RegisterWorkflow(PilotWorkflow)
	return env
}

func TestDevelopWorkflow_HappyPath_DevelopsThenTriggersReview(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
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

func TestDevelopWorkflow_Worktree_PassesWorktreesDirAndDevelopsInReturnedWorktree(t *testing.T) {
	env := newDevelopEnv(t)

	// In worktree mode the CLI passes a worktrees base directory; CreateBranch
	// creates a worktree under it and reports that path as the working directory.
	const worktree = "/cfg/worktrees/flaming-duck-2026-jul-29"
	env.OnActivity(a.CreateBranch, mock.Anything, mock.MatchedBy(func(req CreateBranchRequest) bool {
		return req.WorktreesDir == "/cfg/worktrees"
	})).Return(CreateBranchResult{Branch: "flaming-duck-2026-jul-29", WorkDir: worktree, BaseSHA: "base"}, nil)
	// Every downstream step must run in the worktree, not the original WorkDir.
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.MatchedBy(func(req RunDevelopRequest) bool {
		return req.WorkDir == worktree
	})).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.MatchedBy(func(req EnsureDevelopedRequest) bool {
		return req.WorkDir == worktree
	})).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.MatchedBy(func(in ReviewInput) bool {
		return in.WorkDir == worktree
	})).Return("reviewed", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", WorktreesDir: "/cfg/worktrees", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_GeneratedBranch_ReportsResolvedBranchName(t *testing.T) {
	env := newDevelopEnv(t)

	// With no explicit branch the workflow passes an empty branch to CreateBranch,
	// which resolves it to a generated alias and reports that name back.
	env.OnActivity(a.CreateBranch, mock.Anything, mock.MatchedBy(func(req CreateBranchRequest) bool {
		return req.Branch == ""
	})).Return(CreateBranchResult{Branch: "flaming-duck-2026-jul-29", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	// The generated branch name (not the empty input) is reported.
	require.Contains(t, out, "flaming-duck-2026-jul-29")
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_SeedsReviewWithDevelopTokenUsageAndReportsItInResult(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
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

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
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

func TestDevelopWorkflow_Summary_SetsWebhookBodyAndPropagatesToReview(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	// --summary is propagated to the review loop this workflow spawns.
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.MatchedBy(func(in ReviewInput) bool {
		return in.Summary
	})).Return("reviewed", nil)
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("develop summary for webhook", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing", Summary: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// The last run's summary is delivered to the webhook only.
	require.Equal(t, "develop summary for webhook", got.WebhookBody)
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_NoSummaryFlag_DoesNotSummarize(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Without --summary the final summary step never runs even though the develop
	// agent did, and the webhook body falls back to the plain Body.
	env.AssertNotCalled(t, activityName(a.SummarizeLastRun), mock.Anything, mock.Anything)
	require.Empty(t, got.WebhookBody)
}

func TestDevelopWorkflow_DirtyWorktree_FailsBeforeRunningAgent(t *testing.T) {
	env := newDevelopEnv(t)

	// CreateBranch refuses to proceed on a dirty working tree.
	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{}, temporal.NewNonRetryableApplicationError("dirty", errDirtyWorktree, nil))

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
		Return(CreateBranchResult{}, temporal.NewNonRetryableApplicationError("dirty", errDirtyWorktree, nil))
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

func TestDevelopWorkflow_WithRemote_OrchestratesReviewOpenPRAndPilot(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	// The full remote pipeline runs as supervised children this workflow waits on.
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).Return("opened", nil)
	// The pilot loop is triggered with chaining enabled so it loops until Copilot
	// has nothing left; here it is mocked to return once.
	env.OnWorkflow(PilotWorkflow, mock.Anything, mock.MatchedBy(func(in PilotInput) bool {
		return in.Chain
	})).Return("piloted", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing", WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	// The result reflects the whole pipeline having completed, not just review.
	require.Contains(t, out, "opened the PR")
	require.Contains(t, out, "pilot")
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_WithRemote_SeedsReviewTokensAndPropagatesSummary(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "done", Tokens: 4200}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnActivity(a.SummarizeLastRun, mock.Anything, mock.Anything).Return("develop summary for webhook", nil)
	// The review loop is still seeded with the develop session's usage and gets
	// --summary propagated, exactly like the non-remote path.
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.MatchedBy(func(in ReviewInput) bool {
		return in.TokensSoFar == 4200 && in.Summary
	})).Return("reviewed", nil)
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).Return("opened", nil)
	env.OnWorkflow(PilotWorkflow, mock.Anything, mock.Anything).Return("piloted", nil)
	var sent []notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { sent = append(sent, args.Get(1).(notification.Notification)) }).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing", Summary: true, WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	// The supervised terminal summary reports only the develop step's own usage;
	// the review and pilot children report their totals separately, so it does not
	// claim an "across all sessions" figure it cannot compute here.
	require.Contains(t, out, "Develop step token usage: 4,200 tokens.")
	require.NotContains(t, out, "across all sessions")
	// The develop session's summary is delivered on the up-front develop-completion
	// notification, matched to the step it describes. The terminal pipeline
	// notification carries no (stale) summary body.
	require.Len(t, sent, 2)
	require.Equal(t, "Development complete", sent[0].Title)
	require.Equal(t, "develop summary for webhook", sent[0].WebhookBody)
	require.Equal(t, "Remote pipeline complete", sent[1].Title)
	require.Empty(t, sent[1].WebhookBody)
	env.AssertExpectations(t)
}

func TestDevelopWorkflow_WithRemote_OpenPRFailure_StopsBeforePilot(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)
	// Opening the PR fails, so the pilot loop must never start.
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).
		Return("", temporal.NewNonRetryableApplicationError("no commits to open a PR", "OpenPR", nil))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing", WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "PilotWorkflow", mock.Anything, mock.Anything)
}

func TestDevelopWorkflow_WithRemote_PipelineFailure_NotifiesPipelineNotDevelopment(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Return("reviewed", nil)
	// Opening the PR fails after development has already landed its commits.
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).
		Return("", temporal.NewNonRetryableApplicationError("push rejected", "OpenPR", nil))
	var sent []notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { sent = append(sent, args.Get(1).(notification.Notification)) }).Return(nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing", WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Once development succeeds and ownership passes to the remote pipeline, a stage
	// failure is reported as a pipeline failure, not a second, misleading
	// "Development failed".
	var titles []string
	for _, n := range sent {
		titles = append(titles, n.Title)
	}
	require.Contains(t, titles, "Remote pipeline failed")
	require.NotContains(t, titles, "Development failed")
}

func TestDevelopWorkflow_NoCommits_FailsWithoutTriggeringReview(t *testing.T) {
	env := newDevelopEnv(t)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "nothing"}, nil)
	// The agent produced no commits: the develop pass has nothing to review.
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}
