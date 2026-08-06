package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/notification"
)

// The fleet workflow tests exercise observable behavior — which nodes run in
// which order and what the run reports — with the child develop workflow and
// the plan activity mocked. They say nothing about the develop pipeline itself
// (covered in the codereview package).

// newEnv builds the fleet test environment with a throwaway store, for the tests
// that are not about the durable execution record.
func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	return newEnvWithStore(t, execstoretest.New())
}

// newEnvWithStore builds it around the given store, so a recording test can
// assert on what was written (see recording_test.go). Every workflow records
// itself, and the fleet also stores its plan there, so the store is a required
// dependency rather than an option.
func newEnvWithStore(t *testing.T, store *execstoretest.Store) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store, Plans: store})
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

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
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
	require.Contains(t, out, "2 node(s): 2 succeeded, 0 failed, 0 blocked, 0 skipped.")
	// Both nodes each reported 1,000 tokens, so the fleet total is their sum.
	require.Contains(t, out, "Develop-step token usage across all nodes: 2,000 tokens.")
}

func TestFleetWorkflow_PropagatesInputsAndFormsChildIDs(t *testing.T) {
	env := newEnv(t)

	type childCall struct {
		id string
		in codereview.DevelopInput
	}
	var calls []childCall
	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(workflow.Context)
			in := args.Get(1).(codereview.DevelopInput)
			calls = append(calls, childCall{id: workflow.GetInfo(ctx).WorkflowExecution.ID, in: in})
		}).Return("ok", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt",
		Summary: true,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Key each child call by the node id recovered from its workflow ID suffix.
	byNode := make(map[string]childCall)
	for _, c := range calls {
		idx := strings.LastIndex(c.id, "-")
		byNode[c.id[idx+1:]] = c
	}
	require.Len(t, byNode, 2)

	fleetID := strings.TrimSuffix(byNode["core"].id, "-core")

	// Every node's develop input carries the shared repo/worktree location, its
	// own prompt, the propagated --summary toggle, and Phase 1's await-review
	// gate; each develops on its own run-scoped branch cut from the single base
	// captured once at run start.
	for node, c := range byNode {
		require.Equal(t, "/repo", c.in.WorkDir, node)
		require.Equal(t, "/wt", c.in.WorktreesDir, node)
		require.True(t, c.in.Summary, node)
		require.True(t, c.in.AwaitReview, node)
		require.Equal(t, "base-sha", c.in.StartPoint, node)
		require.Equal(t, NodeBranch(fleetID, node), c.in.Branch, node)
	}
	require.Equal(t, "implement the core", byNode["core"].in.Prompt)
	require.Equal(t, "expose via REST", byNode["rest"].in.Prompt)

	// The foundation has no dependencies to seed from; the dependent is seeded
	// with the foundation's branch so it develops on top of the core's work.
	require.Empty(t, byNode["core"].in.MergeBranches)
	require.Equal(t, []string{NodeBranch(fleetID, "core")}, byNode["rest"].in.MergeBranches)

	// Child workflow IDs are formed as "<fleetID>-<nodeid>": both share the fleet
	// parent's ID as a prefix, differing only in the node suffix.
	require.Equal(t, fleetID, strings.TrimSuffix(byNode["rest"].id, "-rest"))
}

func TestFleetWorkflow_SeedConflictBlocked_RecordsBlockedAndBlocksDependents(t *testing.T) {
	env := newEnv(t)

	// The foundation node's branch cannot be seeded (an unresolved seed conflict),
	// so its child returns the blocked application-error type; the dependent must
	// not start.
	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.MatchedBy(func(in codereview.DevelopInput) bool {
		return in.Prompt == "implement the core"
	})).Return("", temporal.NewNonRetryableApplicationError(
		"cannot resolve conflict", codereview.SeedConflictBlockedErrType, nil))
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	// The blocked node reads as blocked (distinct from failed), and its dependent
	// is still gated.
	require.Contains(t, out, "core: blocked")
	require.Contains(t, out, "rest: skipped")
	require.Contains(t, out, "2 node(s): 0 succeeded, 0 failed, 1 blocked, 1 skipped.")
}

