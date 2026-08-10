package codereview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/instruction/instructiontest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
)

// Where the code workflows say they run. The probe is driven through the real
// activity against a described layout, so what is asserted is the record an
// operator's overview is built from.

// newDevelopEnvIn builds the develop environment around a described repository
// layout, so the run's own probe answers what that layout says.
func newDevelopEnvIn(t *testing.T, store *execstoretest.Store, prober place.Prober) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: prober})
	env.RegisterActivity(&instruction.Activity{Store: instructiontest.New()})
	env.RegisterWorkflow(DevelopWorkflow)
	env.RegisterWorkflow(ReviewWorkflow)
	return env
}

func TestDevelopWorkflow_RecordsTheWorktreeItDevelopsInNotTheDirectoryItWasStartedFrom(t *testing.T) {
	// This is the case the whole feature exists for: a fleet node (and any
	// `--worktrees-dir` run) is started against the repository but develops in a
	// worktree of its own. Recording the directory it was started from would put
	// every node on the same planet.
	store := execstoretest.New()
	env := newDevelopEnvIn(t, store,
		placetest.New().InWorktree("/srv/worktrees/feat-x", "/srv/repos/pricing"))

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/srv/worktrees/feat-x", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).
		Return(ReviewOutcome{Summary: "reviewed", Converged: true}, nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{
		WorkDir: "/srv/repos/pricing", WorktreesDir: "/srv/worktrees", Prompt: "do it"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	recs := store.Records()
	terminal := recs[len(recs)-1]
	require.Equal(t, "/srv/worktrees/feat-x", terminal.Detail.Directory)
	require.Equal(t, "/srv/repos/pricing", terminal.Detail.Repository)
	// And it is known while the run is still running, not only once it settles:
	// otherwise the overview shows a running node in the unknown place for as long
	// as the agent works.
	require.Equal(t, execstore.StatusRunning, recs[1].Status)
	require.Equal(t, "/srv/worktrees/feat-x", recs[1].Detail.Directory)
}

func TestDevelopWorkflow_AProbeThatCannotAnswerLeavesTheRunPlacelessAndUnharmed(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvIn(t, store, placetest.Failing(errors.New("git is not available")))

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).
		Return(ReviewOutcome{Summary: "reviewed", Converged: true}, nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do it"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a failed probe must never fail the development it describes")
	recs := store.Records()
	// Two writes, not three: with nothing established there is nothing to record in
	// between, so the failed probe costs the run neither a place nor a write.
	require.Len(t, recs, 2)
	require.Empty(t, recs[len(recs)-1].Detail.Directory)
}

func TestReviewWorkflow_RecordsThePlaceThePassReviewsIn(t *testing.T) {
	store := execstoretest.New()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{
		Prober: placetest.New().InWorktree("/srv/worktrees/feat-x", "/srv/repos/pricing"),
	})
	env.RegisterWorkflow(ReviewWorkflow)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "feedback"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/srv/worktrees/feat-x"})

	require.True(t, env.IsWorkflowCompleted())
	require.NotEmpty(t, store.Records())
	require.Equal(t, "/srv/worktrees/feat-x", store.Records()[0].Detail.Directory)
	require.Equal(t, "/srv/repos/pricing", store.Records()[0].Detail.Repository)
}

func TestPilotWorkflow_RecordsThePlaceThePassRunsIn(t *testing.T) {
	store := execstoretest.New()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	env.RegisterActivity(&instruction.Activity{Store: instructiontest.New()})
	env.RegisterWorkflow(PilotWorkflow)
	pr := PullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", HeadRef: "feat/x"}
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).Return(LoadCommentsResult{}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/srv/repos/pricing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NotEmpty(t, store.Records())
	require.Equal(t, "/srv/repos/pricing", store.Records()[0].Detail.Directory)
}
