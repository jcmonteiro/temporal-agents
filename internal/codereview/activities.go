package codereview

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// errNoAdvance is the error type returned (non-retryable) when the agent
// produced no new commits.
const errNoAdvance = "NoCommits"

// errDirtyWorktree is the error type returned (non-retryable) when the working
// tree has local changes but a clean one is required.

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

// AgentResult is the output of every agent activity: the agent's final message
// and the total token usage of its session. The workflow accumulates Tokens
// across passes (and, via child workflows, across parent workflows) so the
// run's terminal result can report the whole chain's usage.
type AgentResult struct {
	Output string
	Tokens int
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

// CreateBranchRequest is the input to CreateBranch.
type CreateBranchRequest struct {
	WorkDir string
	// Branch is the branch to create. When empty, a random alias is generated
	// (see RandomBranchAlias), so a retry after a name collision picks a fresh
	// one.
	Branch string
	// WorktreesDir, when non-empty, switches CreateBranch into worktree mode: it
	// creates the branch in a fresh git worktree under this directory instead of
	// checking it out in WorkDir, leaving WorkDir untouched. The worktree lives at
	// <WorktreesDir>/<branch> and becomes the working directory for the rest of
	// the develop flow.
	WorktreesDir string
}

// CreateBranchResult is the output of CreateBranch: the branch that was actually
// created (which may be a generated alias), the directory the rest of the flow
// should run in (the original WorkDir, or the new worktree path in worktree
// mode), and the HEAD the branch starts from.
type CreateBranchResult struct {
	Branch  string
	WorkDir string
	BaseSHA string
}

// RunDevelopRequest is the input to RunDevelopAgent.
type RunDevelopRequest struct {
	WorkDir string
	// Prompt is the caller's instruction describing what to implement.
	Prompt string
}

// EnsureDevelopedRequest is the input to EnsureDeveloped.
type EnsureDevelopedRequest struct {
	WorkDir string
	// BaseSHA is the commit the new branch started from, before the agent ran.
	BaseSHA string
}

// RunImplementRequest is the input to RunImplementAgent.
type RunImplementRequest struct {
	WorkDir string
	// Payload is the previous pass's raw review output whose changes are
	// implemented.
	Payload string
}

// SummarizeRequest is the input to SummarizeLastRun.
type SummarizeRequest struct {
	WorkDir string
}

const errDirtyWorktree = "DirtyWorktree"

// errBranchExists is the error type returned (non-retryable) when the requested
// branch is already checked out on the first attempt, i.e. the caller asked to
// develop on an existing branch rather than a fresh one.
const errBranchExists = "BranchExists"

// CreateBranch creates the branch to develop on and returns the branch name,
// the working directory the rest of the flow should use, and the HEAD SHA the
// branch starts from (so a later step can confirm the agent advanced it).
//
// req.Branch may be empty, in which case a random alias is generated (see
// RandomBranchAlias); the returned CreateBranchResult.Branch reports whichever
// name was used.
//
// When req.WorktreesDir is set it works in a fresh git worktree under that
// directory (see createWorktree), leaving req.WorkDir untouched and requiring no
// clean tree. Otherwise it creates and checks out the branch in req.WorkDir at
// the current HEAD, which requires a clean working tree (no local changes).
//
// The in-place path is idempotent across Temporal retries: when an explicit
// req.Branch is already the checked-out branch on a retry (attempt > 1, i.e.
// this activity already switched before being retried), it skips the clean-tree
// check and branch creation and simply reports the current HEAD. On the first
// attempt an already-checked-out explicit branch is instead rejected: it is
// indistinguishable from a caller asking to develop on an existing branch, and
// skipping the clean-tree check there would let unrelated local changes be
// committed by the agent.
func (a *Activities) CreateBranch(ctx context.Context, req CreateBranchRequest) (CreateBranchResult, error) {
	if req.WorktreesDir != "" {
		return a.createWorktree(ctx, req)
	}

	// An empty request branch means "pick one for me": generate a fresh alias on
	// every invocation so a retry after a name collision does not keep colliding.
	branch := req.Branch
	if branch == "" {
		branch = RandomBranchAlias(time.Now())
	}

	current, err := a.Git.CurrentBranch(ctx, req.WorkDir)
	if err != nil {
		return CreateBranchResult{}, fmt.Errorf("determine current branch: %w", err)
	}
	if current == branch {
		if req.Branch == "" {
			// A generated alias collided with the current branch (astronomically
			// unlikely). Fail retryably so the retry generates a different alias
			// rather than developing on the pre-existing branch.
			return CreateBranchResult{}, fmt.Errorf("generated branch %s is already checked out; retrying", branch)
		}
		if activity.GetInfo(ctx).Attempt <= 1 {
			return CreateBranchResult{}, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("branch %s is already checked out; choose a new branch name", branch),
				errBranchExists, nil)
		}
		head, err := a.Git.Head(ctx, req.WorkDir)
		if err != nil {
			return CreateBranchResult{}, fmt.Errorf("read HEAD: %w", err)
		}
		return CreateBranchResult{Branch: branch, WorkDir: req.WorkDir, BaseSHA: head}, nil
	}

	dirty, err := a.Git.HasChanges(ctx, req.WorkDir)
	if err != nil {
		return CreateBranchResult{}, fmt.Errorf("check for local changes: %w", err)
	}
	if dirty {
		return CreateBranchResult{}, temporal.NewNonRetryableApplicationError(
			"working tree has local changes; commit or stash them first", errDirtyWorktree, nil)
	}

	if err := a.Git.CreateBranch(ctx, req.WorkDir, branch); err != nil {
		return CreateBranchResult{}, fmt.Errorf("create branch %s: %w", branch, err)
	}
	head, err := a.Git.Head(ctx, req.WorkDir)
	if err != nil {
		return CreateBranchResult{}, fmt.Errorf("read HEAD: %w", err)
	}
	return CreateBranchResult{Branch: branch, WorkDir: req.WorkDir, BaseSHA: head}, nil
}

