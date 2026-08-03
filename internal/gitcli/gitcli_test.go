package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
