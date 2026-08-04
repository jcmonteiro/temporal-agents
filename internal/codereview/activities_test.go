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
	createErr     error

	worktreeAdded bool
	addWorktreeAt string
	branchCreated bool

	// Seeding controls: MergeBranch records branches and returns mergeErr;
	// hasConflicts/dirty drive the conflict and clean-tree probes; AbortMerge sets
	// aborted and returns abortErr.
	mergeErr       error
	mergedBranches []string
	hasConflicts   bool
	dirty          bool
	aborted        bool
	abortErr       error
}

func (f *fakeGit) CurrentBranch(_ context.Context, dir string) (string, error) {
	if b, ok := f.currentBranch[dir]; ok {
		return b, nil
	}
	return "", fmt.Errorf("%s is not a worktree", dir)
}

func (f *fakeGit) AddWorktree(_ context.Context, _, worktreePath, _, _ string) error {
	f.worktreeAdded = true
	f.addWorktreeAt = worktreePath
	return f.addErr
}

func (f *fakeGit) Head(context.Context, string) (string, error) { return f.head, nil }

func (f *fakeGit) CreateBranch(context.Context, string, string, string) error {
	f.branchCreated = true
	return f.createErr
}

// The remaining Git methods are unused by CreateBranch's paths under test.
func (f *fakeGit) HasChanges(context.Context, string) (bool, error) { return f.dirty, nil }
func (f *fakeGit) Stash(context.Context, string) error              { return nil }
func (f *fakeGit) StashPop(context.Context, string) error           { return nil }
func (f *fakeGit) CommitsSince(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeGit) Push(context.Context, string, string) error { return nil }
func (f *fakeGit) MergeBranch(_ context.Context, _, branch string) error {
	f.mergedBranches = append(f.mergedBranches, branch)
	return f.mergeErr
}
func (f *fakeGit) HasConflicts(context.Context, string) (bool, error) { return f.hasConflicts, nil }
func (f *fakeGit) AbortMerge(context.Context, string) error {
	f.aborted = true
	return f.abortErr
}

// fakeAgent is a hand-written Agent stub for the conflict-resolution activity
// tests. It records the last prompt and returns canned output/tokens/err.
type fakeAgent struct {
	output     string
	tokens     int
	err        error
	lastPrompt string
}

func (f *fakeAgent) Run(_ context.Context, prompt, _ string) (string, int, error) {
	f.lastPrompt = prompt
	return f.output, f.tokens, f.err
}

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

func TestCreateBranch_Worktree_AddReportsAlreadyExists_NonRetryable(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// The probe finds nothing at the target path (e.g. a stale directory or a
	// branch ref with no worktree — states the probe cannot detect), so the
	// activity attempts to create the worktree and git reports it already exists.
	// That is permanent for an explicit branch, so the activity must fail
	// non-retryably rather than burning all its attempts on the same error.
	fg := &fakeGit{
		currentBranch: map[string]string{},
		head:          "base-sha",
		addErr:        fmt.Errorf("git worktree add: %w: fatal: already exists", ErrBranchOrWorktreeExists),
	}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.CreateBranch, CreateBranchRequest{
		WorkDir: "/repo", Branch: "feat/x", WorktreesDir: "/wt",
	})
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "want an ApplicationError, got %T", err)
	require.True(t, appErr.NonRetryable(), "an already-exists failure must be non-retryable")
	require.Equal(t, errBranchExists, appErr.Type())
}

func TestCreateBranch_InPlace_RecoveredGeneratedAlias_AdoptsWithoutCreating(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// A prior attempt generated this alias, persisted it via heartbeat details, and
	// created+checked out the branch before failing (e.g. the later Head call
	// failed). The retry recovers the same alias and finds it already checked out.
	const alias = "flaming-duck-2026-jul-29"
	env.SetHeartbeatDetails(alias)
	fg := &fakeGit{currentBranch: map[string]string{"/repo": alias}, head: "base-sha"}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	// Branch is empty (generate one for me); the persisted alias is recovered.
	val, err := env.ExecuteActivity(act.CreateBranch, CreateBranchRequest{WorkDir: "/repo"})
	require.NoError(t, err)

	var res CreateBranchResult
	require.NoError(t, val.Get(&res))
	// The recovered alias is reused rather than a new one being generated, and no
	// second branch is created — so the retry does not orphan the first branch.
	require.Equal(t, alias, res.Branch)
	require.Equal(t, "/repo", res.WorkDir)
	require.Equal(t, "base-sha", res.BaseSHA)
	require.False(t, fg.branchCreated, "a recovered alias must adopt, not create a second branch")
}

