package fleet

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/wfrecord"
)

// This file is the recording half of the fleet workflows: the typed state they
// persist, the activities that write it through the execstore port, and the
// workflow-side helpers that call them. As everywhere else, the write happens in
// an activity so the workflow stays deterministic, and only the port's record
// types cross the boundary.

// FleetState is the typed input to PersistFleetWorkflowState: everything the
// `fleet execute` parent knows about its own orchestration run.
type FleetState struct {
	// WorkflowID and RunID are the Temporal correlation handles; RunID is the key
	// the write upserts on. Each node's child develop workflow records itself
	// separately and points back here through its parent handle, so the fleet→node
	// tree is reconstructable without parsing IDs.
	WorkflowID string
	RunID      string
	// ParentWorkflowID is set only in the unusual case of a nested fleet run.
	ParentWorkflowID string
	// Goal is the plan's high-level goal.
	Goal string
	// PlanID is the stored plan this run executes, correlating the run with the
	// plan handle it came from.
	PlanID string
	// PlanNodes is how many nodes the plan has.
	PlanNodes int
	// StartedAt and EndedAt come from the workflow's deterministic clock.
	StartedAt time.Time
	EndedAt   time.Time
	Status    execstore.Status
	// Nodes is the per-node breakdown. It is the only home for a skipped node's
	// outcome: a skipped node's dependency did not succeed, so it never starts a
	// child workflow and has no run ID to record a row under.
	Nodes []NodeResult
	Error string
}

// PersistFleetWorkflowState records a FleetWorkflow execution's state. It is
// called when the run starts and again when every node has settled.
//
// The row carries no token usage of its own: the orchestrator runs no agent, and
// each node's develop (and that node's review) records its own usage, so summing
// rows gives the run's real cost with nothing counted twice. The per-node token
// figures in the breakdown are there to read, not to add.
func (a *Activities) PersistFleetWorkflowState(ctx context.Context, in FleetState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindFleet,
		Prompt:           in.Goal,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail: execstore.Detail{
			PlanID:    in.PlanID,
			PlanNodes: in.PlanNodes,
			Nodes:     nodeOutcomes(in.Nodes),
			Error:     in.Error,
		},
	})
}

// nodeOutcomes maps the fleet's own per-node results onto the record type, so the
// domain type stays free of any persistence concern.
func nodeOutcomes(results []NodeResult) []execstore.NodeOutcome {
	if len(results) == 0 {
		return nil
	}
	out := make([]execstore.NodeOutcome, 0, len(results))
	for _, r := range results {
		out = append(out, execstore.NodeOutcome{
			ID:     r.ID,
			Status: string(r.Status),
			Detail: r.Detail,
			Tokens: r.Tokens,
		})
	}
	return out
}

// startFleetState builds and writes the "started" record for the running fleet
// orchestration, returning the state the terminal write updates.
func startFleetState(ctx workflow.Context, in FleetInput) (FleetState, error) {
	id := wfrecord.Of(ctx)
	st := FleetState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		Goal:             in.Plan.Goal,
		PlanID:           in.PlanID,
		PlanNodes:        len(in.Plan.Nodes),
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistFleetWorkflowState, st).Get(opts, nil); err != nil {
		return FleetState{}, fmt.Errorf("record the fleet run as started: %w", err)
	}
	return st, nil
}

// finishFleetState records the fleet run's terminal state, per-node breakdown
// included, on a disconnected context so a cancelled run still settles its
// record.
func finishFleetState(ctx workflow.Context, st FleetState, err error) error {
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistFleetWorkflowState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the fleet run's terminal state: %w", perr)
	}
	return nil
}

// FleetPlanState is the typed input to PersistFleetPlanWorkflowState: what the
// `fleet plan` agent run knows about itself.
//
// Planning is recorded as its own kind, separate from the `fleet execute` run
// that later orchestrates the plan, so its status, timing and token cost are
// visible on their own rather than folded into an execution it may never have.
type FleetPlanState struct {
	// WorkflowID and RunID are the Temporal correlation handles; RunID is the key
	// the write upserts on.
	WorkflowID string
	RunID      string
	// ParentWorkflowID is set only when planning was started as a child workflow.
	ParentWorkflowID string
	// Goal is the high-level change the run was asked to decompose.
	Goal string
	// PlanID is the handle the produced plan is stored under, so the planning run
	// and the plan it created correlate.
	PlanID string
	// PlanNodes is how many nodes the produced plan has, known only once it exists.
	PlanNodes int
	// StartedAt and EndedAt come from the workflow's deterministic clock.
	StartedAt time.Time
	EndedAt   time.Time
	Status    execstore.Status
	// Tokens is the planning agent session's own usage.
	Tokens int
	Error  string
}

// PersistFleetPlanWorkflowState records a FleetPlanWorkflow execution's state.
func (a *Activities) PersistFleetPlanWorkflowState(ctx context.Context, in FleetPlanState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindFleetPlan,
		Prompt:           in.Goal,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail: execstore.Detail{
			PlanID:    in.PlanID,
			PlanNodes: in.PlanNodes,
			Error:     in.Error,
		},
	})
}

// startFleetPlanState builds and writes the "started" record for the running
// planning workflow.
func startFleetPlanState(ctx workflow.Context, in FleetPlanInput) (FleetPlanState, error) {
	id := wfrecord.Of(ctx)
	st := FleetPlanState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		Goal:             in.Goal,
		PlanID:           in.PlanID,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistFleetPlanWorkflowState, st).Get(opts, nil); err != nil {
		return FleetPlanState{}, fmt.Errorf("record the fleet planning run as started: %w", err)
	}
	return st, nil
}

// finishFleetPlanState records the planning run's terminal state on a
// disconnected context, so a cancelled run still settles its record.
func finishFleetPlanState(ctx workflow.Context, st FleetPlanState, err error) error {
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistFleetPlanWorkflowState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the fleet planning run's terminal state: %w", perr)
	}
	return nil
}
