package fleet

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/execstore"
)

// The recording tests drive the real PersistFleetWorkflowState activity against an
// in-memory stand-in for the execstore port, so they assert on the record that was
// written rather than on which activity was called.

// fakeStore is an in-memory execstore.Store. Setting err makes every write fail,
// standing in for a store outage.
type fakeStore struct {
	mu    sync.Mutex
	saved []execstore.Execution
	err   error
}

func (f *fakeStore) SaveExecution(_ context.Context, e execstore.Execution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, e)
	return nil
}

func (f *fakeStore) ListExecutions(_ context.Context, _ execstore.Filter) ([]execstore.Execution, error) {
	return f.records(), nil
}

// records returns the executions written so far, in write order.
func (f *fakeStore) records() []execstore.Execution {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execstore.Execution{}, f.saved...)
}

// last returns the most recent write, which for a settled workflow is its
// terminal record.
func (f *fakeStore) last(t *testing.T) execstore.Execution {
	t.Helper()
	recs := f.records()
	require.NotEmpty(t, recs, "expected the workflow to record its state")
	return recs[len(recs)-1]
}

// storeFor picks the store an env constructor was given, or a fresh one when a
// test does not care about the records.
func storeFor(opts []*fakeStore) execstore.Store {
	if len(opts) > 0 {
		return opts[0]
	}
	return &fakeStore{}
}

func TestFleetWorkflow_RecordsStartAndTerminalStateWithPerNodeBreakdown(t *testing.T) {
	store := &fakeStore{}
	env := newEnv(t, store)

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).
		Return("developed successfully\n\nTotal token usage across all sessions: 1,000 tokens.", nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt", PlanID: "plan-abcd1234"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	recs := store.records()
	require.Len(t, recs, 2)

	start := recs[0]
	require.Equal(t, execstore.KindFleet, start.Kind)
	require.Equal(t, execstore.StatusRunning, start.Status)
	require.Equal(t, "expose the core", start.Prompt)
	require.Equal(t, "plan-abcd1234", start.Detail.PlanID, "a run is traceable back to the plan it came from")
	require.Equal(t, 2, start.Detail.PlanNodes)

	end := recs[1]
	require.Equal(t, start.RunID, end.RunID, "both writes key on the run ID, so the second upserts the first")
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Len(t, end.Detail.Nodes, 2)
	require.Equal(t, "core", end.Detail.Nodes[0].ID)
	require.Equal(t, string(StatusSucceeded), end.Detail.Nodes[0].Status)
	// The orchestrator runs no agent of its own, and each node's develop run records
	// its own usage, so the parent row must add nothing to the total.
	require.Zero(t, end.Tokens)
}

func TestFleetWorkflow_SkippedNodeLivesInTheParentsDetail(t *testing.T) {
	store := &fakeStore{}
	env := newEnv(t, store)

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	// The first node fails, so its dependent is skipped and never starts a child
	// workflow of its own.
	env.OnWorkflow(codereview.DevelopWorkflow, mock.Anything, mock.Anything).
		Return("", errors.New("develop failed"))
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.last(t)
	nodes := map[string]execstore.NodeOutcome{}
	for _, n := range end.Detail.Nodes {
		nodes[n.ID] = n
	}
	require.Equal(t, string(StatusFailed), nodes["core"].Status)
	// A skipped node has no child run ID, so the parent's breakdown is the only
	// place its outcome can live — and it names the dependency that blocked it.
	require.Equal(t, string(StatusSkipped), nodes["rest"].Status)
	require.Contains(t, nodes["rest"].Detail, "core")
}

func TestFleetWorkflow_RejectedUpFront_StillRecordsTheAttempt(t *testing.T) {
	store := &fakeStore{}
	env := newEnv(t, store)

	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	// A missing worktrees directory is rejected before any node starts.
	env.ExecuteWorkflow(FleetWorkflow, FleetInput{Plan: linearPlan(), WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	end := store.last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	require.Contains(t, end.Detail.Error, "WorktreesDir")
}

func TestFleetWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	store := &fakeStore{err: errors.New("postgres is down")}
	env := newEnv(t, store)

	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan: linearPlan(), WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	// The record comes first, so an unrecordable run starts no node.
	env.AssertNotCalled(t, "ResolveBase", mock.Anything, mock.Anything)
}

// cra references the codereview activity bundle's method names for OnActivity, so
// a fleet test can let a real child develop workflow run.
var cra *codereview.Activities

func TestFleetWorkflow_NodeChildRecordsTheFleetAsItsParent(t *testing.T) {
	store := &fakeStore{}
	env := newEnv(t, store)
	// Let the real child develop workflow run, with the same store, so it records
	// itself as this fleet's child. Its own review child is mocked away.
	env.RegisterActivity(&codereview.Activities{Store: store})
	env.RegisterWorkflow(codereview.ReviewWorkflow)

	env.OnActivity(fa.ResolveBase, mock.Anything, mock.Anything).Return("base-sha", nil)
	env.OnActivity(cra.CreateBranch, mock.Anything, mock.Anything).
		Return(codereview.CreateBranchResult{Branch: "feat/node", WorkDir: "/wt/node", BaseSHA: "base-sha"}, nil)
	env.OnActivity(cra.RunDevelopAgent, mock.Anything, mock.Anything).
		Return(codereview.AgentResult{Output: "done", Tokens: 400}, nil)
	env.OnActivity(cra.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(codereview.ReviewWorkflow, mock.Anything, mock.Anything).
		Return(codereview.ReviewOutcome{Summary: "reviewed", Converged: true}, nil)
	env.OnActivity(na.Notify, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(FleetWorkflow, FleetInput{
		Plan:    FleetPlan{Goal: "one node", Nodes: []FleetNode{{ID: "core", Prompt: "implement the core"}}},
		WorkDir: "/repo", WorktreesDir: "/wt"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var fleetRec, nodeRec execstore.Execution
	for _, r := range store.records() {
		switch r.Kind {
		case execstore.KindFleet:
			fleetRec = r
		case execstore.KindDevelop:
			nodeRec = r
		}
	}
	require.NotEmpty(t, fleetRec.WorkflowID)
	require.NotEmpty(t, nodeRec.WorkflowID)
	// The tree is reconstructed from the parent handle, not by parsing workflow IDs:
	// the node points at the fleet run that started it.
	require.Equal(t, fleetRec.WorkflowID, nodeRec.ParentWorkflowID)
	// Each row carries its own usage: the node's develop tokens live on the node
	// row, and the parent adds nothing.
	require.Equal(t, 400, nodeRec.Tokens)
	require.Zero(t, fleetRec.Tokens)
}