func TestCreateBranch_InPlace_ExplicitBranchAlreadyExists_NonRetryable(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// In-place mode (no WorktreesDir). The current branch differs from the
	// requested one and the tree is clean, so the activity attempts to create the
	// branch; git reports it already exists (a branch ref that is not checked
	// out). Retrying cannot fix an explicit name, so the failure is non-retryable.
	fg := &fakeGit{
		currentBranch: map[string]string{"/repo": "main"},
		head:          "base-sha",
		createErr:     fmt.Errorf("git checkout -b: %w: fatal: a branch named 'feat/x' already exists", ErrBranchOrWorktreeExists),
	}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.CreateBranch, CreateBranchRequest{
		WorkDir: "/repo", Branch: "feat/x",
	})
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "want an ApplicationError, got %T", err)
	require.True(t, appErr.NonRetryable(), "an already-exists failure must be non-retryable")
	require.Equal(t, errBranchExists, appErr.Type())
}

func TestMergeDependency_CleanMerge_ReportsHead(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	fg := &fakeGit{head: "merged-head"}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	val, err := env.ExecuteActivity(act.MergeDependency, MergeDependencyRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.NoError(t, err)

	var res MergeDependencyResult
	require.NoError(t, val.Get(&res))
	// A clean merge reports the resulting HEAD and no conflict.
	require.False(t, res.Conflicted)
	require.Equal(t, "merged-head", res.Head)
	require.Equal(t, []string{"dep-a"}, fg.mergedBranches)
}

func TestMergeDependency_Conflict_ReportsConflictedNotError(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// The merge fails and leaves unmerged paths: the activity reports Conflicted
	// rather than erroring, so the orchestrator can attempt resolution.
	fg := &fakeGit{mergeErr: errors.New("merge conflict"), hasConflicts: true}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	val, err := env.ExecuteActivity(act.MergeDependency, MergeDependencyRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.NoError(t, err)

	var res MergeDependencyResult
	require.NoError(t, val.Get(&res))
	require.True(t, res.Conflicted)
}

func TestMergeDependency_MergeFailsWithoutConflict_IsError(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// A merge failure with no unmerged paths is a genuine git failure, surfaced as
	// an activity error rather than a resolvable conflict.
	fg := &fakeGit{mergeErr: errors.New("fatal: not a git repository"), hasConflicts: false}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.MergeDependency, MergeDependencyRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.Error(t, err)
}

func TestResolveMergeConflict_Resolved_ReportsHeadAndTokens(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// After the agent runs, no conflicts remain and the tree is clean (the merge
	// was committed): the resolution succeeds and reports the new HEAD and usage.
	fg := &fakeGit{head: "resolved-head", hasConflicts: false, dirty: false}
	ag := &fakeAgent{tokens: 321}
	act := &Activities{Git: fg, Agent: ag}
	env.RegisterActivity(act)

	val, err := env.ExecuteActivity(act.ResolveMergeConflict, ResolveMergeConflictRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.NoError(t, err)

	var res ResolveMergeConflictResult
	require.NoError(t, val.Get(&res))
	require.Equal(t, "resolved-head", res.Head)
	require.Equal(t, 321, res.Tokens)
	require.Contains(t, ag.lastPrompt, "dep-a")
}

func TestResolveMergeConflict_StillConflicted_IsError(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// The agent ran but conflicts remain: the activity errors so its bounded retry
	// policy re-runs the agent (and the orchestrator eventually aborts).
	fg := &fakeGit{hasConflicts: true}
	ag := &fakeAgent{}
	act := &Activities{Git: fg, Agent: ag}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.ResolveMergeConflict, ResolveMergeConflictRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.Error(t, err)
}

func TestResolveMergeConflict_ResolvedButNotCommitted_IsError(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	// Conflicts are gone but the tree is dirty: the merge was not committed, so
	// the activity errors rather than reporting a half-finished merge as done.
	fg := &fakeGit{hasConflicts: false, dirty: true}
	ag := &fakeAgent{}
	act := &Activities{Git: fg, Agent: ag}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.ResolveMergeConflict, ResolveMergeConflictRequest{
		WorkDir: "/wt/node", Branch: "dep-a",
	})
	require.Error(t, err)
}

func TestAbortMerge_DelegatesToGit(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	fg := &fakeGit{}
	act := &Activities{Git: fg}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.AbortMerge, AbortMergeRequest{WorkDir: "/wt/node"})
	require.NoError(t, err)
	require.True(t, fg.aborted, "the activity must abort the in-progress merge")
}
