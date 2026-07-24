package codereview

import (
	"fmt"
	"time"

	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// chainDelay is how long a chained run waits before its child begins, giving
// reviewers (and any freshly requested Copilot review) time to post feedback.
const chainDelay = 3 * time.Minute

// PilotWorkflow drives the "code pilot" loop: it finds the open PR for the
// current branch, waits out any in-flight review, has the Pi agent address the
// PR's unresolved review comments, then replies to and resolves those comments
// with the resulting commit hashes and requests a fresh Copilot review.
//
// When in.Chain is set, a successful pass spawns a child run that starts after
// chainDelay, so the loop keeps folding in new feedback indefinitely.
func PilotWorkflow(ctx workflow.Context, in PilotInput) (string, error) {
	summary, err := runPilotOnce(ctx, in)
	if err != nil {
		return "", err
	}
	if in.Chain {
		if err := spawnDelayedChild(ctx, in); err != nil {
			return "", err
		}
	}
	return summary, nil
}

// runPilotOnce performs a single pass of the loop and returns a human-readable
// summary of what it did.
func runPilotOnce(ctx workflow.Context, in PilotInput) (string, error) {
	// Quick, deterministic git/GitHub steps. They are idempotent enough to
	// retry, but should not run forever.
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	// Waiting for an in-flight review can take a while; the activity heartbeats.
	waitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	// The agent step is long-running and streams heartbeats.
	agentCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	// Best-effort steps run once: retrying a failed stash pop (e.g. a merge
	// conflict) would only compound the mess.
	bestEffort := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	var a *Activities

	var pr PullRequest
	if err := workflow.ExecuteActivity(quick, a.DeterminePR, in).Get(quick, &pr); err != nil {
		return "", err
	}

	// Wait out any review still in progress so we act on a settled comment set.
	if err := workflow.ExecuteActivity(waitCtx, a.WaitOngoingReview, pr).Get(waitCtx, nil); err != nil {
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
	agentReq := RunAgentRequest{Input: in, PR: pr, Threads: loaded.Threads}
	if err := workflow.ExecuteActivity(agentCtx, a.RunAgent, agentReq).Get(agentCtx, &agentResult); err != nil {
		return "", err
	}

	var commits []string
	advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
	if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
		return "", err
	}

	// Publish the new commits to the PR branch before answering comments and
	// requesting a fresh review, so both see the pushed work.
	pushReq := PushBranchRequest{WorkDir: in.WorkDir, Branch: pr.HeadRef}
	if err := workflow.ExecuteActivity(quick, a.PushBranch, pushReq).Get(quick, nil); err != nil {
		return "", err
	}

	replyReq := ReplyAndResolveRequest{PR: pr, Threads: loaded.Threads, CommitSHAs: commits}
	if err := workflow.ExecuteActivity(quick, a.ReplyAndResolve, replyReq).Get(quick, nil); err != nil {
		return "", err
	}

	if err := workflow.ExecuteActivity(quick, a.RequestCopilotReview, pr).Get(quick, nil); err != nil {
		return "", err
	}

	// Put the developer's pre-existing local changes back. This is best-effort:
	// a conflict against the agent's new commits leaves the stash in place for
	// manual resolution rather than failing the (already successful) run.
	if cp.Stashed {
		restoreReq := RestoreStashRequest{WorkDir: in.WorkDir, Stashed: true}
		if err := workflow.ExecuteActivity(bestEffort, a.RestoreStash, restoreReq).Get(bestEffort, nil); err != nil {
			workflow.GetLogger(ctx).Warn("could not restore stashed changes; they remain in the git stash", "error", err)
		}
	}

	return fmt.Sprintf("Addressed %d comment(s) on PR #%d with %d commit(s); requested Copilot review.",
		len(loaded.Threads), pr.Number, len(commits)), nil
}

// spawnDelayedChild waits chainDelay, then starts a detached child PilotWorkflow
// with the same input. The child is abandoned (ParentClosePolicy) so it outlives
// this run and continues the chain on its own.
func spawnDelayedChild(ctx workflow.Context, in PilotInput) error {
	if err := workflow.Sleep(ctx, chainDelay); err != nil {
		return err
	}
	cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	child := workflow.ExecuteChildWorkflow(cctx, PilotWorkflow, in)
	// Ensure the child has actually started before this run completes; with an
	// abandon policy the parent otherwise finishes without guaranteeing the
	// child was scheduled.
	var childWE workflow.Execution
	return child.GetChildWorkflowExecution().Get(ctx, &childWE)
}
