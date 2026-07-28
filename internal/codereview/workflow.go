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
	"temporal-agents/internal/wfnotify"
)

// reviewPollInterval is how long the workflow sleeps between checks for a
// still-pending Copilot review.
const reviewPollInterval = time.Minute

const (
	// completeSummaryTimeout bounds the last-run summary step on a successful
	// terminal path. Like the other agent activities it is a long-running Pi run,
	// so it gets the same hour budget.
	completeSummaryTimeout = time.Hour
	// failureSummaryTimeout bounds the same summary step on the failure path,
	// where it runs on the disconnected context *before* the best-effort failure
	// notification is delivered and the workflow closes. A full hour there would
	// let a slow summary hold the failure heads-up (and workflow close) hostage
	// far beyond wfnotify's delivery budget, so the failure path caps it well
	// below the completion budget. A summary that does not finish in time is
	// simply dropped and the webhook falls back to the plain body.
	failureSummaryTimeout = 2 * time.Minute
)

// summarizeForWebhook runs the SummarizeLastRun activity when enabled and an
// agent actually ran in this workflow run, returning the agent's summary, which
// the caller attaches as the webhook-only notification body. It is best-effort:
// when disabled it returns "", and any failure is logged and swallowed
// (returning "") so the webhook simply falls back to the plain body rather than
// the run's notification being lost.
//
// The agentRan guard matters because piagent keys the Pi session on the
// workflow run: SummarizeLastRun only resumes real work if an agent activity
// already ran in this run. On terminal paths where none did (e.g. a pilot pass
// that found nothing to address, or any failure before the first agent step), a
// summary would run against a fresh, empty session and fabricate a description
// of work that never happened. Guarding on agentRan makes the webhook cleanly
// fall back to the plain Body on those paths instead.
func summarizeForWebhook(ctx workflow.Context, summaryEnabled, agentRan bool, workDir string, timeout time.Duration) string {
	if !summaryEnabled || !agentRan {
		return ""
	}
	// The summary is a long-running agent step, like the other agent activities,
	// but runs once: a best-effort meta-step should not retry and multiply cost.
	// The timeout differs by path (see completeSummaryTimeout/failureSummaryTimeout).
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var a *Activities
	var summary string
	if err := workflow.ExecuteActivity(opts, a.SummarizeLastRun, SummarizeRequest{WorkDir: workDir}).Get(opts, &summary); err != nil {
		workflow.GetLogger(ctx).Warn("could not summarize last Pi execution for webhook", "error", err)
		return ""
	}
	return summary
}

// notifyComplete delivers a completion notification best-effort, attaching the
// optional last-run summary as the webhook-only body when the summary is
// enabled and an agent ran in this run (see summarizeForWebhook for why
// agentRan is required).
func notifyComplete(ctx workflow.Context, summaryEnabled, agentRan bool, workDir string, n notification.Notification) {
	n.WebhookBody = summarizeForWebhook(ctx, summaryEnabled, agentRan, workDir, completeSummaryTimeout)
	wfnotify.NotifyBestEffort(ctx, n)
}

