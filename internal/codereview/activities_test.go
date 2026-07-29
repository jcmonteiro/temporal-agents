package codereview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// The CreateBranch worktree tests exercise the activity's observable behavior in
// worktree mode: whether it probes the target path, adds a worktree, and reports
// that worktree as the working directory — with a fake Git standing in for the
// gitcli adapter. planWorktree (the pure retry policy it consumes) is tested
// separately in domain_test.go.

// fakeGit is a hand-written Git stub for the worktree activity tests. Only the
// methods CreateBranch's worktree path touches carry behavior; CurrentBranch
// returns a branch for the directories present in currentBranch (and errors for
// any other directory, mirroring `git` on a path that is not yet a worktree).
type fakeGit struct {
	// currentBranch maps a directory to the branch checked out there; a missing
	// entry makes CurrentBranch return an error, as git does for a non-worktree
	// path.
	currentBranch map[string]string
	head          string
	addErr        error

	worktreeAdded bool
	addWorktreeAt string
}

func (f *fakeGit) CurrentBranch(_ context.Context, dir string) (string, error) {
	if b, ok := f.currentBranch[dir]; ok {
		return b, nil
	}
	return "", fmt.Errorf("%s is not a worktree", dir)
}

func (f *fakeGit) AddWorktree(_ context.Context, _, worktreePath, _ string) error {
	f.worktreeAdded = true
	f.addWorktreeAt = worktreePath
	return f.addErr
}

func (f *fakeGit) Head(context.Context, string) (string, error) { return f.head, nil }

// The remaining Git methods are unused by CreateBranch's worktree path.
func (f *fakeGit) CreateBranch(context.Context, string, string) error { return nil }
func (f *fakeGit) HasChanges(context.Context, string) (bool, error)   { return false, nil }
func (f *fakeGit) Stash(context.Context, string) error                { return nil }
func (f *fakeGit) StashPop(context.Context, string) error             { return nil }
func (f *fakeGit) CommitsSince(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeGit) Push(context.Context, string, string) error { return nil }

func TestCreateBranch_Worktree_CreatesWorktreeAndReportsItAsWorkDir(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// The target path is not yet a worktree, so the probe finds nothing and the
	// activity creates one.
	fg := &fakeGit{currentBranch: map[string]string{}, head: "base-sha"}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	val, err := env.ExecuteActivity(act.CreateBranch, CreateBranchRequest{
		WorkDir: "/repo", Branch: "feat/x", WorktreesDir: "/wt",
	})
	require.NoError(t, err)

	var res CreateBranchResult
	require.NoError(t, val.Get(&res))
	wantPath := filepath.Join("/wt", "feat/x")
	// The new worktree path — not the original WorkDir — becomes the working
	// directory for the rest of the flow.
	require.Equal(t, wantPath, res.WorkDir)
	require.Equal(t, "feat/x", res.Branch)
	require.Equal(t, "base-sha", res.BaseSHA)
	require.True(t, fg.worktreeAdded, "a worktree should be created")
	require.Equal(t, wantPath, fg.addWorktreeAt)
}

func TestCreateBranch_Worktree_ExistingWorktreeOnFirstAttempt_Rejected(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	wtPath := filepath.Join("/wt", "feat/x")
	// The probe finds the branch already checked out at the target path. On the
	// first attempt (the default in TestActivityEnvironment) that is treated as a
	// caller asking to develop on a pre-existing branch, and is rejected.
	fg := &fakeGit{currentBranch: map[string]string{wtPath: "feat/x"}, head: "base-sha"}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.CreateBranch, CreateBranchRequest{
		WorkDir: "/repo", Branch: "feat/x", WorktreesDir: "/wt",
	})
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "want an ApplicationError, got %T", err)
	require.Equal(t, errBranchExists, appErr.Type())
	require.False(t, fg.worktreeAdded, "no worktree should be added when rejecting")
}
