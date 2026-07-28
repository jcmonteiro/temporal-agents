package codereview

import "context"

// Git is the port for the local repository operations the workflow needs.
// Implementations are driven adapters over the `git` CLI.
type Git interface {
	// CurrentBranch returns the checked-out branch name in dir.
	CurrentBranch(ctx context.Context, dir string) (string, error)
	// CreateBranch creates and checks out a new branch at the current HEAD in
	// dir.
	CreateBranch(ctx context.Context, dir, branch string) error
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
}

// PullRequests is the port for the GitHub operations the workflow needs.
// Implementations are driven adapters over the `gh` CLI.
type PullRequests interface {
	// FindOpen locates the single open PR whose head is branch in the repo at
	// dir. It returns an error when there is no open PR or more than one.
	FindOpen(ctx context.Context, dir, branch string) (PullRequest, error)
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
	// Run executes the agent for prompt in workDir and returns its final
	// message.
	Run(ctx context.Context, prompt, workDir string) (string, error)
}
