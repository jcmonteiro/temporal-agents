package codereview

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
)

// reviewPollInterval is how long the workflow sleeps between checks for a
// still-pending Copilot review.
const reviewPollInterval = time.Minute

// notifyChainComplete sends a best-effort completion notification at a chain's
// terminal step. Failures are logged and swallowed so a notification problem
// never fails an otherwise successful workflow. It must be called before the
// workflow returns (never on a continue-as-new path), since continue-as-new
// cancels any in-flight activity. url, when non-empty, is a hyperlink to the
// relevant resource (e.g. the pull request) that adapters surface alongside the
// message.
func notifyChainComplete(ctx workflow.Context, title, body, url string) {
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	var na *notification.Activity
	if err := workflow.ExecuteActivity(opts, na.Notify, notification.Notification{Title: title, Body: body, URL: url}).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn("could not send completion notification", "error", err)
	}
}

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
	summary, addressed, tokens, prURL, err := runPilotOnce(ctx, in)
	if err != nil {
		return "", err
	}
	// Fold this pass's usage into the running total carried across chained runs.
	total := in.TokensSoFar + tokens
	if in.Chain && addressed {
		next := in
		next.TokensSoFar = total
		return "", workflow.NewContinueAsNewError(ctx, PilotWorkflow, next)
	}
	summary = withTokenTotal(summary, total)
	notifyChainComplete(ctx, "Copilot review chain complete", summary, prURL)
	return summary, nil
}

// runPilotOnce performs a single pass of the loop. It returns a human-readable
// summary, whether it actually addressed any comments (false when the PR had no
// unresolved comments, which the caller uses to decide whether to chain), the
// pass's total agent token usage, and a hyperlink to the PR the pass operated
// on (empty when it failed before determining the PR) so the caller can include
// it in the completion notification.
func runPilotOnce(ctx workflow.Context, in PilotInput) (string, bool, int, string, error) {
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
		return "", false, 0, "", err
	}

	// Wait out any review still in progress so we act on a settled comment set.
	// Polling lives in the workflow: a quick check activity plus a durable timer
	// between attempts, rather than a long-running heartbeating activity.
	for {
		var ongoing bool
		if err := workflow.ExecuteActivity(quick, a.CheckOngoingReview, pr).Get(quick, &ongoing); err != nil {
			return "", false, 0, "", err
		}
		if !ongoing {
			break
		}
		if err := workflow.Sleep(ctx, reviewPollInterval); err != nil {
			return "", false, 0, "", err
		}
	}

	var loaded LoadCommentsResult
	if err := workflow.ExecuteActivity(quick, a.LoadUnresolvedComments, pr).Get(quick, &loaded); err != nil {
		return "", false, 0, "", err
	}
	if len(loaded.Threads) == 0 {
		return fmt.Sprintf("No unresolved comments on PR #%d; nothing to do.", pr.Number), false, 0, pr.URL, nil
	}

	var cp Checkpoint
	if err := workflow.ExecuteActivity(quick, a.MarkHeadAndStash, in).Get(quick, &cp); err != nil {
		return "", false, 0, "", err
	}

	var agentResult AgentResult
	agentReq := RunAgentRequest{Input: in, PR: pr, Threads: loaded.Threads}
	if err := workflow.ExecuteActivity(agentCtx, a.RunAgent, agentReq).Get(agentCtx, &agentResult); err != nil {
		return "", false, 0, "", err
	}

	var commits []string
	advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
	if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
		return "", false, 0, "", err
	}

	// Publish the new commits to the PR branch before answering comments and
	// requesting a fresh review, so both see the pushed work.
	pushReq := PushBranchRequest{WorkDir: in.WorkDir, Branch: pr.HeadRef}
	if err := workflow.ExecuteActivity(quick, a.PushBranch, pushReq).Get(quick, nil); err != nil {
		return "", false, 0, "", err
	}

	replyReq := ReplyAndResolveRequest{PR: pr, Threads: loaded.Threads, CommitSHAs: commits}
	if err := workflow.ExecuteActivity(quick, a.ReplyAndResolve, replyReq).Get(quick, nil); err != nil {
		return "", false, 0, "", err
	}

	if err := workflow.ExecuteActivity(quick, a.RequestCopilotReview, pr).Get(quick, nil); err != nil {
		return "", false, 0, "", err
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
		len(loaded.Threads), pr.Number, len(commits)), true, agentResult.Tokens, pr.URL, nil
}

