package fleet

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
)

// newEnvIn builds the fleet environment around a described repository layout, so
// the orchestration's own probe answers what that layout says.
func newEnvIn(t *testing.T, store *execstoretest.Store, prober place.Prober) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store, Plans: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: prober})
	env.RegisterWorkflow(FleetWorkflow)
	env.RegisterWorkflow(FleetPlanWorkflow)
	env.RegisterWorkflow(codereview.DevelopWorkflow)
	return env
}

func TestFleetWorkflow_RecordsTheRepositoryItOrchestratesFrom(t *testing.T) {
	// The fleet itself runs in the repository; its nodes run in worktrees of that
	// repository and record those themselves (see the codereview package). That is
	// what makes a fleet's nodes hang under the fleet's own place.
	store := execstoretest.New()
	env := newEnvIn(t, store, placetest.New().InRepository("/srv/repos/pricing", "/srv/repos/pricing"))

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).
		Return("developed successfully", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/srv/repos/pricing", WorktreesDir: "/srv/worktrees"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "/srv/repos/pricing", store.Last(t).Detail.Directory)
	require.Empty(t, store.Last(t).Detail.Repository, "the repository is one place, not two")
}

func TestFleetPlanWorkflow_RecordsTheRepositoryItPlannedAgainst(t *testing.T) {
	// Planning reads a disposable clone, which is an implementation detail of the
	// step. The place an operator has work in is the repository they planned for.
	store := execstoretest.New()
	env := newEnvIn(t, store, placetest.New())

	env.OnActivity(fa.GeneratePlan, mock.Anything, mock.Anything).
		Return(GeneratePlanResult{Plan: linearPlan(), Tokens: 10}, nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetPlanWorkflow, FleetPlanInput{
		Goal: "expose the core", WorkDir: "/srv/repos/pricing", PlanID: "plan-abcd1234"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "/srv/repos/pricing", store.Last(t).Detail.Directory)
}
