package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/codereview"
)

// TestClassifyExists pins the load-bearing "already exists" substring match:
// git phrases both a colliding branch and a colliding worktree path with that
// substring, and only those must be wrapped as the non-retryable sentinel.
// initRepo creates a real git repository with one committed file and returns its
// path. Identity and template config are pinned so the test does not depend on
// (or read) the developer's global git configuration.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("one\n"), 0o644))
	git("add", "file.txt")
	git("commit", "-m", "init")
	return dir
}

// TestFingerprintDetectsStagedOnlyChange pins the tripwire's staged-only
// coverage: staging an already-unstaged edit leaves HEAD and worktree bytes
// unchanged, so only a fingerprint that hashes the real index tree separately
// changes. A worktree-only fingerprint would be identical before and after.
func TestFingerprintDetectsStagedOnlyChange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	// Make an unstaged edit, then fingerprint: the worktree differs from HEAD but
	// the index still matches HEAD.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("two\n"), 0o644))
	before, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	// Stage that same edit. HEAD is unchanged and the worktree bytes are
	// identical; only the index moved.
	stage := exec.Command("git", "-C", dir, "add", "file.txt")
	stage.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	require.NoError(t, stage.Run())

	after, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	require.NotEqual(t, before, after, "staging an edit must change the fingerprint")
}

// TestFingerprintDetectsEditToTrackedIgnoredFile pins the HEAD-seeded index:
// a file that is committed but now matched by .gitignore is skipped by
// `git add -A`, so a worktree index that started empty would drop it and an
// unstaged edit to it would leave HEAD, the real index, and the worktree tree
// all unchanged. Seeding the throwaway index from HEAD keeps the file
// fingerprinted so the edit is detected.
func TestFingerprintDetectsEditToTrackedIgnoredFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// file.txt is already committed; now start ignoring it, then commit the
	// .gitignore so the worktree is clean and file.txt is tracked-but-ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("file.txt\n"), 0o644))
	git("add", ".gitignore")
	git("commit", "-m", "ignore file.txt")

	before, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	// Edit the tracked-but-ignored file without staging it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("two\n"), 0o644))

	after, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	require.NotEqual(t, before, after, "editing a tracked-but-ignored file must change the fingerprint")
}

// TestFingerprintDetectsEditToIgnoredUntrackedFile pins that ignored files are
// covered: an ignored, untracked file such as .env is never committed and is
// skipped by plain `git add -A`, so without --force overwriting it would leave
// HEAD, the real index, and the worktree tree all unchanged and the read-only
// tripwire would miss the mutation. Staging with --force folds it into the
// synthesized tree so the fingerprint moves.
func TestFingerprintDetectsEditToIgnoredUntrackedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// Ignore .env, then create it as an untracked, ignored file (never committed,
	// as a secret file would be).
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644))
	git("add", ".gitignore")
	git("commit", "-m", "ignore .env")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=one\n"), 0o644))

	before, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	// Overwrite the ignored file, as an escaped command could.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=two\n"), 0o644))

	after, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)

	require.NotEqual(t, before, after, "overwriting an ignored untracked file must change the fingerprint")
}

// TestFingerprintWritesNoObjectsToSourceRepo pins the read-only guarantee at
// the object-store level: synthesizing the worktree tree stages ignored files
// with `git add --force`, which would otherwise persist their blobs (a secret
// like .env, or a large ignored tree) under the source repo's .git/objects
// permanently. Redirecting new objects to a disposable directory must leave the
// source repo's object database byte-for-byte unchanged.
func TestFingerprintWritesNoObjectsToSourceRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	// An ignored, untracked file whose blob would be stored by `git add --force`.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=one\n"), 0o644))

	objectsDir := filepath.Join(dir, ".git", "objects")
	listObjects := func() []string {
		t.Helper()
		var files []string
		err := filepath.Walk(objectsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		require.NoError(t, err)
		return files
	}

	before := listObjects()
	_, err := g.Fingerprint(ctx, dir)
	require.NoError(t, err)
	after := listObjects()

	require.ElementsMatch(t, before, after, "fingerprinting must not write objects into the source repository")
}

