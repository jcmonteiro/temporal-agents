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
	// headBefore/headAfter and dirtyBefore/dirtyAfter let a test simulate the
	// source repo changing between the two snapshots GeneratePlan takes.
	headBefore, headAfter   string
	dirtyBefore, dirtyAfter bool
	headCalls               int
	dirtyCalls              int
	added                   bool
	removed                 bool
}

func (f *fakeGit) Head(_ context.Context, _ string) (string, error) {
	f.headCalls++
	if f.headCalls == 1 {
		return f.headBefore, nil
	}
	return f.headAfter, nil
}

func (f *fakeGit) HasChanges(_ context.Context, _ string) (bool, error) {
	f.dirtyCalls++
	if f.dirtyCalls == 1 {
		return f.dirtyBefore, nil
	}
	return f.dirtyAfter, nil
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
		sandbox:    "/tmp/sandbox",
		headBefore: "abc", headAfter: "abc",
		dirtyBefore: false, dirtyAfter: false,
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
	// The source repo's HEAD advanced across the run: the read-only contract was
	// violated, so no plan is returned even though the agent produced valid JSON.
	git := &fakeGit{
		sandbox:    "/tmp/sandbox",
		headBefore: "abc", headAfter: "def",
	}
	agent := &fakeAgent{output: planJSON}
	a := &Activities{Agent: agent, Git: git}

	_, err := a.GeneratePlan(context.Background(), GeneratePlanRequest{Goal: "expose the core", WorkDir: "/repo"})

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "read-only contract"))
	// Even on the tripwire failure, the sandbox is discarded.
	require.True(t, git.removed)
}
