package gitcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The probe is exercised against real repositories, because what it is asserting is
// what git answers — a fake git would only restate this file's own assumptions.

// requireGit skips a test on a host without the binary the adapter drives.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// resolved returns path with symlinks resolved, which is what git reports. macOS
// hands out temporary directories under a symlinked /var, so the raw path and git's
// answer differ there and only for that reason.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return real
}

func TestWorkInAnOrdinaryCheckoutRunsInTheRepositoryAndNowhereElse(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	facts, err := New().Probe(context.Background(), repo)

	require.NoError(t, err)
	require.Equal(t, resolved(t, repo), facts.Directory)
	require.Empty(t, facts.Repository,
		"a checkout is one place: a second one would be the same place drawn twice")
}

func TestWorkInASubdirectoryRunsInTheWorkingTreeGitNames(t *testing.T) {
	// The place is the working tree, not the directory the operator happened to
	// invoke from: git states the relation, so it is a probed fact and not a prefix
	// comparison.
	requireGit(t)
	repo := initRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(repo, "internal"), 0o755))

	facts, err := New().Probe(context.Background(), filepath.Join(repo, "internal"))

	require.NoError(t, err)
	require.Equal(t, resolved(t, repo), facts.Directory)
	require.Empty(t, facts.Repository)
}

func TestWorkInAWorktreeReportsTheRepositoryItWasCreatedFrom(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	// Deliberately outside the repository, which is where git puts a worktree by
	// default and why path containment could never establish this edge.
	worktree := filepath.Join(t.TempDir(), "feature")
	require.NoError(t, New().AddWorktree(context.Background(), repo, worktree, "feature", ""))

	facts, err := New().Probe(context.Background(), worktree)

	require.NoError(t, err)
	require.Equal(t, resolved(t, worktree), facts.Directory)
	require.Equal(t, resolved(t, repo), facts.Repository)
}

func TestADirectoryThatIsInNoRepositoryHasNoPlaceToReport(t *testing.T) {
	// The probe answers with facts or with a failure. An empty answer dressed up as
	// success would leave the caller unable to tell "nowhere" from "did not ask".
	requireGit(t)

	_, err := New().Probe(context.Background(), t.TempDir())

	require.Error(t, err)
}
