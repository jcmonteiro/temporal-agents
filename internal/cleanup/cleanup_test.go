package cleanup

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeGit records the removals it is asked to perform and answers List/Merged
// from canned data.
type fakeGit struct {
	worktrees []Worktree
	merged    map[string]bool
	listErr   error
	mergedErr error
	removeErr error
	// worktreeDirtyErr fails a removal only when force is false, modelling a
	// merged branch whose worktree still has local changes.
	worktreeDirtyErr error

	removed []removeCall
}

type removeCall struct {
	branch string
	force  bool
}

func (f *fakeGit) List(context.Context, string, string) ([]Worktree, error) {
	return f.worktrees, f.listErr
}

func (f *fakeGit) Merged(_ context.Context, _, branch string) (bool, error) {
	if f.mergedErr != nil {
		return false, f.mergedErr
	}
	return f.merged[branch], nil
}

func (f *fakeGit) Remove(_ context.Context, _ string, wt Worktree, force bool) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.worktreeDirtyErr != nil && !force {
		return f.worktreeDirtyErr
	}
	f.removed = append(f.removed, removeCall{branch: wt.Branch, force: force})
	return nil
}

// scriptedPrompter answers each question in turn from a queue, recording the
// defaultYes it was asked with.
type scriptedPrompter struct {
	answers      []bool
	i            int
	defaultsSeen []bool
	err          error
}

func (p *scriptedPrompter) Confirm(_ string, defaultYes bool) (bool, error) {
	p.defaultsSeen = append(p.defaultsSeen, defaultYes)
	if p.err != nil {
		return false, p.err
	}
	ans := p.answers[p.i]
	p.i++
	return ans, nil
}

func newCleaner(g Git, p Prompter) *Cleaner {
	return &Cleaner{Git: g, Prompt: p, Out: io.Discard}
}

func TestRun_NoWorktrees_RemovesNothing(t *testing.T) {
	g := &fakeGit{}
	p := &scriptedPrompter{}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Zero(t, removed)
	require.Empty(t, g.removed)
	require.Zero(t, p.i, "should not prompt when there are no worktrees")
}