// createWorktree handles CreateBranch's worktree mode: it creates the branch in
// a fresh git worktree under req.WorktreesDir, leaving req.WorkDir untouched, and
// reports that worktree as the working directory for the rest of the flow.
// Because it never mutates WorkDir there is no clean-tree requirement. An empty
// request branch is generated fresh on every invocation, so a retry after a
// branch/path collision picks a new alias (and thus a new worktree path).
func (a *Activities) createWorktree(ctx context.Context, req CreateBranchRequest) (CreateBranchResult, error) {
	branch := req.Branch
	if branch == "" {
		branch = RandomBranchAlias(time.Now())
	}
	worktreePath := filepath.Join(req.WorktreesDir, branch)
	if err := a.Git.AddWorktree(ctx, req.WorkDir, worktreePath, branch); err != nil {
		return CreateBranchResult{}, fmt.Errorf("create worktree for branch %s: %w", branch, err)
	}
	head, err := a.Git.Head(ctx, worktreePath)
	if err != nil {
		return CreateBranchResult{}, fmt.Errorf("read HEAD: %w", err)
	}
	return CreateBranchResult{Branch: branch, WorkDir: worktreePath, BaseSHA: head}, nil
}

// RunDevelopAgent drives the Pi agent to implement the caller's prompt on the
// freshly created branch, committing its work so the workflow's HEAD-advanced
// check can confirm the change landed.
func (a *Activities) RunDevelopAgent(ctx context.Context, req RunDevelopRequest) (AgentResult, error) {
	out, tokens, err := a.Agent.Run(ctx, BuildDevelopPrompt(req.Prompt), req.WorkDir)
	if err != nil {
		return AgentResult{}, err
	}
	return AgentResult{Output: out, Tokens: tokens}, nil
}