// TestAddDisposableCloneIsolatesSourceRepo pins the read-only boundary at the
// git-storage level: the sandbox is a standalone clone with its own .git, so
// the ref- and object-writing commands the reviewer flagged (git branch, git
// tag, a commit) stay inside the throwaway clone and leave the source repo's
// refs and object database untouched. A shared linked worktree would let them
// escape.
func TestAddDisposableCloneIsolatesSourceRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	objectsDir := filepath.Join(dir, ".git", "objects")
	listObjects := func() []string {
		t.Helper()
		var files []string
		err := filepath.Walk(objectsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		require.NoError(t, err)
		return files
	}
	before := listObjects()

	sandbox, err := g.AddDisposableClone(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.RemoveDisposableClone(ctx, sandbox) })

	// Perform exactly the ref- and object-mutating operations that would persist
	// in a shared .git: a branch, a tag, and a commit, all inside the sandbox.
	sb := gitRunner(t, sandbox)
	sb("config", "user.name", "t")
	sb("config", "user.email", "t@t")
	sb("branch", "sandbox-branch")
	sb("tag", "sandbox-tag")
	require.NoError(t, os.WriteFile(filepath.Join(sandbox, "sandbox.txt"), []byte("x\n"), 0o644))
	sb("add", "sandbox.txt")
	sb("commit", "-m", "sandbox commit")

	// The source repo's object database is byte-for-byte unchanged.
	require.ElementsMatch(t, before, listObjects(), "sandbox writes must not reach the source object database")

	// And the source repo gained none of the sandbox's refs.
	refs, err := run(ctx, dir, "for-each-ref", "--format=%(refname)")
	require.NoError(t, err)
	require.NotContains(t, refs, "sandbox-branch", "sandbox branch must not appear in the source repo")
	require.NotContains(t, refs, "sandbox-tag", "sandbox tag must not appear in the source repo")
}

// gitRunner returns a helper that runs pinned-identity git commands in dir and
// fails the test on error. Identity is set both via env and (by the caller)
// local config so merge commits succeed without reading the developer's global
// git configuration.
func gitRunner(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestMergeBranch_CombinesDivergentBranches pins the seeding behavior: merging a
// branch that diverged from the base folds its commits into the checked-out
// branch, so a dependent's branch ends up carrying the work of the branch it
// depends on.
func TestMergeBranch_CombinesDivergentBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	git := gitRunner(t, dir)
	// Pin identity in local config so the merge commit does not need global config.
	git("config", "user.name", "t")
	git("config", "user.email", "t@t")

	// A dependency branch that adds its own file, then a node branch off the same
	// base that adds a different file: the two diverge without conflicting.
	git("checkout", "-b", "dep")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dep.txt"), []byte("dep\n"), 0o644))
	git("add", "dep.txt")
	git("commit", "-m", "dep work")
	git("checkout", "main")
	git("checkout", "-b", "node")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node.txt"), []byte("node\n"), 0o644))
	git("add", "node.txt")
	git("commit", "-m", "node work")

	require.NoError(t, New().MergeBranch(context.Background(), dir, "dep"))

	// The merge brought the dependency's file in alongside the node's own.
	require.FileExists(t, filepath.Join(dir, "dep.txt"))
	require.FileExists(t, filepath.Join(dir, "node.txt"))
}

// TestMergeBranch_ConflictErrorsAndAbortRestores pins the conflict contract: a
// conflicting merge returns an error and leaves the merge in progress, and
// AbortMerge restores the branch to a clean pre-merge state so no conflict
// markers survive.
func TestMergeBranch_ConflictErrorsAndAbortRestores(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()
	git := gitRunner(t, dir)
	git("config", "user.name", "t")
	git("config", "user.email", "t@t")

	// Two branches edit the same line differently: merging them conflicts.
	git("checkout", "-b", "dep")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("dep\n"), 0o644))
	git("add", "file.txt")
	git("commit", "-m", "dep edit")
	git("checkout", "main")
	git("checkout", "-b", "node")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("node\n"), 0o644))
	git("add", "file.txt")
	git("commit", "-m", "node edit")

	require.Error(t, g.MergeBranch(ctx, dir, "dep"), "a conflicting merge must error")

	// The merge stopped on conflict: unmerged paths are present, which lets a
	// caller tell this apart from other git failures.
	conflicted, err := g.HasConflicts(ctx, dir)
	require.NoError(t, err)
	require.True(t, conflicted, "a conflicting merge must leave unmerged paths")

	require.NoError(t, g.AbortMerge(ctx, dir))
	conflicted, err = g.HasConflicts(ctx, dir)
	require.NoError(t, err)
	require.False(t, conflicted, "abort must clear the unmerged paths")
	dirty, err := g.HasChanges(ctx, dir)
	require.NoError(t, err)
	require.False(t, dirty, "abort must leave a clean working tree")
	got, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "node\n", string(got), "abort must restore the node branch's content")
}

