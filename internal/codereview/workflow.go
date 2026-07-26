package codereview

import (
	"fmt"
	"strings"
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

// ReviewWorkflow drives the "code review" loop entirely on the host machine.
// Each pass:
//
//   - When it carries a structured review payload, it first implements those
//     actions with the Pi agent, checking HEAD before (MarkHeadAndStash) and
//     after (EnsureHeadAdvanced) to confirm the change landed—reusing the same
//     activities as the Copilot pilot flow. Any changes stashed before the
//     agent ran are restored best-effort at the end of the pass. With no
//     payload it skips this and just reviews.
//   - It then runs the Pi agent to review the current branch. Because the
//     review activity blocks until it completes, no waiting/polling is needed.
//   - It structures the review's last output into JSON (hardening the flow) and
//     deterministically validates that JSON against the expected schema.
//   - If the validated payload has actionable items, it continues as new with
//     that payload, so the next pass implements them and reviews again. If it
//     has none, the chain ends successfully.
//
// The loop is bounded: it stops after MaxReviewPasses passes even when the
// review agent keeps surfacing items, so it cannot run (and commit) forever.
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

	// With a payload: implement the review actions, verifying HEAD advanced.
	var cp Checkpoint
	if strings.TrimSpace(in.Payload) != "" {
		if err := workflow.ExecuteActivity(quick, a.MarkHeadAndStash, PilotInput{WorkDir: in.WorkDir}).Get(quick, &cp); err != nil {
			return "", err
		}

		implReq := RunImplementRequest{WorkDir: in.WorkDir, Payload: in.Payload}
		if err := workflow.ExecuteActivity(agentCtx, a.RunImplementAgent, implReq).Get(agentCtx, nil); err != nil {
			return "", err
		}

		var commits []string
		advReq := EnsureHeadAdvancedRequest{WorkDir: in.WorkDir, Checkpoint: cp}
		if err := workflow.ExecuteActivity(quick, a.EnsureHeadAdvanced, advReq).Get(quick, &commits); err != nil {
			return "", err
		}
	}

	// Review the current branch. This blocks until the review completes.
	var reviewOutput string
	if err := workflow.ExecuteActivity(agentCtx, a.RunReviewAgent, ReviewInput{WorkDir: in.WorkDir}).Get(agentCtx, &reviewOutput); err != nil {
		return "", err
	}

	// Structure the review's last output into JSON.
	var structured string
	structReq := StructureReviewRequest{WorkDir: in.WorkDir, LastOutput: reviewOutput}
	if err := workflow.ExecuteActivity(agentCtx, a.StructureReview, structReq).Get(agentCtx, &structured); err != nil {
		return "", err
	}

	// Deterministically validate the structured JSON.
	var validated ValidateReviewResult
	valReq := ValidateReviewRequest{Payload: structured}
	if err := workflow.ExecuteActivity(quick, a.ValidateReviewJSON, valReq).Get(quick, &validated); err != nil {
		return "", err
	}

	// Put the developer's pre-existing local changes back before ending the
	// pass. This is best-effort: a conflict against the implement commits leaves
	// the stash in place for manual resolution rather than failing the run.
	if cp.Stashed {
		restoreReq := RestoreStashRequest{WorkDir: in.WorkDir, Stashed: true}
		if err := workflow.ExecuteActivity(bestEffort, a.RestoreStash, restoreReq).Get(bestEffort, nil); err != nil {
			workflow.GetLogger(ctx).Warn("could not restore stashed changes; they remain in the git stash", "error", err)
		}
	}

	// Actionable items: implement them next pass by continuing as new with the
	// structured payload, unless the pass cap is reached. Otherwise the chain
	// ends.
	if validated.ItemCount > 0 {
		nextPass := in.Pass + 1
		if nextPass >= MaxReviewPasses {
			return fmt.Sprintf(
				"Review stopped after %d pass(es); %d actionable item(s) still remain.",
				MaxReviewPasses, validated.ItemCount), nil
		}
		return "", workflow.NewContinueAsNewError(ctx, ReviewWorkflow,
			ReviewInput{WorkDir: in.WorkDir, Payload: validated.Payload, Pass: nextPass})
	}
	return "Review complete; no actionable items.", nil
}
