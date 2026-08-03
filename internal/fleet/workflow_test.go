package fleet

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/notification"
)

// The fleet workflow tests exercise observable behavior — which nodes run in
// which order and what the run reports — with the child develop workflow and
// the plan activity mocked. They say nothing about the develop pipeline itself
// (covered in the codereview package).

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterWorkflow(FleetWorkflow)
	env.RegisterWorkflow(FleetPlanWorkflow)
	// The child develop workflow is mocked, but must be registered so its name
	// resolves.
	env.RegisterWorkflow(codereview.DevelopWorkflow)
	return env
}

var na *notification.Activity // referenced only for method names in OnActivity

var fa *Activities // referenced only for method names in OnActivity

func linearPlan() FleetPlan {
	return FleetPlan{
		Goal: "expose the core",
		Nodes: []FleetNode{
			{ID: "core", Prompt: "implement the core"},
			{ID: "rest", Prompt: "expose via REST", DependsOn: []string{"core"}},
		},
	}
}

func TestFleetWorkflow_HappyPath_RunsEveryNodeAndAggregates(t *testing.T) {
	env := newEnv(t)

	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).
		Return("developed successfully\n\nTotal token usage across all sessions: 1,000 tokens.", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "core: succeeded")
	require.Contains(t, out, "rest: succeeded")
	require.Contains(t, out, "2 node(s): 2 succeeded, 0 failed, 0 skipped.")
	// Both nodes each reported 1,000 tokens, so the fleet total is their sum.
	require.Contains(t, out, "Total token usage across all nodes: 2,000 tokens.")
}

func TestFleetWorkflow_DependencyFailure_SkipsDependents(t *testing.T) {
	env := newEnv(t)

	// The foundation node (core) fails; the dependent (rest) must never start.
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.MatchedBy(func(in codereview.DevelopInput) bool {
		return in.Prompt == "implement the core"
	})).Return("", errors.New("develop blew up"))
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "core: failed")
	require.Contains(t, out, "rest: skipped")
	require.Contains(t, out, "2 node(s): 0 succeeded, 1 failed, 1 skipped.")
}

func TestFleetWorkflow_ParallelNodes_BothRun(t *testing.T) {
	env := newEnv(t)
	plan := FleetPlan{
		Goal: "expose the core",
		Nodes: []FleetNode{
			{ID: "core", Prompt: "core"},
			{ID: "rest", Prompt: "rest", DependsOn: []string{"core"}},
			{ID: "grpc", Prompt: "grpc", DependsOn: []string{"core"}},
		},
	}

	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).Return("ok", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{Plan: plan, WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "3 node(s): 3 succeeded, 0 failed, 0 skipped.")
}

func TestFleetWorkflow_InvalidPlan_FailsWithoutStartingChildren(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	// A cyclic plan is rejected before any child develop workflow starts.
	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		WorkDir: "/repo", WorktreesDir: "/wt",
		Plan: FleetPlan{Nodes: []FleetNode{
			{ID: "a", Prompt: "p", DependsOn: []string{"b"}},
			{ID: "b", Prompt: "q", DependsOn: []string{"a"}},
		}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestFleetWorkflow_Failure_SendsFailureNotification(t *testing.T) {
	env := newEnv(t)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		WorkDir: "/repo", WorktreesDir: "/wt",
		Plan: FleetPlan{Nodes: []FleetNode{{ID: "a", Prompt: "p", DependsOn: []string{"a"}}}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, "Fleet run failed", got.Title)
}

func TestFleetPlanWorkflow_ReturnsGeneratedPlan(t *testing.T) {
	env := newEnv(t)
	plan := linearPlan()

	env.OnActivity(fa.GeneratePlan, mock.Anything, mock.Anything).Return(plan, nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetPlanWorkflow, FleetPlanInput{Goal: "expose the core", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got FleetPlan
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, plan, got)
}
