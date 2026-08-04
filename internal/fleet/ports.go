package fleet

import "context"

// Agent is the port for running the Pi agent to produce a fleet plan. Planning
// is a read-only step — it reads the repository to inform the decomposition and
// makes no code changes — so the port requires the read-only variant of the
// agent run. The concrete adapter lives in the piagent package (piagent.Agent),
// which enforces the read-only tool policy. Keeping a local port declaration
// keeps this application core decoupled from the driven adapter.
type Agent interface {
	// RunReadOnly executes the agent for prompt in workDir under a read-only tool
	// policy and returns its final message and the total token usage of the
	// session.
	RunReadOnly(ctx context.Context, prompt, workDir string) (output string, tokens int, err error)
}

// Git is the port for the disposable-sandbox git operations planning uses to
// enforce its read-only contract: it runs the agent against a throwaway clone
// and confirms the source repository was left untouched. Implementations
// are driven adapters over the `git` CLI (gitcli.Git satisfies this port).
type Git interface {
	// Fingerprint returns a value that changes whenever any content in dir's
	// worktree or index changes, capturing HEAD plus tracked, staged, and
	// untracked content. The planning tripwire compares fingerprints taken before
	// and after the run: a content fingerprint (rather than a dirty/clean flag)
	// detects a mutation to a file that was already modified when planning
	// started, which a boolean comparison would miss.
	Fingerprint(ctx context.Context, dir string) (string, error)
	// Head returns the commit SHA that HEAD points at in dir. The orchestrator
	// reads it once when a run starts to pin every node's worktree to the same
	// base commit, giving every node a stable start point that a moved checkout
	// cannot shift; a dependent's branch is then seeded by merging its dependency
	// branches on top of this fixed base.
	Head(ctx context.Context, dir string) (string, error)
	// AddDisposableClone creates a throwaway standalone clone of the repo in dir
	// and returns its path, so a read-only step can run against an isolated copy
	// with its own independent .git (refs and object database) that never touches
	// the user's working tree, branch, index, refs, or objects.
	AddDisposableClone(ctx context.Context, dir string) (string, error)
	// RemoveDisposableClone discards the clone previously created at path,
	// including any changes made in it.
	RemoveDisposableClone(ctx context.Context, path string) error
}
