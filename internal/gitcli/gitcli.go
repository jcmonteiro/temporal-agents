// Package gitcli is a driven adapter over the `git` command line. It implements
// the codereview.Git port and the cleanup.Git port (the latter in cleanup.go).
package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// CreateBranch creates and checks out a new branch in dir. When startPoint is
// non-empty the branch is created at that commit-ish; an empty startPoint lets
// git default to dir's current HEAD.
func (g Git) CreateBranch(ctx context.Context, dir, branch, startPoint string) error {
	args := []string{"checkout", "-b", branch}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	_, err := run(ctx, dir, args...)
	return classifyExists(err)
}

// AddWorktree creates a new worktree at worktreePath checked out on a new
// branch created off the repository in dir. When startPoint is non-empty the
// branch is created at that commit-ish; an empty startPoint lets git default to
// the repository's current HEAD.
func (g Git) AddWorktree(ctx context.Context, dir, worktreePath, branch, startPoint string) error {
	args := []string{"worktree", "add", worktreePath, "-b", branch}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	_, err := run(ctx, dir, args...)
	return classifyExists(err)
}

// AddDisposableWorktree creates a throwaway detached-HEAD worktree of the repo
// in dir at a fresh temporary path and returns that path. It lets a read-only
// step (e.g. fleet planning) run against an isolated copy of the repository so
// the step cannot touch the user's working tree, branch, or index; callers pair
// it with RemoveWorktree to discard the copy afterward.
func (g Git) AddDisposableWorktree(ctx context.Context, dir string) (string, error) {
	// Reserve a unique path without leaving a directory behind: `git worktree add`
	// wants to create the directory itself and refuses a pre-existing non-empty
	// one. MkdirTemp is the simplest race-free way to reserve a name, so create it
	// then immediately remove the empty directory and hand the bare path to git.
	path, err := os.MkdirTemp("", "fleet-plan-*")
	if err != nil {
		return "", fmt.Errorf("reserve worktree path: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("clear worktree path: %w", err)
	}
	if _, err := run(ctx, dir, "worktree", "add", "--detach", path); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveWorktree discards a worktree previously created at path, including any
// uncommitted changes in it (--force), so a disposable sandbox leaves nothing
// behind.
func (g Git) RemoveWorktree(ctx context.Context, dir, path string) error {
	_, err := run(ctx, dir, "worktree", "remove", "--force", path)
	return err
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

// Fingerprint returns a value that changes whenever any content in dir's
// worktree or index changes — not merely whether the repository is dirty.
// It combines the HEAD commit SHA with two independent tree object hashes: the
// real index tree (the staging area) and a synthesized tree of the complete
// worktree (tracked, staged, untracked, and ignored content). Because it
// captures content rather than a dirty/clean boolean, it detects a mutation to
// a file that was already modified before the fingerprint was taken, which a
// dirty-flag comparison cannot.
//
// Hashing the real index separately is what makes staged-only changes visible:
// if a file has an unstaged edit and something runs `git add` on it, HEAD and
// worktree bytes are unchanged, so a worktree-only fingerprint would be
// identical before and after even though the user's index was mutated. The
// real index tree captures that staging. It never disturbs the user's real
// index: `git write-tree` only reads the index, and the worktree tree is
// synthesized in a throwaway index file via GIT_INDEX_FILE.
//
// Crucially it also writes no objects into the source repository. Both
// write-tree calls (and the worktree `git add`) would otherwise persist new
// blob and tree objects — including ignored secrets like .env and large
// ignored trees pulled in by --force — under the source repo's .git/objects,
// contradicting the read-only guarantee even for planning that never commits.
// GIT_OBJECT_DIRECTORY redirects every new object into a disposable directory
// that is removed when the fingerprint returns, while
// GIT_ALTERNATE_OBJECT_DIRECTORIES lets git still read the repository's
// existing objects (HEAD's tree, tracked blobs) to synthesize the trees.
func (g Git) Fingerprint(ctx context.Context, dir string) (string, error) {
	head, err := g.Head(ctx, dir)
	if err != nil {
		return "", err
	}
	// Locate the source repo's real object database so it can be exposed as a
	// read-only alternate: new objects go to the disposable dir below, existing
	// ones are read from here. Resolve to an absolute path so it does not depend
	// on any git process's working directory.
	realObjects, err := run(ctx, dir, "rev-parse", "--git-path", "objects")
	if err != nil {
		return "", err
	}
	realObjects = strings.TrimSpace(realObjects)
	if !filepath.IsAbs(realObjects) {
		realObjects = filepath.Join(dir, realObjects)
	}
	// Disposable object directory: every object git writes while fingerprinting
	// lands here and is discarded, so the source repo's .git/objects is never
	// written to. The alternate keeps existing objects readable.
	objDir, err := os.MkdirTemp("", "fleet-fp-obj-*")
	if err != nil {
		return "", fmt.Errorf("reserve fingerprint object dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(objDir) }()
	objEnv := []string{
		"GIT_OBJECT_DIRECTORY=" + objDir,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + realObjects,
	}
	// Hash the real index tree so staged-only changes are covered. write-tree
	// reads the current index and writes the corresponding tree object (now into
	// the disposable object dir) without mutating the staged content, so it
	// leaves the user's index intact.
	indexTree, err := runEnv(ctx, dir, objEnv, "write-tree")
	if err != nil {
		return "", err
	}
	// Reserve a throwaway index path and seed it from HEAD before staging the
	// worktree, all against GIT_INDEX_FILE so the user's real index is never
	// touched. `git add --force -A` stages everything the plain form would
	// (tracked, staged, unstaged, and untracked changes, deletions included) and,
	// crucially, ignored files too: plain `git add -A` skips ignored paths, so an
	// escaped command could overwrite a common ignored file such as .env while
	// HEAD, the real index, and the worktree tree all stayed identical, letting
	// the tripwire miss the mutation and contradicting the read-only guarantee.
	// --force folds those ignored files into the synthesized tree so a change to
	// one moves the fingerprint. read-tree HEAD seeds the index first so a
	// committed-but-now-ignored file stays fingerprinted as a further guard.
	idx, err := os.MkdirTemp("", "fleet-fp-*")
	if err != nil {
		return "", fmt.Errorf("reserve fingerprint index dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(idx) }()
	env := append([]string{"GIT_INDEX_FILE=" + idx + "/index"}, objEnv...)
	if _, err := runEnv(ctx, dir, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := runEnv(ctx, dir, env, "add", "--force", "-A"); err != nil {
		return "", err
	}
	worktreeTree, err := runEnv(ctx, dir, env, "write-tree")
	if err != nil {
		return "", err
	}
	return head + ":" + strings.TrimSpace(indexTree) + ":" + strings.TrimSpace(worktreeTree), nil
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

// MergeBranch merges branch into the branch currently checked out in dir,
// creating a merge commit (or fast-forwarding) with a default message
// (--no-edit). It is used to seed a dependent's branch with the work of the
// branches it depends on and, later, to keep it current as those branches move.
// A merge conflict leaves the merge in progress and returns an error; the caller
// is responsible for aborting (see AbortMerge) so the branch is not left with
// conflict markers.
func (g Git) MergeBranch(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "merge", "--no-edit", branch)
	return err
}

// AbortMerge aborts a merge left in progress in dir (e.g. after a conflict),
// restoring the branch to its pre-merge state so no conflict markers are
// committed or pushed.
func (g Git) AbortMerge(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "merge", "--abort")
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
	return runEnv(ctx, dir, nil, args...)
}

// runEnv is run with extra environment variables appended (after LC_ALL=C), so
// callers can point git at a throwaway index via GIT_INDEX_FILE without leaking
// that setting into the process environment.
func runEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Pin the locale to C so git emits stable, English stderr. classifyExists
	// matches the "already exists" substring, which would silently stop matching
	// under a localized LANG/LC_ALL and degrade the fast-fail path to opaque retry.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