// notifyFailure delivers a best-effort failure notification via
// wfnotify.NotifyFailureBestEffortWith, reusing that helper's shared failure
// policy (no-op on success and on continue-as-new; delivery on a disconnected
// context so a cancellation-caused failure still notifies). When the summary is
// enabled it enriches the notification with the last Pi execution's summary as
// the webhook-only body, run on the same disconnected context so it survives a
// cancelled workflow. The summary is attached only when an agent ran in this
// run (see summarizeForWebhook for why agentRan is required).
func notifyFailure(ctx workflow.Context, title, workDir string, summaryEnabled, agentRan bool, err error) {
	wfnotify.NotifyFailureBestEffortWith(ctx, title, err,
		func(dctx workflow.Context, n notification.Notification) notification.Notification {
			n.WebhookBody = summarizeForWebhook(dctx, summaryEnabled, agentRan, workDir, failureSummaryTimeout)
			return n
		})
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
//
// The chain always runs in production (every caller sets Chain), so its only
// terminal step is the no-comments pass, where no agent ran and there is
// nothing to summarize. A pilot loop's meaningful work therefore lives entirely
// in the passes that address comments and then continue as new. So when
// in.Summary is set, each addressing pass summarizes its own Pi session and
// delivers that summary (webhook body) before continuing—otherwise --summary
// would produce no webhook body at all on the real, always-chained path.
func PilotWorkflow(ctx workflow.Context, in PilotInput) (summary string, err error) {
	// Notify best-effort when the pilot loop fails. Continue-as-new is a control
	// signal (chained passes), not a failure, so NotifyFailureBestEffort excludes
	// it. agentRan is read by the deferred notify to decide whether summarizing
	// the last run is meaningful (a session exists to resume).
	var agentRan bool
	defer func() { notifyFailure(ctx, "Copilot review chain failed", in.WorkDir, in.Summary, agentRan, err) }()

	var addressed bool
	var tokens int
	var prURL string
	summary, addressed, tokens, prURL, err = runPilotOnce(ctx, in, &agentRan)
	if err != nil {
		return "", err
	}
	// Fold this pass's usage into the running total carried across chained runs.
	total := in.TokensSoFar + tokens
	if in.Chain && addressed {
		// This pass addressed comments, so the loop continues as new and never
		// reaches the terminal notifyComplete below. Continue-as-new is normally a
		// silent control signal, but --summary explicitly asks for a webhook summary
		// of the agent's work, and on the always-chained production path every
		// meaningful pass is one that addressed comments and chains. So when
		// --summary is set, summarize this pass's Pi session and deliver it as the
		// webhook-only body before continuing. The summary runs synchronously here
		// because continue-as-new cancels any in-flight activity; without --summary
		// the chain stays silent exactly as before.
		if in.Summary {
			wfnotify.NotifyBestEffort(ctx, notification.Notification{
				Title:       "Copilot review pass complete",
				Body:        summary,
				URL:         prURL,
				WebhookBody: summarizeForWebhook(ctx, in.Summary, agentRan, in.WorkDir, completeSummaryTimeout),
			})
		}
		next := in
		next.TokensSoFar = total
		return "", workflow.NewContinueAsNewError(ctx, PilotWorkflow, next)
	}
	summary = withTokenTotal(summary, total)
	notifyComplete(ctx, in.Summary, agentRan, in.WorkDir, notification.Notification{Title: "Copilot review chain complete", Body: summary, URL: prURL})
	return summary, nil
}

// runPilotOnce performs a single pass of the loop. It returns a human-readable
// summary, whether it actually addressed any comments (false when the PR had no
// unresolved comments, which the caller uses to decide whether to chain), the
// pass's total agent token usage, and a hyperlink to the PR the pass operated
// on (empty when it failed before determining the PR) so the caller can include
// it in the completion notification. It sets *agentRan to true once the Pi
// agent has run in this pass, so callers know a resumable Pi session exists for
// the optional last-run summary.
func runPilotOnce(ctx workflow.Context, in PilotInput, agentRan *bool) (string, bool, int, string, error) {
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
	// The agent has run: a Pi session now exists for this run that a later
	// SummarizeLastRun step could resume.
	*agentRan = true

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

// OpenPRWorkflow publishes the current branch, ensures an open pull request
// exists for it, and requests a fresh Copilot review. Both steps are
// idempotent, so re-running over an already-published branch or an already-open
// PR simply succeeds. It is used as a supervised stage of the
// `develop --with-remote` pipeline but is a standalone workflow in its own
// right.
func OpenPRWorkflow(ctx workflow.Context, in OpenPRInput) (result string, err error) {
	// No agent runs here, so there is never a Pi session to summarize; pass
	// summaryEnabled/agentRan false so the notification simply carries the plain
	// body.
	defer func() { notifyFailure(ctx, "Opening the pull request failed", in.WorkDir, false, false, err) }()

	// Quick, deterministic git/GitHub steps. Idempotent enough to retry, but
	// should not run forever.
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})

	var a *Activities

	var pr PullRequest
	if err := workflow.ExecuteActivity(quick, a.OpenPR, in).Get(quick, &pr); err != nil {
		return "", err
	}
	if err := workflow.ExecuteActivity(quick, a.RequestCopilotReview, pr).Get(quick, nil); err != nil {
		return "", err
	}

	// OpenPR is idempotent: it returns an already-open PR unchanged rather than
	// creating one, so a supported retry/re-run reaches here without necessarily
	// having opened anything. Use outcome-neutral wording that holds whether the PR
	// was just created or already existed.
	summary := fmt.Sprintf("PR #%d is open and a Copilot review was requested.", pr.Number)
	notifyComplete(ctx, false, false, in.WorkDir, notification.Notification{
		Title: "Pull request ready", Body: summary, URL: pr.URL})
	return summary, nil
}

