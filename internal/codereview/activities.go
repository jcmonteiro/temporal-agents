package codereview

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// errNoAdvance is the error type returned (non-retryable) when the agent
// produced no new commits.
const errNoAdvance = "NoCommits"

// Activities bundles the driven adapters the workflow orchestrates. It is
// registered with the Temporal worker; each exported method is an activity.
type Activities struct {
	Git   Git
	PRs   PullRequests
	Agent Agent
}

// LoadCommentsResult is the output of LoadUnresolvedComments.
type LoadCommentsResult struct {
	Threads []ReviewThread
}

// RunAgentRequest is the input to RunAgent.
type RunAgentRequest struct {
	Input   PilotInput
	PR      PullRequest
	Threads []ReviewThread
}

// EnsureHeadAdvancedRequest is the input to EnsureHeadAdvanced.
type EnsureHeadAdvancedRequest struct {
	WorkDir    string
	Checkpoint Checkpoint
}

// PushBranchRequest is the input to PushBranch.
type PushBranchRequest struct {
	WorkDir string
	Branch  string
}

// ReplyAndResolveRequest is the input to ReplyAndResolve.
type ReplyAndResolveRequest struct {
	PR         PullRequest
	Threads    []ReviewThread
	CommitSHAs []string
}

// RestoreStashRequest is the input to RestoreStash.
type RestoreStashRequest struct {
	WorkDir string
	Stashed bool
}

// RunImplementRequest is the input to RunImplementAgent.
type RunImplementRequest struct {
	WorkDir string
	// Payload is the previous pass's raw review output whose changes are
	// implemented.
	Payload string
}

// DeterminePR finds the current branch and its single open PR, failing when
// there is no open PR or more than one.
func (a *Activities) DeterminePR(ctx context.Context, in PilotInput) (PullRequest, error) {
	branch, err := a.Git.CurrentBranch(ctx, in.WorkDir)
	if err != nil {
		return PullRequest{}, fmt.Errorf("determine current branch: %w", err)
	}
	pr, err := a.PRs.FindOpen(ctx, in.WorkDir, branch)
	if err != nil {
		return PullRequest{}, err
	}
	return pr, nil
}

// CheckOngoingReview reports whether a Copilot review is still pending on the
// PR. It is a single, quick probe; the workflow polls it (sleeping between
// checks) so the waiting lives in deterministic workflow state rather than in a
// long-running, heartbeating activity.
func (a *Activities) CheckOngoingReview(ctx context.Context, pr PullRequest) (bool, error) {
	ongoing, err := a.PRs.ReviewOngoing(ctx, pr)
	if err != nil {
		return false, fmt.Errorf("check ongoing review: %w", err)
	}
	return ongoing, nil
}

// LoadUnresolvedComments returns the PR's unresolved review threads. An empty
// result is a success: there is simply nothing to address.
func (a *Activities) LoadUnresolvedComments(ctx context.Context, pr PullRequest) (LoadCommentsResult, error) {
	threads, err := a.PRs.UnresolvedThreads(ctx, pr)
	if err != nil {
		return LoadCommentsResult{}, fmt.Errorf("load unresolved comments: %w", err)
	}
	return LoadCommentsResult{Threads: threads}, nil
}

// MarkHeadAndStash records the current HEAD and stashes local changes if any,
// returning a checkpoint the later steps compare against.
//
// Note: each review-loop pass calls this before running the agent and pops via
// RestoreStash at the end. A pop that conflicts leaves the stash in place, so a
// later pass would stash again on top of it. This is bounded by MaxReviewPasses
// (the loop cannot run forever), but a leftover stash still needs manual
// reconciliation (git stash list / git stash pop) once the loop ends.
func (a *Activities) MarkHeadAndStash(ctx context.Context, in PilotInput) (Checkpoint, error) {
	head, err := a.Git.Head(ctx, in.WorkDir)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read HEAD: %w", err)
	}
	dirty, err := a.Git.HasChanges(ctx, in.WorkDir)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("check for local changes: %w", err)
	}
	cp := Checkpoint{HeadSHA: head}
	if dirty {
		if err := a.Git.Stash(ctx, in.WorkDir); err != nil {
			return Checkpoint{}, fmt.Errorf("stash local changes: %w", err)
		}
		cp.Stashed = true
	}
	return cp, nil
}

