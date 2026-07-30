// Package gitcli is a driven adapter over the `git` command line. It implements
// the codereview.Git port and the cleanup.Git port (the latter in cleanup.go).
package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"temporal-agents/internal/codereview"
)

// Git runs local git operations against a repository directory.
type Git struct{}

// New returns a git CLI adapter.
func New() Git { return Git{} }

// CurrentBranch returns the checked-out branch name in dir.
func (g Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CreateBranch creates and checks out a new branch at the current HEAD in dir.
func (g Git) CreateBranch(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "checkout", "-b", branch)
	return classifyExists(err)
}

// AddWorktree creates a new worktree at worktreePath checked out on a new
// branch created at the current HEAD of the repository in dir.
func (g Git) AddWorktree(ctx context.Context, dir, worktreePath, branch string) error {
	_, err := run(ctx, dir, "worktree", "add", worktreePath, "-b", branch)
	return classifyExists(err)
}

// classifyExists wraps err with codereview.ErrBranchOrWorktreeExists when git's
// failure reports that the branch or worktree path already exists, so the
// activity layer can treat it as a permanent (non-retryable) condition rather
// than exhausting retries on an error the same name can never fix. git phrases
// both cases with the "already exists" substring ("a branch named '...' already
// exists", "'<path>' already exists").
func classifyExists(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("%w: %v", codereview.ErrBranchOrWorktreeExists, err)
	}
	return err
}

// Head returns the commit SHA that HEAD points at in dir.
func (g Git) Head(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasChanges reports whether dir has uncommitted changes, including untracked
// files.
func (g Git) HasChanges(ctx context.Context, dir string) (bool, error) {
	out, err := run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Stash saves local changes (including untracked files) off to the side.
func (g Git) Stash(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "stash", "push", "--include-untracked")
	return err
}

// StashPop restores the most recently stashed changes.
func (g Git) StashPop(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "stash", "pop")
	return err
}

// CommitsSince returns the SHAs of commits made after sha up to HEAD, oldest
// first.
func (g Git) CommitsSince(ctx context.Context, dir, sha string) ([]string, error) {
	out, err := run(ctx, dir, "rev-list", "--reverse", sha+"..HEAD")
	if err != nil {
		return nil, err
	}
	return parseRevList(out), nil
}

// Push publishes HEAD to the named branch on origin.
func (g Git) Push(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "push", "origin", "HEAD:"+branch)
	return err
}

// parseRevList splits `git rev-list` output into SHAs, dropping blank lines.
func parseRevList(out string) []string {
	var shas []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			shas = append(shas, s)
		}
	}
	return shas
}

// run executes `git -C dir <args...>` and returns stdout, wrapping failures
// with stderr for context.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Pin the locale to C so git emits stable, English stderr. classifyExists
	// matches the "already exists" substring, which would silently stop matching
	// under a localized LANG/LC_ALL and degrade the fast-fail path to opaque retry.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
