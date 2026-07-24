package codereview

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// reviewPollInterval is how long the workflow sleeps between checks for a
// still-pending Copilot review.
const reviewPollInterval = time.Minute

// PilotWorkflow drives the "code pilot" loop: it finds the open PR for the
// current branch, waits out any in-flight review, has the Pi agent address the
// PR's unresolved review comments, then replies to and resolves those comments
// with the resulting commit hashes and requests a fresh Copilot review.
//
// When in.Chain is set, a pass that addressed comments continues as new, so the
// loop keeps folding in new feedback; the next pass's CheckOngoingReview wait
// absorbs the delay while the freshly requested Copilot review runs. A pass that
// found no unresolved comments ends the chain instead of looping forever. This
// mirrors PromptWorkflow: continue-as-new restarts the run with a fresh,
// bounded event history under the same workflow ID.
func PilotWorkflow(ctx workflow.Context, in PilotInput) (string, error) {
	summary, addressed, err := runPilotOnce(ctx, in)
	if err != nil {
		return "", err
	}
	if in.Chain && addressed {
		return "", workflow.NewContinueAsNewError(ctx, PilotWorkflow, in)
	}
	return summary, nil
}

// runPilotOnce performs a single pass of the loop. It returns a human-readable
// summary and whether it actually addressed any comments (false when the PR had
// no unresolved comments), which the caller uses to decide whether to chain.
func runPilotOnce(ctx workflow.Context, in PilotInput) (string, bool, error) {
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
	// Best-effort steps run once: retrying a failed stash pop (e.g. a merge
	// conflict) would only compound the mess.
	bestEffort := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	var a *Activities

	var pr PullRequest
	if err := workflow.ExecuteActivity(quick, a.DeterminePR, in).Get(quick, &pr); err != nil {
		return "", false, err
	}

	// Wait out any review still in progress so we act on a settled comment set.
	// Polling lives in the workflow: a quick check activity plus a durable timer
	// between attempts, rather than a long-running heartbeating activity.
	for {
		var ongoing bool
		if err := workflow.ExecuteActivity(quick, a.CheckOngoingReview, pr).Get(quick, &ongoing); err != nil {
			return "", false, err
		}
		if !ongoing {
			break
		}
		if err := workflow.Sleep(ctx, reviewPollInterval); err != nil {
			return "", false, err
		}
	}

	var loaded LoadCommentsResult
	if err := workflow.ExecuteActivity(quick, a.LoadUnresolvedComments, pr).Get(quick, &loaded); err != nil {
		return "", false, err
	}
	if len(loaded.Threads) == 0 {
		return fmt.Sprintf("No unresolved comments on PR #%d; nothing to do.", pr.Number), false, nil
	}

	var cp Checkpoint
	if err := workflow.ExecuteActivity(quick, a.MarkHeadAndStash, in).Get(quick, &cp); err != nil {
		return "", false, err
	}

	var agentResult string
	agentReq := RunAgentRequest{Input: in, PR: pr, Threads: loaded.Threads}
	if err := workflow.ExecuteActivity(agentCtx, a.RunAgent, agentReq).Get(agentCtx, &agentResult); err != nil {
		return "", false, err
	}

	var commits []string
	advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
	if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
		return "", false, err
	}

	// Publish the new commits to the PR branch before answering comments and
	// requesting a fresh review, so both see the pushed work.
	pushReq := PushBranchRequest{WorkDir: in.WorkDir, Branch: pr.HeadRef}
	if err := workflow.ExecuteActivity(quick, a.PushBranch, pushReq).Get(quick, nil); err != nil {
		return "", false, err
	}

	replyReq := ReplyAndResolveRequest{PR: pr, Threads: loaded.Threads, CommitSHAs: commits}
	if err := workflow.ExecuteActivity(quick, a.ReplyAndResolve, replyReq).Get(quick, nil); err != nil {
		return "", false, err
	}

	if err := workflow.ExecuteActivity(quick, a.RequestCopilotReview, pr).Get(quick, nil); err != nil {
		return "", false, err
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
		len(loaded.Threads), pr.Number, len(commits)), true, nil
}
