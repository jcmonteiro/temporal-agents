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
	removeErr error

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
	return f.merged[branch], nil
}

func (f *fakeGit) Remove(_ context.Context, _ string, wt Worktree, force bool) error {
	if f.removeErr != nil {
		return f.removeErr
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
}

func (p *scriptedPrompter) Confirm(_ string, defaultYes bool) (bool, error) {
	p.defaultsSeen = append(p.defaultsSeen, defaultYes)
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
	// A merged branch asks exactly once, defaulting to yes.
	require.Equal(t, []bool{true}, p.defaultsSeen)
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
	// The delete prompt defaults to yes, the force prompt defaults to no.
	require.Equal(t, []bool{true, false}, p.defaultsSeen)
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