func TestFleetWorkflow_ReviewNotConverged_RecordsBlockedAndBlocksDependents(t *testing.T) {
	env := newEnv(t)

	// The foundation node develops but its local review loop stops at the pass cap
	// without converging, so its child returns the review-not-converged error type.
	// Development landed and the branch is clean, so it reads as blocked (not
	// failed); either way the dependent must not start against un-addressed review
	// feedback.
	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.MatchedBy(func(in codereview.DevelopInput) bool {
		return in.Prompt == "implement the core"
	})).Return("", temporal.NewNonRetryableApplicationError(
		"local review did not converge", codereview.ReviewNotConvergedErrType, nil))
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "core: blocked")
	require.Contains(t, out, "rest: skipped")
	require.Contains(t, out, "2 node(s): 0 succeeded, 0 failed, 1 blocked, 1 skipped.")
}

func TestFleetWorkflow_DependencyFailure_SkipsDependents(t *testing.T) {
	env := newEnv(t)

	// The foundation node (core) fails; the dependent (rest) must never start.
	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
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
	// The skip line names the blocking dependency so the reader need not
	// reconstruct the graph.
	require.Contains(t, out, `rest: skipped (dependency "core" did not succeed)`)
	require.Contains(t, out, "2 node(s): 0 succeeded, 1 failed, 0 blocked, 1 skipped.")
}

func TestFleetWorkflow_TransitiveSkip_PropagatesThroughLayers(t *testing.T) {
	env := newEnv(t)

	// a -> b -> c: node a fails, so b is skipped (direct dependency failed) and c
	// is skipped in turn because its dependency b was itself skipped, not failed.
	// This pins that any non-succeeded status blocks a dependent, across layers.
	plan := FleetPlan{
		Goal: "three-layer chain",
		Nodes: []FleetNode{
			{ID: "a", Prompt: "a"},
			{ID: "b", Prompt: "b", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "c", DependsOn: []string{"b"}},
		},
	}

	// Only node a should ever start a child; b and c must be skipped without
	// running. Mock a to fail; any other child call would be an unexpected call.
	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.MatchedBy(func(in codereview.DevelopInput) bool {
		return in.Prompt == "a"
	})).Return("", errors.New("develop blew up"))
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{Plan: plan, WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "a: failed")
	require.Contains(t, out, `b: skipped (dependency "a" did not succeed)`)
	// c is blocked by b, which was skipped (not failed) — the transitive case.
	require.Contains(t, out, `c: skipped (dependency "b" did not succeed)`)
	require.Contains(t, out, "3 node(s): 0 succeeded, 1 failed, 0 blocked, 2 skipped.")
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

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).Return("ok", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{Plan: plan, WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Contains(t, out, "3 node(s): 3 succeeded, 0 failed, 0 blocked, 0 skipped.")
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

func TestFleetWorkflow_MissingWorktreesDir_FailsWithoutStartingChildren(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	// An empty WorktreesDir is rejected before the base is resolved or any child
	// starts: neither ResolveBase nor DevelopWorkflow is registered as an
	// expected call, so reaching them would fail the run for the wrong reason.
	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo",
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

	env.OnActivity(fa.GeneratePlan, mock.Anything, mock.Anything).
		Return(GeneratePlanResult{Plan: plan, Tokens: 1234}, nil)
	var got notification.Notification
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(notification.Notification) }).Return(nil)

	env.ExecuteWorkflow(FleetPlanWorkflow, FleetPlanInput{Goal: "expose the core", WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var gotPlan FleetPlan
	require.NoError(t, env.GetWorkflowResult(&gotPlan))
	require.Equal(t, plan, gotPlan)

	// The plan-ready notification surfaces the node count and the read-only
	// planning token spend from GeneratePlanResult.Tokens (grouped in thousands).
	require.Equal(t, "Fleet plan ready", got.Title)
	require.Contains(t, got.Body, "Planned 2 node(s) for: expose the core")
	require.Contains(t, got.Body, "Planning token usage: 1,234 tokens.")
}
