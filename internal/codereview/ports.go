package codereview

import (
	"context"
	"errors"
)

// ErrBranchOrWorktreeExists is returned (wrapped) by Git.CreateBranch and
// Git.AddWorktree when the requested branch or the target worktree path already
// exists. Callers test for it with errors.Is to distinguish this permanent
// condition — retrying with the same explicit name cannot succeed — from
// transient git failures worth retrying.
var ErrBranchOrWorktreeExists = errors.New("branch or worktree already exists")

// Git is the port for the local repository operations the workflow needs.
// Implementations are driven adapters over the `git` CLI.
type Git interface {
	// CurrentBranch returns the checked-out branch name in dir.
	CurrentBranch(ctx context.Context, dir string) (string, error)
	// CreateBranch creates and checks out a new branch in dir. When startPoint is
	// non-empty the branch is created at that commit-ish; an empty startPoint
	// falls back to dir's current HEAD. It wraps ErrBranchOrWorktreeExists when
	// the branch already exists.
	CreateBranch(ctx context.Context, dir, branch, startPoint string) error
	// AddWorktree creates a new worktree at worktreePath checked out on a new
	// branch created off the repository in dir. When startPoint is non-empty the
	// branch is created at that commit-ish; an empty startPoint falls back to the
	// repository's current HEAD. It wraps ErrBranchOrWorktreeExists when the
	// branch or the worktree path already exists.
	AddWorktree(ctx context.Context, dir, worktreePath, branch, startPoint string) error
	// Head returns the commit SHA that HEAD points at in dir.
	Head(ctx context.Context, dir string) (string, error)
	// HasChanges reports whether dir has uncommitted changes (tracked or
	// untracked).
	HasChanges(ctx context.Context, dir string) (bool, error)
	// Stash saves local changes off to the side.
	Stash(ctx context.Context, dir string) error
	// StashPop restores the most recently stashed changes.
	StashPop(ctx context.Context, dir string) error
	// CommitsSince returns the SHAs of commits made after sha up to HEAD, in
	// chronological (oldest-first) order.
	CommitsSince(ctx context.Context, dir, sha string) ([]string, error)
	// Push publishes HEAD to the named branch on the origin remote.
	Push(ctx context.Context, dir, branch string) error
	// MergeBranch merges branch into the branch currently checked out in dir
	// (creating a merge commit or fast-forwarding). It seeds a dependent's branch
	// with the committed work of the branches it depends on. A conflict returns an
	// error and leaves the merge in progress for the caller to resolve or abort.
	MergeBranch(ctx context.Context, dir, branch string) error
	// AbortMerge aborts an in-progress merge in dir, restoring the pre-merge state
	// so a branch that could not be seeded cleanly is left clean rather than
	// half-merged with conflict markers.
	AbortMerge(ctx context.Context, dir string) error
	// HasConflicts reports whether dir has unmerged paths from an in-progress
	// merge. It lets a caller tell a merge that stopped on conflict (recoverable
	// by agent resolution or AbortMerge) apart from other git failures, and verify
	// a resolution attempt left no conflict markers.
	HasConflicts(ctx context.Context, dir string) (bool, error)
	// IsAncestor reports whether commit ancestor is an ancestor of (i.e. reachable
	// from) commit descendant in dir. It lets a caller prove a dependency branch
	// was actually merged: after a resolution the dependency tip must be an
	// ancestor of HEAD, which distinguishes a genuine merge commit from an aborted
	// merge that left HEAD on its pre-merge commit.
	IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error)
}

// PullRequests is the port for the GitHub operations the workflow needs.
// Implementations are driven adapters over the `gh` CLI.
type PullRequests interface {
	// FindOpen locates the single open PR whose head is branch in the repo at
	// dir. It returns an error when there is no open PR or more than one.
	FindOpen(ctx context.Context, dir, branch string) (PullRequest, error)
	// EnsureOpen returns the open PR whose head is branch, creating it when none
	// exists yet. It is idempotent: an already-open PR is returned unchanged
	// rather than opening a duplicate.
	EnsureOpen(ctx context.Context, dir, branch string) (PullRequest, error)
	// ReviewOngoing reports whether a requested Copilot review is still pending
	// (i.e. requested but not yet delivered).
	ReviewOngoing(ctx context.Context, pr PullRequest) (bool, error)
	// UnresolvedThreads returns the PR's unresolved review threads.
	UnresolvedThreads(ctx context.Context, pr PullRequest) ([]ReviewThread, error)
	// Reply posts body as a reply on the given review thread.
	Reply(ctx context.Context, pr PullRequest, threadID, body string) error
	// Resolve marks the given review thread as resolved.
	Resolve(ctx context.Context, pr PullRequest, threadID string) error
	// RequestCopilotReview requests a fresh Copilot review on the PR.
	RequestCopilotReview(ctx context.Context, pr PullRequest) error
}

// Agent is the port for running the Pi agent. The concrete adapter lives in the
// piagent package.
type Agent interface {
	// Run executes the agent for prompt in workDir and returns its final message
	// and the total token usage of the session.
	Run(ctx context.Context, prompt, workDir string) (output string, tokens int, err error)
}
