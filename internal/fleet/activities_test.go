package fleet

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeGit is a test double for the Git port. It records the sandbox lifecycle
// and lets a test simulate the source repository changing across a planning run.
type fakeGit struct {
	sandbox string
	// fpBefore/fpAfter let a test simulate the source repo's content fingerprint
	// changing between the two snapshots GeneratePlan takes.
	fpBefore, fpAfter string
	fpCalls           int
	head              string
	added             bool
	removed           bool
}

func (f *fakeGit) Fingerprint(_ context.Context, _ string) (string, error) {
	f.fpCalls++
	if f.fpCalls == 1 {
		return f.fpBefore, nil
	}
	return f.fpAfter, nil
}

func (f *fakeGit) Head(_ context.Context, _ string) (string, error) {
	return f.head, nil
}

func (f *fakeGit) AddDisposableWorktree(_ context.Context, _ string) (string, error) {
	f.added = true
	return f.sandbox, nil
}

func (f *fakeGit) RemoveWorktree(_ context.Context, _, _ string) error {
	f.removed = true
	return nil
}

// fakeAgent is a test double for the Agent port. It records the workDir it was
// asked to run in so a test can assert planning ran in the sandbox, not the repo.
type fakeAgent struct {
	output   string
	tokens   int
	gotDir   string
	ranCount int
}

func (f *fakeAgent) RunReadOnly(_ context.Context, _, workDir string) (string, int, error) {
	f.ranCount++
	f.gotDir = workDir
	return f.output, f.tokens, nil
}

const planJSON = `{"goal":"expose the core","nodes":[{"id":"core","prompt":"implement the core"}]}`

func TestGeneratePlan_RunsAgentInDisposableSandbox(t *testing.T) {
	git := &fakeGit{
		sandbox:  "/tmp/sandbox",
		fpBefore: "abc:tree1", fpAfter: "abc:tree1",
	}
	agent := &fakeAgent{output: planJSON, tokens: 1234}
	a := &Activities{Agent: agent, Git: git}

	res, err := a.GeneratePlan(context.Background(), GeneratePlanRequest{Goal: "expose the core", WorkDir: "/repo"})

	require.NoError(t, err)
	require.Equal(t, 1234, res.Tokens)
	require.Equal(t, "expose the core", res.Plan.Goal)
	// The agent ran against the disposable worktree, never the user's repo.
	require.Equal(t, "/tmp/sandbox", agent.gotDir)
	require.True(t, git.added)
	// The sandbox is always discarded.
	require.True(t, git.removed)
}

func TestGeneratePlan_TripwireFailsWhenRepoMutated(t *testing.T) {
	// The source repo's content fingerprint changed across the run: the read-only
	// contract was violated, so no plan is returned even though the agent produced
	// valid JSON.
	git := &fakeGit{
		sandbox:  "/tmp/sandbox",
		fpBefore: "abc:tree1", fpAfter: "def:tree2",
	}
	agent := &fakeAgent{output: planJSON}
	a := &Activities{Agent: agent, Git: git}

	_, err := a.GeneratePlan(context.Background(), GeneratePlanRequest{Goal: "expose the core", WorkDir: "/repo"})

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "read-only contract"))
	// Even on the tripwire failure, the sandbox is discarded.
	require.True(t, git.removed)
}

func TestGeneratePlan_TripwireFailsWhenAlreadyDirtyRepoMutated(t *testing.T) {
	// The repo starts dirty and stays dirty (HEAD unchanged), but the content of
	// an already-modified file changes across the run, so the fingerprints differ.
	// A dirty/clean boolean comparison would miss this; the content fingerprint
	// catches it and no plan is returned.
	git := &fakeGit{
		sandbox:  "/tmp/sandbox",
		fpBefore: "abc:dirtyTreeA", fpAfter: "abc:dirtyTreeB",
	}
	agent := &fakeAgent{output: planJSON}
	a := &Activities{Agent: agent, Git: git}

	_, err := a.GeneratePlan(context.Background(), GeneratePlanRequest{Goal: "expose the core", WorkDir: "/repo"})

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "read-only contract"))
	require.True(t, git.removed)
}