// DevelopWorkflow drives the "code develop" flow. In sequence it:
//
//   - Creates the requested branch off a clean working tree (CreateBranch fails
//     when there are uncommitted local changes) and records the starting HEAD.
//   - Has the Pi agent implement the caller's prompt on that branch.
//   - Confirms the agent advanced HEAD and left a clean working tree
//     (EnsureDeveloped fails when nothing was committed or changes remain).
//   - Triggers the local review loop (ReviewWorkflow) on the new branch as an
//     abandoned child, so it keeps running—and notifies on completion—after
//     this workflow returns, exactly like a standalone "code review" run.
//
// It returns once the review has been started, reporting the review workflow's
// ID so the caller can watch it.
func DevelopWorkflow(ctx workflow.Context, in DevelopInput) (string, error) {
	// Quick, deterministic git/validation steps. Idempotent enough to retry, but
	// should not run forever.
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

	var base string
	if err := workflow.ExecuteActivity(quick, a.CreateBranch,
		CreateBranchRequest{WorkDir: in.WorkDir, Branch: in.Branch}).Get(quick, &base); err != nil {
		return "", err
	}

	var agentResult AgentResult
	if err := workflow.ExecuteActivity(agentCtx, a.RunDevelopAgent,
		RunDevelopRequest{WorkDir: in.WorkDir, Prompt: in.Prompt}).Get(agentCtx, &agentResult); err != nil {
		return "", err
	}

	var commits []string
	if err := workflow.ExecuteActivity(quick, a.EnsureDeveloped,
		EnsureDevelopedRequest{WorkDir: in.WorkDir, BaseSHA: base}).Get(quick, &commits); err != nil {
		return "", err
	}

	// Trigger the review loop as an abandoned child so it outlives this workflow.
	reviewID := "review-" + workflow.GetInfo(ctx).WorkflowExecution.ID
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        reviewID,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	// Seed the review loop with this develop session's token usage so its
	// terminal result reports the whole tree's usage ("including parent
	// workflows").
	child := workflow.ExecuteChildWorkflow(childCtx, ReviewWorkflow,
		ReviewInput{WorkDir: in.WorkDir, TokensSoFar: agentResult.Tokens})
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return "", fmt.Errorf("start review workflow: %w", err)
	}

	summary := withTokenTotal(fmt.Sprintf("Developed branch %s with %d commit(s); started review %s.",
		in.Branch, len(commits), reviewID), agentResult.Tokens)
	notifyChainComplete(ctx, "Development complete",
		fmt.Sprintf("Developed branch %s with %d commit(s) successfully. The review cycle will now commence.",
			in.Branch, len(commits)), "")
	return summary, nil
}

// ReviewWorkflow drives the "code review" loop entirely on the host machine.
// Each pass:
//
//   - When it carries a payload (the previous pass's raw review output), it
//     first implements that feedback with the Pi agent, checking HEAD before
//     (MarkHeadAndStash) and after (EnsureHeadAdvanced) to confirm the change
//     landed—reusing the same activities as the Copilot pilot flow. If the
//     implement pass makes no commits, the agent found nothing left to change,
//     so the loop ends successfully. Any changes stashed before the agent ran
//     are restored best-effort at the end of the pass. With no payload it skips
//     this and just reviews.
//   - It then runs the Pi agent to review the current branch. Because the
//     review activity blocks until it completes, no waiting/polling is needed.
//   - It continues as new with that raw review output as the next pass's
//     payload, so the next pass implements it and reviews again.
//
// The loop is also bounded: it stops after MaxReviewPasses passes even when the
// implement pass keeps making commits, so it cannot run forever.
func ReviewWorkflow(ctx workflow.Context, in ReviewInput) (string, error) {
	// Quick, deterministic git/validation steps. Idempotent enough to retry, but
	// should not run forever.
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	// The agent steps are long-running and stream heartbeats.
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

	// Accumulate token usage across this pass's agent sessions, seeded with the
	// usage carried in from prior passes (and, when spawned by DevelopWorkflow,
	// its parent's develop session).
	total := in.TokensSoFar

	// With a payload: implement the previous pass's review feedback, verifying
	// HEAD advanced. No new commits means the agent found nothing left to change,
	// so the branch has converged and the loop ends successfully.
	var cp Checkpoint
	if strings.TrimSpace(in.Payload) != "" {
		if err := workflow.ExecuteActivity(quick, a.MarkHeadAndStash, PilotInput{WorkDir: in.WorkDir}).Get(quick, &cp); err != nil {
			return "", err
		}

		var implResult AgentResult
		implReq := RunImplementRequest{WorkDir: in.WorkDir, Payload: in.Payload}
		if err := workflow.ExecuteActivity(agentCtx, a.RunImplementAgent, implReq).Get(agentCtx, &implResult); err != nil {
			return "", err
		}
		total += implResult.Tokens

		var commits []string
		advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
		if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
			// A NoCommits result is the success exit: the implement pass had nothing
			// to change. EnsureHeadAdvanced already restored any stash on this path,
			// so return without further cleanup.
			var appErr *temporal.ApplicationError
			if errors.As(err, &appErr) && appErr.Type() == errNoAdvance {
				summary := withTokenTotal("Review complete; the implement pass found nothing to commit.", total)
				notifyChainComplete(ctx, "Local review chain complete", summary, "")
				return summary, nil
			}
			return "", err
		}
	}

	// Review the current branch. This blocks until the review completes.
	var reviewResult AgentResult
	if err := workflow.ExecuteActivity(agentCtx, a.RunReviewAgent, ReviewInput{WorkDir: in.WorkDir}).Get(agentCtx, &reviewResult); err != nil {
		return "", err
	}
	total += reviewResult.Tokens
	reviewOutput := reviewResult.Output

	// Put the developer's pre-existing local changes back before ending the
	// pass. This is best-effort: a conflict against the implement commits leaves
	// the stash in place for manual resolution rather than failing the run.
	if cp.Stashed {
		restoreReq := RestoreStashRequest{WorkDir: in.WorkDir, Stashed: true}
		if err := workflow.ExecuteActivity(bestEffort, a.RestoreStash, restoreReq).Get(bestEffort, nil); err != nil {
			workflow.GetLogger(ctx).Warn("could not restore stashed changes; they remain in the git stash", "error", err)
		}
	}

	// Hand the raw review output to the next pass to implement, bounded by the
	// pass cap so the loop cannot run forever.
	nextPass := in.Pass + 1
	if nextPass >= MaxReviewPasses {
		summary := withTokenTotal(fmt.Sprintf("Review stopped after %d pass(es).", MaxReviewPasses), total)
		notifyChainComplete(ctx, "Local review chain complete", summary, "")
		return summary, nil
	}
	return "", workflow.NewContinueAsNewError(ctx, ReviewWorkflow,
		ReviewInput{WorkDir: in.WorkDir, Payload: reviewOutput, Pass: nextPass, TokensSoFar: total})
}
