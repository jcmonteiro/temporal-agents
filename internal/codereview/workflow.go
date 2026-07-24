package codereview

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PilotWorkflow drives the "code pilot" loop: it finds the open PR for the
// current branch, has the Pi agent address the PR's unresolved review comments,
// then replies to and resolves those comments with the resulting commit hashes
// and requests a fresh Copilot review.
//
// The activities are executed in the fixed order described by the feature spec.
// When there are no unresolved comments the workflow returns early with a
// success message. When the agent produces no new commits the EnsureHeadAdvanced
// activity restores any stash and fails non-retryably.
func PilotWorkflow(ctx workflow.Context, in PilotInput) (string, error) {
	// Quick, deterministic git/GitHub steps. They are idempotent enough to
	// retry, but should not run forever.
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	// The agent step is long-running and streams heartbeats.
	agentCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	var a *Activities

	var pr PullRequest
	if err := workflow.ExecuteActivity(quick, a.DeterminePR, in).Get(quick, &pr); err != nil {
		return "", err
	}

	var loaded LoadCommentsResult
	if err := workflow.ExecuteActivity(quick, a.LoadUnresolvedComments, pr).Get(quick, &loaded); err != nil {
		return "", err
	}
	if len(loaded.Threads) == 0 {
		return fmt.Sprintf("No unresolved comments on PR #%d; nothing to do.", pr.Number), nil
	}

	var cp Checkpoint
	if err := workflow.ExecuteActivity(quick, a.MarkHeadAndStash, in).Get(quick, &cp); err != nil {
		return "", err
	}

	var agentResult string
	agentReq := RunAgentRequest{Input: in, Threads: loaded.Threads}
	if err := workflow.ExecuteActivity(agentCtx, a.RunAgent, agentReq).Get(agentCtx, &agentResult); err != nil {
		return "", err
	}

	var commits []string
	advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
	if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
		return "", err
	}

	replyReq := ReplyAndResolveRequest{PR: pr, Threads: loaded.Threads, CommitSHAs: commits}
	if err := workflow.ExecuteActivity(quick, a.ReplyAndResolve, replyReq).Get(quick, nil); err != nil {
		return "", err
	}

	if err := workflow.ExecuteActivity(quick, a.RequestCopilotReview, pr).Get(quick, nil); err != nil {
		return "", err
	}

	return fmt.Sprintf("Addressed %d comment(s) on PR #%d with %d commit(s); requested Copilot review.",
		len(loaded.Threads), pr.Number, len(commits)), nil
}
