package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"temporal-agents/internal/cleanup"
)

// List returns the worktrees under baseDir that belong to the repository at
// repoDir. It parses `git worktree list --porcelain` and keeps only the entries
// whose path lives under baseDir, i.e. the ones temporal-agents created with
// `code develop --worktree`. It implements the cleanup.Git port.
func (g Git) List(ctx context.Context, repoDir, baseDir string) ([]cleanup.Worktree, error) {
	out, err := run(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out, baseDir), nil
}

// Merged reports whether branch is fully contained in repoDir's current HEAD,
// i.e. every commit on the branch is already an ancestor of HEAD. It implements
// the cleanup.Git port.
//
// Note: this is an ancestor test, so a branch that was squash- or rebase-merged
// (its commits rewritten) reads as unmerged. The forced-delete path in cleanup
// exists precisely for that case.
func (g Git) Merged(ctx context.Context, repoDir, branch string) (bool, error) {
	// `merge-base --is-ancestor A B` exits 0 when A is an ancestor of B and 1
	// when it is not; anything else is a real error.
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", "--is-ancestor", branch, "HEAD")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// Remove deletes the worktree and its branch. When force is set it removes a
// worktree that still has local changes and force-deletes an unmerged branch;
// otherwise git refuses both. It implements the cleanup.Git port.
//
// Remove is idempotent about an already-gone worktree directory: when the path
// no longer exists it prunes the stale registration and proceeds straight to
// the branch delete. This matters for the force-retry path in cleanup, where an
// earlier attempt removed the worktree but failed to delete the branch; without
// this a retried `worktree remove` would fail on the missing path ("not a
// working tree") and the orphaned branch would be left behind.
func (g Git) Remove(ctx context.Context, repoDir string, wt cleanup.Worktree, force bool) error {
	branchFlag := "-d"
	if force {
		branchFlag = "-D"
	}
	if _, err := os.Stat(wt.Path); err == nil {
		removeArgs := []string{"worktree", "remove", wt.Path}
		if force {
			removeArgs = []string{"worktree", "remove", "--force", wt.Path}
		}
		if _, err := run(ctx, repoDir, removeArgs...); err != nil {
			return err
		}
	} else if _, err := run(ctx, repoDir, "worktree", "prune"); err != nil {
		// The directory is already gone. Prune the stale registration so the branch
		// delete below is not refused for a branch still recorded as checked out in
		// a worktree that no longer exists.
		return err
	}
	// The worktree is gone at this point; if only the branch delete fails, say so
	// so the caller does not report the whole removal as failed.
	if _, err := run(ctx, repoDir, "branch", branchFlag, wt.Branch); err != nil {
		return fmt.Errorf("worktree removed but branch %s not deleted: %w", wt.Branch, err)
	}
	return nil
}

// parseWorktrees extracts the worktrees under baseDir from the porcelain output
// of `git worktree list`. Each record is a blank-line-separated block whose
// first line is "worktree <path>" and whose branch line is "branch
// refs/heads/<name>" (detached or bare entries have no branch and are skipped).
//
// This is not a pure function: the underDir filter resolves symlinks via
// filepath.EvalSymlinks, so it reads the filesystem. Paths that do not exist on
// disk fall back to filepath.Clean, which keeps the string-only cases working.
func parseWorktrees(out, baseDir string) []cleanup.Worktree {
	var worktrees []cleanup.Worktree
	var path, branch string
	flush := func() {
		if path != "" && branch != "" && underDir(path, baseDir) {
			worktrees = append(worktrees, cleanup.Worktree{Path: path, Branch: branch})
		}
		path, branch = "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			// A new block begins; commit the previous one first.
			flush()
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return worktrees
}

// underDir reports whether path is baseDir itself or nested inside it. Both
// sides are symlink-resolved first because baseDir comes from os.UserConfigDir
// (unresolved) while `git worktree list` reports real paths; on macOS these
// diverge (e.g. /var vs /private/var, symlinked Application Support), which
// would otherwise make filepath.Rel yield a ".." and silently exclude a genuine
// temporal-agents worktree. Cleaned paths keep trailing slashes and "."
// segments from causing false misses.
func underDir(path, baseDir string) bool {
	rel, err := filepath.Rel(resolveSymlinks(baseDir), resolveSymlinks(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveSymlinks returns p with symlinks resolved, falling back to the cleaned
// path when p does not exist (or cannot be resolved) so comparisons still work
// for paths that are not present on disk.
func resolveSymlinks(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