// EnsureDeveloped confirms the develop agent advanced HEAD past the branch's
// starting commit and left a clean working tree, returning the new commit SHAs.
// It fails non-retryably when the agent produced no commits, and when it left
// uncommitted changes behind: the develop pass must land all its work as
// commits.
func (a *Activities) EnsureDeveloped(ctx context.Context, req EnsureDevelopedRequest) ([]string, error) {
	commits, err := a.Git.CommitsSince(ctx, req.WorkDir, req.BaseSHA)
	if err != nil {
		return nil, fmt.Errorf("list commits since %s: %w", req.BaseSHA, err)
	}
	if len(commits) == 0 {
		return nil, temporal.NewNonRetryableApplicationError(
			"agent produced no new commits", errNoAdvance, nil)
	}
	dirty, err := a.Git.HasChanges(ctx, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("check for local changes: %w", err)
	}
	if dirty {
		return nil, temporal.NewNonRetryableApplicationError(
			"agent left uncommitted changes", errDirtyWorktree, nil)
	}
	return commits, nil
}

// OpenPR publishes the current branch and ensures an open PR exists for it,
// returning that PR. It pushes HEAD to the branch (so the PR has commits to
// open against), then opens the PR when none exists yet. PR creation is
// idempotent: an already-open PR is returned unchanged rather than opening a
// duplicate, so a retry or a re-run over an already-published branch succeeds.
// Note the idempotency guarantee is about the PR, not the push: the preceding
// non-force push fails if the remote branch has diverged from local HEAD.
func (a *Activities) OpenPR(ctx context.Context, in OpenPRInput) (PullRequest, error) {
	branch, err := a.Git.CurrentBranch(ctx, in.WorkDir)
	if err != nil {
		return PullRequest{}, fmt.Errorf("determine current branch: %w", err)
	}
	if err := a.Git.Push(ctx, in.WorkDir, branch); err != nil {
		return PullRequest{}, fmt.Errorf("push branch %s: %w", branch, err)
	}
	pr, err := a.PRs.EnsureOpen(ctx, in.WorkDir, branch)
	if err != nil {
		return PullRequest{}, err
	}
	return pr, nil
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
func (a *Activities) RunAgent(ctx context.Context, req RunAgentRequest) (AgentResult, error) {
	prompt := BuildPrompt(req.Input.PromptMode, req.Input.PromptText, req.PR.Body, req.Threads)
	out, tokens, err := a.Agent.Run(ctx, prompt, req.Input.WorkDir)
	if err != nil {
		return AgentResult{}, err
	}
	return AgentResult{Output: out, Tokens: tokens}, nil
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
func (a *Activities) RunReviewAgent(ctx context.Context, in ReviewInput) (AgentResult, error) {
	out, tokens, err := a.Agent.Run(ctx, ReviewPrompt, in.WorkDir)
	if err != nil {
		return AgentResult{}, err
	}
	return AgentResult{Output: out, Tokens: tokens}, nil
}

// RunImplementAgent drives the Pi agent to implement the changes called for by
// the previous pass's raw review output, committing its work so the workflow's
// HEAD-advanced check can confirm the change landed (or detect that there was
// nothing to change).
func (a *Activities) RunImplementAgent(ctx context.Context, req RunImplementRequest) (AgentResult, error) {
	prompt := BuildImplementPrompt(req.Payload)
	out, tokens, err := a.Agent.Run(ctx, prompt, req.WorkDir)
	if err != nil {
		return AgentResult{}, err
	}
	return AgentResult{Output: out, Tokens: tokens}, nil
}

// SummarizeLastRun drives the Pi agent to summarize the work of the last
// execution in this workflow run. Because piagent keys the Pi session on the
// workflow run, running this as a later activity in the same run resumes that
// session, so the agent summarizes what it actually did. This resume only
// yields a real summary when an agent activity already ran in this run;
// callers therefore gate it on that (see summarizeForWebhook's agentRan guard)
// so it is never invoked against a fresh, empty session. The summary is used
// only as the webhook notification body; its token usage is intentionally not
// folded into the run's reported total, as this is a meta-step over an already
// finished (or failed) run rather than part of the work itself.
func (a *Activities) SummarizeLastRun(ctx context.Context, req SummarizeRequest) (string, error) {
	out, _, err := a.Agent.Run(ctx, SummarizePrompt, req.WorkDir)
	if err != nil {
		return "", err
	}
	return out, nil
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