// DevelopWorkflow drives the "code develop" flow. In sequence it:
//
//   - Creates the requested branch off a clean working tree (CreateBranch fails
//     when there are uncommitted local changes) and records the starting HEAD.
//   - Has the Pi agent implement the caller's prompt on that branch.
//   - Confirms the agent advanced HEAD and left a clean working tree
//     (EnsureDeveloped fails when nothing was committed or changes remain).
//
// What happens next depends on in.WithRemote:
//
//   - When false (the default), it triggers the local review loop
//     (ReviewWorkflow) on the new branch as an abandoned child, so it keeps
//     running—and notifies on completion—after this workflow returns, exactly
//     like a standalone "code review" run. It returns once the review has been
//     started, reporting the review workflow's ID so the caller can watch it.
//   - When true, it instead supervises the full remote pipeline as a sequence
//     of waited-on children: the review loop, then OpenPRWorkflow (open the PR
//     and request Copilot), then the pilot loop. Each child still continues as
//     new internally, but a parent's child future resolves only when the whole
//     continue-as-new chain converges, so this workflow stays alive and
//     oversees the pipeline end to end, returning only once the pilot loop has
//     finished folding in Copilot feedback.
func DevelopWorkflow(ctx workflow.Context, in DevelopInput) (result string, err error) {
	// Notify best-effort when development fails before the review loop is started.
	// agentRan gates summarizing the last run: a failure before the develop agent
	// runs has no Pi session to resume.
	var agentRan bool
	defer func() { notifyFailure(ctx, "Development failed", in.WorkDir, in.Summary, agentRan, err) }()

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
	// The develop agent has run: a Pi session now exists for this run that a
	// later SummarizeLastRun step could resume.
	agentRan = true

	var commits []string
	if err := workflow.ExecuteActivity(quick, a.EnsureDeveloped,
		EnsureDevelopedRequest{WorkDir: in.WorkDir, BaseSHA: base}).Get(quick, &commits); err != nil {
		return "", err
	}

	// Summarize this develop run's Pi session for the webhook *before* spawning
	// the review child. The child review loop runs its own agent passes in the
	// same in.WorkDir, so deferring the summary until after the spawn would run
	// this summary's Pi process concurrently with the review child's first pass
	// over the same working tree. Summarizing first keeps the develop summary
	// running against a quiescent tree, with no overlap against the child.
	webhookBody := summarizeForWebhook(ctx, in.Summary, agentRan, in.WorkDir, completeSummaryTimeout)

	if in.WithRemote {
		return developWithRemote(ctx, in, commits, agentResult.Tokens, webhookBody)
	}

	// Trigger the review loop as an abandoned child so it outlives this workflow.
	reviewID := "review-" + workflow.GetInfo(ctx).WorkflowExecution.ID
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        reviewID,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	// Seed the review loop with this develop session's token usage so its
	// terminal result reports the whole tree's usage ("including parent
	// workflows"). When --summary is set it is also propagated, so the spawned
	// review loop summarizes its own (later) run for its completion webhook. This
	// is intentional but means a single `develop --summary` triggers two
	// independent summary agent runs (this develop completion plus the review
	// completion), each a full, billable Pi run.
	child := workflow.ExecuteChildWorkflow(childCtx, ReviewWorkflow,
		ReviewInput{WorkDir: in.WorkDir, TokensSoFar: agentResult.Tokens, Summary: in.Summary})
	if err := child.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return "", fmt.Errorf("start review workflow: %w", err)
	}

	summary := withTokenTotal(fmt.Sprintf("Developed branch %s with %d commit(s); started review %s.",
		in.Branch, len(commits), reviewID), agentResult.Tokens)
	// Deliver the completion notification with the pre-computed webhook summary
	// (see above); wfnotify.NotifyBestEffort is used directly here rather than
	// notifyComplete because the summary was intentionally run before the spawn.
	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Development complete",
		Body: fmt.Sprintf("Developed branch %s with %d commit(s) successfully. The review cycle will now commence.",
			in.Branch, len(commits)),
		WebhookBody: webhookBody,
	})
	return summary, nil
}