func TestRun_MergedBranch_ConfirmedYes_RemovesWithoutForce(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:    map[string]bool{"feat/x": true},
	}
	p := &scriptedPrompter{answers: []bool{true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Equal(t, []removeCall{{branch: "feat/x", force: false}}, g.removed)
	// A merged branch asks exactly once, defaulting to no.
	require.Equal(t, []bool{false}, p.defaultsSeen)
}

func TestRun_DeclinedAtFirstPrompt_SkipsAndDoesNotCheckMerge(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		// No merge data on purpose: Merged must not decide the outcome here.
	}
	p := &scriptedPrompter{answers: []bool{false}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Zero(t, removed)
	require.Empty(t, g.removed)
	require.Equal(t, 1, p.i, "declining the first prompt should stop before the force prompt")
}

func TestRun_UnmergedBranch_ForceConfirmed_RemovesWithForce(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:    map[string]bool{"feat/x": false},
	}
	// First prompt (delete?) yes, second prompt (force?) yes.
	p := &scriptedPrompter{answers: []bool{true, true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Equal(t, []removeCall{{branch: "feat/x", force: true}}, g.removed)
	// Both prompts default to no: the delete prompt and the force prompt.
	require.Equal(t, []bool{false, false}, p.defaultsSeen)
}

func TestRun_UnmergedBranch_ForceDeclined_SkipsWithoutRemoving(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:    map[string]bool{"feat/x": false},
	}
	// Delete? yes, force? no.
	p := &scriptedPrompter{answers: []bool{true, false}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Zero(t, removed)
	require.Empty(t, g.removed)
}

func TestRun_MultipleWorktrees_HandlesEachIndependently(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{
			{Path: "/wt/a", Branch: "feat/a"}, // merged, accepted -> removed
			{Path: "/wt/b", Branch: "feat/b"}, // declined -> skipped
			{Path: "/wt/c", Branch: "feat/c"}, // unmerged, forced -> removed
		},
		merged: map[string]bool{"feat/a": true, "feat/c": false},
	}
	// a: yes; b: no; c: yes then force yes.
	p := &scriptedPrompter{answers: []bool{true, false, true, true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Equal(t, 2, removed)
	require.Equal(t, []removeCall{
		{branch: "feat/a", force: false},
		{branch: "feat/c", force: true},
	}, g.removed)
}

func TestRun_ListError_IsReported(t *testing.T) {
	g := &fakeGit{listErr: errors.New("boom")}
	p := &scriptedPrompter{}

	_, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.ErrorContains(t, err, "list worktrees")
}

func TestRun_MergedError_IsWrappedAndReported(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		mergedErr: errors.New("boom"),
	}
	p := &scriptedPrompter{answers: []bool{true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.Zero(t, removed)
	require.Empty(t, g.removed)
	require.ErrorContains(t, err, "check merge status of feat/x")
}

func TestRun_RemoveError_IsWrappedAndReported(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:    map[string]bool{"feat/x": true},
		removeErr: errors.New("boom"),
	}
	// Delete? yes; the force retry offered after the failure is also yes, and the
	// forced removal fails too, so the wrapped error still surfaces.
	p := &scriptedPrompter{answers: []bool{true, true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.Zero(t, removed)
	require.ErrorContains(t, err, "remove worktree /wt/feat-x")
}

func TestRun_MergedWorktreeDirty_ForceRetryConfirmed_RemovesWithForce(t *testing.T) {
	g := &fakeGit{
		worktrees:        []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:           map[string]bool{"feat/x": true},
		worktreeDirtyErr: errors.New("worktree contains modified files"),
	}
	// Delete? yes; non-force removal fails, force retry? yes.
	p := &scriptedPrompter{answers: []bool{true, true}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Equal(t, []removeCall{{branch: "feat/x", force: true}}, g.removed)
	// Delete prompt and force-retry prompt both default to no.
	require.Equal(t, []bool{false, false}, p.defaultsSeen)
}

func TestRun_MergedWorktreeDirty_ForceRetryDeclined_SkipsWithoutRemoving(t *testing.T) {
	g := &fakeGit{
		worktrees:        []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}},
		merged:           map[string]bool{"feat/x": true},
		worktreeDirtyErr: errors.New("worktree contains modified files"),
	}
	// Delete? yes; force retry? no.
	p := &scriptedPrompter{answers: []bool{true, false}}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.NoError(t, err)
	require.Zero(t, removed)
	require.Empty(t, g.removed)
}

func TestRun_PromptError_IsReported(t *testing.T) {
	g := &fakeGit{worktrees: []Worktree{{Path: "/wt/feat-x", Branch: "feat/x"}}}
	p := &scriptedPrompter{err: errors.New("boom")}

	removed, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.Zero(t, removed)
	require.Empty(t, g.removed)
	require.Error(t, err)
}

func TestRun_ErrorOnOneWorktree_ContinuesWithTheRest(t *testing.T) {
	g := &fakeGit{
		worktrees: []Worktree{
			{Path: "/wt/a", Branch: "feat/a"}, // remove fails
			{Path: "/wt/b", Branch: "feat/b"}, // still processed
		},
		merged:    map[string]bool{"feat/a": true, "feat/b": true},
		removeErr: errors.New("boom"),
	}
	// Each worktree is confirmed for deletion (answer 1) and its post-failure
	// force retry is also confirmed (answer 2), so both worktrees consume two
	// prompts before the forced removal fails again.
	p := &scriptedPrompter{answers: []bool{true, true, true, true}}

	_, err := newCleaner(g, p).Run(context.Background(), "/repo", "/wt")

	require.Error(t, err)
	// The loop reached the second worktree despite the first failing.
	require.Equal(t, 4, p.i)
}