// TestAbortMerge_NoMergeInProgressIsNoOp pins abort idempotency: with no merge
// underway (a prior attempt already aborted, or none ever started) AbortMerge
// must succeed rather than fail with "no merge to abort", so it is safe to
// re-run under activity retries.
func TestAbortMerge_NoMergeInProgressIsNoOp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	// A clean repository has no merge in progress; abort must be a no-op.
	require.NoError(t, g.AbortMerge(ctx, dir))

	// After a real abort, a second abort (as a retry would issue) must also
	// succeed rather than resurface as a failure.
	git := gitRunner(t, dir)
	git("config", "user.name", "t")
	git("config", "user.email", "t@t")
	git("checkout", "-b", "dep")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("dep\n"), 0o644))
	git("add", "file.txt")
	git("commit", "-m", "dep edit")
	git("checkout", "main")
	git("checkout", "-b", "node")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("node\n"), 0o644))
	git("add", "file.txt")
	git("commit", "-m", "node edit")
	require.Error(t, g.MergeBranch(ctx, dir, "dep"), "a conflicting merge must error")

	require.NoError(t, g.AbortMerge(ctx, dir))
	require.NoError(t, g.AbortMerge(ctx, dir), "a second abort must be a no-op")
}

// TestAddDisposableClone_DetachesOrigin pins the source-repo detachment: the
// sandbox must carry no `origin` remote pointing back at the source, so a
// `git push origin ...` from the read-only sandbox has no configured path to
// create refs or objects in the source repository.
func TestAddDisposableClone_DetachesOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()

	sandbox, err := g.AddDisposableClone(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.RemoveDisposableClone(ctx, sandbox) })

	remotes, err := run(ctx, sandbox, "remote")
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(remotes), "the sandbox must have no remote back to the source")
}

// TestIsAncestor pins the ancestry probe: it reports true for a commit reachable
// from HEAD and false for an unrelated branch tip, without erroring on the
// negative case (git exits 1, not a genuine failure).
func TestIsAncestor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepo(t)
	g := New()
	ctx := context.Background()
	git := gitRunner(t, dir)
	git("config", "user.name", "t")
	git("config", "user.email", "t@t")

	base, err := g.Head(ctx, dir)
	require.NoError(t, err)

	// A branch that diverges from base: its tip is not reachable from main's HEAD.
	git("checkout", "-b", "side")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side\n"), 0o644))
	git("add", "side.txt")
	git("commit", "-m", "side work")
	git("checkout", "main")

	// The base commit is an ancestor of main's HEAD.
	ancestor, err := g.IsAncestor(ctx, dir, base, "HEAD")
	require.NoError(t, err)
	require.True(t, ancestor, "base must be an ancestor of HEAD")

	// The diverged branch tip is not reachable from main's HEAD.
	ancestor, err = g.IsAncestor(ctx, dir, "side", "HEAD")
	require.NoError(t, err)
	require.False(t, ancestor, "an unmerged branch tip must not be an ancestor of HEAD")
}

func TestClassifyExists(t *testing.T) {
	tests := []struct {
		name         string
		in           error
		wantSentinel bool
	}{
		{"nil", nil, false},
		{"branch exists", errors.New("git checkout -b: exit 128: fatal: a branch named 'x' already exists"), true},
		{"path exists", errors.New("git worktree add: exit 128: fatal: 'wt/x' already exists"), true},
		{"other failure", errors.New("git checkout -b: exit 128: fatal: not a git repository"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.Is(classifyExists(tt.in), codereview.ErrBranchOrWorktreeExists)
			if got != tt.wantSentinel {
				t.Fatalf("errors.Is = %v, want %v", got, tt.wantSentinel)
			}
		})
	}
}