// RunAgent is the only activity that drives the Pi agent. It builds the prompt
// from the (default/append/replace) instruction plus the unresolved comments
// and hands it to the agent, which is expected to commit its work.
func (a *Activities) RunAgent(ctx context.Context, req RunAgentRequest) (string, error) {
	prompt := BuildPrompt(req.Input.PromptMode, req.Input.PromptText, req.PR.Body, req.Threads)
	return a.Agent.Run(ctx, prompt, req.Input.WorkDir)
}

// EnsureHeadAdvanced verifies the agent produced new commits. When it did, the
// new commit SHAs are returned. When it did not, any stash taken earlier is
// restored and a non-retryable error is returned.
func (a *Activities) EnsureHeadAdvanced(ctx context.Context, req EnsureHeadAdvancedRequest) ([]string, error) {
	commits, err := a.Git.CommitsSince(ctx, req.WorkDir, req.Checkpoint.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("list commits since %s: %w", req.Checkpoint.HeadSHA, err)
	}
	if len(commits) == 0 {
		if req.Checkpoint.Stashed {
			if popErr := a.Git.StashPop(ctx, req.WorkDir); popErr != nil {
				// Surface the restore failure; it is retryable because the pop
				// may succeed once a transient issue clears.
				return nil, fmt.Errorf("restore stash after no commits: %w", popErr)
			}
		}
		return nil, temporal.NewNonRetryableApplicationError(
			"agent produced no new commits", errNoAdvance, nil)
	}
	return commits, nil
}

// PushBranch publishes the agent's new commits to the PR's feature branch so
// the pull request and any subsequent review see them.
func (a *Activities) PushBranch(ctx context.Context, req PushBranchRequest) error {
	if err := a.Git.Push(ctx, req.WorkDir, req.Branch); err != nil {
		return fmt.Errorf("push to %s: %w", req.Branch, err)
	}
	return nil
}

// ReplyAndResolve posts the concatenated commit hashes as a reply on every
// thread and marks each thread resolved.
func (a *Activities) ReplyAndResolve(ctx context.Context, req ReplyAndResolveRequest) error {
	body := FormatReplyBody(req.CommitSHAs)
	for _, t := range req.Threads {
		if err := a.PRs.Reply(ctx, req.PR, t.ID, body); err != nil {
			return fmt.Errorf("reply to thread %s: %w", t.ID, err)
		}
		if err := a.PRs.Resolve(ctx, req.PR, t.ID); err != nil {
			return fmt.Errorf("resolve thread %s: %w", t.ID, err)
		}
		activity.RecordHeartbeat(ctx, t.ID)
	}
	return nil
}

// RequestCopilotReview requests a fresh Copilot review on the PR.
func (a *Activities) RequestCopilotReview(ctx context.Context, pr PullRequest) error {
	if err := a.PRs.RequestCopilotReview(ctx, pr); err != nil {
		return fmt.Errorf("request Copilot review: %w", err)
	}
	return nil
}

// RunReviewAgent drives the Pi agent to review the current branch and returns
// its final message. Unlike the Copilot flow, the review runs on the host and
// blocks until it completes, so no waiting/polling is needed afterwards.
func (a *Activities) RunReviewAgent(ctx context.Context, in ReviewInput) (string, error) {
	return a.Agent.Run(ctx, ReviewPrompt, in.WorkDir)
}

// RunImplementAgent drives the Pi agent to implement the changes called for by
// the previous pass's raw review output, committing its work so the workflow's
// HEAD-advanced check can confirm the change landed (or detect that there was
// nothing to change).
func (a *Activities) RunImplementAgent(ctx context.Context, req RunImplementRequest) (string, error) {
	prompt := BuildImplementPrompt(req.Payload)
	return a.Agent.Run(ctx, prompt, req.WorkDir)
}

// RestoreStash pops changes stashed by MarkHeadAndStash. It is invoked as a
// best-effort courtesy on the success path so the developer's pre-existing
// local changes are put back; callers may ignore its error (e.g. a merge
// conflict against the agent's new commits leaves the stash intact for manual
// resolution).
func (a *Activities) RestoreStash(ctx context.Context, req RestoreStashRequest) error {
	if !req.Stashed {
		return nil
	}
	if err := a.Git.StashPop(ctx, req.WorkDir); err != nil {
		return fmt.Errorf("restore stash: %w", err)
	}
	return nil
}