// developWithRemote runs the `develop --with-remote` tail: after development has
// landed its commits, it supervises the remote pipeline as a sequence of
// waited-on children—the local review loop, then OpenPRWorkflow (open the PR
// and request Copilot), then the pilot loop—returning only once the pilot loop
// has finished. Each child continues as new internally; a parent's child future
// resolves only when the whole continue-as-new chain converges, so waiting on
// each in turn keeps this workflow alive and overseeing the pipeline end to end
// rather than continuing as new itself.
//
// The children are supervised (not abandoned) so this workflow stays their
// parent for the pipeline's lifetime. Each still emits its own completion
// notification. This workflow emits two of its own: a "Development complete"
// notification up front (carrying the develop step's --summary webhook body,
// since that summary describes the develop step and would be stale by the time
// the pipeline converges) and a final "Remote pipeline complete" once the whole
// pipeline has converged (with no summary body, so the terminal notification is
// not misattributed the oldest, develop-only summary).
func developWithRemote(ctx workflow.Context, in DevelopInput, commits []string, tokens int, webhookBody string) (string, error) {
	id := workflow.GetInfo(ctx).WorkflowExecution.ID

	// Deliver the develop-completion notification now, before the pipeline spawns.
	// webhookBody summarizes *this develop step*, computed against the quiescent
	// tree in DevelopWorkflow; attaching it here (rather than to the terminal
	// pipeline notification below) keeps the summary matched to the step it
	// describes. The review and pilot children emit their own, fresher summaries as
	// they complete.
	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Development complete",
		Body: fmt.Sprintf("Developed branch %s with %d commit(s) successfully. The review, pull request, and Copilot pilot stages will now commence.",
			in.Branch, len(commits)),
		WebhookBody: webhookBody,
	})

	// The local review loop. Seed it with this develop session's token usage so
	// its own result reports the whole tree's usage, and propagate --summary so it
	// summarizes its own run for its completion webhook. Combined with the develop
	// summary attached above and the pilot summaries below, a single
	// `develop --with-remote --summary` therefore triggers at least three
	// independent, billable summary runs (develop, review, and one per pilot pass
	// that addresses comments)—at least one more than the non-remote path's two
	// (develop plus review). The pilot summarizes on every addressing pass, so a
	// pilot loop that iterates N times bills N pilot summaries, not one.
	reviewCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "review-" + id})
	if err := workflow.ExecuteChildWorkflow(reviewCtx, ReviewWorkflow,
		ReviewInput{WorkDir: in.WorkDir, TokensSoFar: tokens, Summary: in.Summary}).Get(ctx, nil); err != nil {
		return "", fmt.Errorf("review workflow: %w", err)
	}

	// Open the PR and request a Copilot review.
	openCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "open-pr-" + id})
	if err := workflow.ExecuteChildWorkflow(openCtx, OpenPRWorkflow,
		OpenPRInput{WorkDir: in.WorkDir}).Get(ctx, nil); err != nil {
		return "", fmt.Errorf("open PR workflow: %w", err)
	}

	// The pilot loop. It always chains (Chain: true), so waiting on it here blocks
	// until a pass finds no unresolved comments left to address and the loop
	// converges.
	pilotCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "pilot-" + id})
	if err := workflow.ExecuteChildWorkflow(pilotCtx, PilotWorkflow,
		PilotInput{WorkDir: in.WorkDir, Chain: true, Summary: in.Summary}).Get(ctx, nil); err != nil {
		return "", fmt.Errorf("pilot workflow: %w", err)
	}

	// Report only the develop step's own token usage: the review and pilot children
	// run in their own sessions (their results are discarded via .Get(ctx, nil)) and
	// emit their own totals, so a "across all sessions" figure computed from develop
	// tokens alone would under-report and mislead.
	summary := withDevelopStepTokens(fmt.Sprintf(
		"Developed branch %s with %d commit(s); ran the review loop, opened the PR, and completed the Copilot pilot loop.",
		in.Branch, len(commits)), tokens)
	// The terminal pipeline notification carries no summary body: the develop
	// summary was delivered up front with the develop-completion notification, and
	// the review and pilot children have since emitted their own fresher summaries.
	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Remote pipeline complete",
		Body: fmt.Sprintf("Developed branch %s with %d commit(s); the review, pull request, and Copilot pilot stages have all completed.",
			in.Branch, len(commits)),
	})
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
func ReviewWorkflow(ctx workflow.Context, in ReviewInput) (result string, err error) {
	// Notify best-effort when the review loop fails. Continue-as-new is a control
	// signal (looping passes), not a failure, so NotifyFailureBestEffort excludes
	// it. agentRan gates summarizing the last run: a failure before any agent step
	// (e.g. MarkHeadAndStash) has no Pi session to resume.
	var agentRan bool
	defer func() { notifyFailure(ctx, "Local review chain failed", in.WorkDir, in.Summary, agentRan, err) }()

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
		// The implement agent has run: a Pi session now exists for this run that a
		// later SummarizeLastRun step could resume.
		agentRan = true
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
				notifyComplete(ctx, in.Summary, agentRan, in.WorkDir, notification.Notification{Title: "Local review chain complete", Body: summary})
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
	// The review agent has run: a Pi session now exists for this run that a later
	// SummarizeLastRun step could resume.
	agentRan = true
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
		notifyComplete(ctx, in.Summary, agentRan, in.WorkDir, notification.Notification{Title: "Local review chain complete", Body: summary})
		return summary, nil
	}
	return "", workflow.NewContinueAsNewError(ctx, ReviewWorkflow,
		ReviewInput{WorkDir: in.WorkDir, Payload: reviewOutput, Pass: nextPass, TokensSoFar: total, Summary: in.Summary})
}
