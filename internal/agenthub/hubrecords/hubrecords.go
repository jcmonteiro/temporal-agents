// Package hubrecords is the driven adapter that answers the Agent Hub's read
// ports from the durable execution record: the recorded executions behind
// agenthub.RecordSource, and a fleet's approved plan behind agenthub.PlanSource.
//
// It exists so the read API's core depends on neither the record's schema nor the
// orchestration core's plan type. Everything crossing into agenthub is translated
// here: the record's statuses become the API's outcomes, the stored plan document
// becomes the API's plan, and the workflow-ID convention supplies each execution's
// class. Swapping the record for a purpose-built projection later means writing a
// second adapter, not changing the core or the published contract.
package hubrecords

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/execstore"
	"temporal-agents/internal/fleet"
	"temporal-agents/internal/wfid"
)

// Records answers the record-backed read ports. It holds the two execstore ports
// it reads through rather than a database handle, so it is driven by the same
// in-memory fake the workflows' tests use and never sees a driver type.
type Records struct {
	executions execstore.ExecutionReader
	overview   execstore.OverviewReader
	plans      execstore.PlanReader
}

// Compile-time proof the adapter satisfies the ports it is injected as.
var (
	_ agenthub.RecordSource     = (*Records)(nil)
	_ agenthub.CollectionSource = (*Records)(nil)
	_ agenthub.PlanSource       = (*Records)(nil)
)

// New returns the adapter over the given ports. Both are required: a reader
// without a plan store could answer executions but would report every fleet as
// having no plan, which looks exactly like a fleet whose plan was lost.
func New(executions interface {
	execstore.ExecutionReader
	execstore.OverviewReader
}, plans execstore.PlanReader) (*Records, error) {
	if executions == nil {
		return nil, errors.New("the execution reader is required")
	}
	if plans == nil {
		return nil, errors.New("the plan reader is required")
	}
	return &Records{executions: executions, overview: executions, plans: plans}, nil
}

// RecordedExecutions implements agenthub.RecordSource.
func (r *Records) RecordedExecutions(ctx context.Context, q agenthub.RecordQuery) ([]agenthub.Execution, error) {
	records, err := r.executions.ListExecutions(ctx, execstore.Filter{
		Kind:       kindFor(q.Class),
		WorkflowID: q.WorkflowID,
		ScheduleID: q.ScheduleID,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]agenthub.Execution, 0, len(records))
	for _, record := range records {
		out = append(out, executionFrom(record))
	}
	return out, nil
}

// RunChains implements agenthub.CollectionSource.
func (r *Records) RunChains(ctx context.Context, query agenthub.ChainQuery) ([]agenthub.ExecutionChain, error) {
	chains, err := r.overview.ListExecutionChains(ctx, execstore.ChainFilter{
		Kinds: []execstore.Kind{
			execstore.KindRun, execstore.KindDevelop, execstore.KindReview,
			execstore.KindPilot, execstore.KindFleetPlan,
		},
		WorkflowID:          query.WorkflowID,
		RequiredWorkflowIDs: query.RequiredWorkflowIDs,
		ExcludedWorkflowIDs: query.ExcludedWorkflowIDs,
		Limit:               query.Limit,
	})
	if err != nil {
		return nil, err
	}
	return chainsFrom(chains), nil
}

// FleetTrees implements agenthub.CollectionSource.
func (r *Records) FleetTrees(ctx context.Context, query agenthub.ChainQuery) ([]agenthub.FleetTree, error) {
	trees, err := r.overview.ListExecutionTrees(ctx, execstore.ChainFilter{
		Kinds:               []execstore.Kind{execstore.KindFleet},
		WorkflowID:          query.WorkflowID,
		RequiredWorkflowIDs: query.RequiredWorkflowIDs,
		ExcludedWorkflowIDs: query.ExcludedWorkflowIDs,
		Limit:               query.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]agenthub.FleetTree, 0, len(trees))
	for _, tree := range trees {
		converted := agenthub.FleetTree{Chain: chainFrom(tree.Chain)}
		for _, execution := range tree.Executions {
			converted.Executions = append(converted.Executions, executionFrom(execution))
		}
		out = append(out, converted)
	}
	return out, nil
}

// ScheduleActionChains implements agenthub.CollectionSource.
func (r *Records) ScheduleActionChains(ctx context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]agenthub.ExecutionChain, error) {
	actions, err := r.overview.ListScheduleActionChains(ctx, scheduleIDs, perScheduleLimit)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]agenthub.ExecutionChain, len(actions))
	for id, chains := range actions {
		out[id] = chainsFrom(chains)
	}
	return out, nil
}

// Plans implements agenthub.PlanSource.
func (r *Records) Plans(ctx context.Context, refs []agenthub.PlanReference) (map[string]agenthub.Plan, error) {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.PlanID != "" {
			ids = append(ids, ref.PlanID)
		}
	}
	stored, err := r.plans.Plans(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]agenthub.Plan, len(refs))
	for _, ref := range refs {
		plan, ok := stored[ref.PlanID]
		if !ok {
			continue
		}
		converted, err := planFrom(plan)
		if err != nil {
			return nil, err
		}
		out[ref.FleetID] = converted
	}
	return out, nil
}

func chainsFrom(chains []execstore.ExecutionChain) []agenthub.ExecutionChain {
	out := make([]agenthub.ExecutionChain, 0, len(chains))
	for _, chain := range chains {
		out = append(out, chainFrom(chain))
	}
	return out
}

func chainFrom(chain execstore.ExecutionChain) agenthub.ExecutionChain {
	return agenthub.ExecutionChain{
		Latest:     executionFrom(chain.Latest),
		StartedAt:  chain.StartedAt,
		Iterations: chain.Iterations,
		Tokens:     chain.Tokens,
	}
}

// PlanFor resolves one fleet plan for compatibility with detail-focused callers.
// PlanFor implements agenthub.PlanSource: it resolves the plan a fleet executes by
// following the plan handle the fleet's own record carries, then reading that plan
// from the store.
//
// Going through the record rather than through an ambient plan file is what makes
// the answer trustworthy: the handle was written by the run itself, so it names the
// plan that run actually executed — not whichever plan document happens to be
// lying around, which may be absent, stale, or another fleet's.
func (r *Records) PlanFor(ctx context.Context, fleetID string) (agenthub.Plan, error) {
	if fleetID == "" {
		return agenthub.Plan{}, agenthub.ErrNoPlan
	}
	// KindFleet narrows the tree query back to the parent itself. Without it, a
	// sufficiently large fleet could fill the cap with newer child records and push
	// its older parent (the row that carries the plan handle) out of the result.
	records, err := r.executions.ListExecutions(ctx, execstore.Filter{
		Kind:       execstore.KindFleet,
		WorkflowID: fleetID,
		Limit:      1,
	})
	if err != nil {
		return agenthub.Plan{}, err
	}
	planID := ""
	for _, record := range records {
		if record.WorkflowID == fleetID && record.Detail.PlanID != "" {
			planID = record.Detail.PlanID
			break
		}
	}
	if planID == "" {
		// A fleet started before plans were stored, or one whose record predates the
		// handle: its graph is unknowable, which the core reports as a fleet without
		// nodes rather than as a failure.
		return agenthub.Plan{}, agenthub.ErrNoPlan
	}

	stored, err := r.plans.Plan(ctx, planID)
	if errors.Is(err, execstore.ErrNoSuchPlan) {
		return agenthub.Plan{}, agenthub.ErrNoPlan
	}
	if err != nil {
		return agenthub.Plan{}, err
	}
	return planFrom(stored)
}

// planFrom decodes a stored plan document into the API's own plan type. The
// document is the JSON encoding of the orchestration core's plan, which this
// adapter is allowed to know and the core of the read API is not.
func planFrom(stored execstore.Plan) (agenthub.Plan, error) {
	var decoded fleet.FleetPlan
	if err := json.Unmarshal(stored.Document, &decoded); err != nil {
		return agenthub.Plan{}, fmt.Errorf("decode the stored plan %s: %w", stored.ID, err)
	}
	plan := agenthub.Plan{Goal: decoded.Goal, Nodes: make([]agenthub.PlanNode, 0, len(decoded.Nodes))}
	if plan.Goal == "" {
		// The goal is stored alongside the document for listing; fall back to it so a
		// document written without one still names the fleet.
		plan.Goal = stored.Goal
	}
	for _, node := range decoded.Nodes {
		plan.Nodes = append(plan.Nodes, agenthub.PlanNode{
			ID:        node.ID,
			Prompt:    node.Prompt,
			DependsOn: node.DependsOn,
		})
	}
	return plan, nil
}

// executionFrom translates one record into the API's execution type.
func executionFrom(record execstore.Execution) agenthub.Execution {
	e := agenthub.Execution{
		WorkflowID:       record.WorkflowID,
		RunID:            record.RunID,
		FirstRunID:       record.FirstRunID,
		Class:            wfid.Classify(record.WorkflowID),
		Outcome:          outcomeFrom(record.Status),
		Label:            record.Prompt,
		StartedAt:        record.StartedAt,
		EndedAt:          record.EndedAt,
		Tokens:           record.Tokens,
		ScheduleID:       record.ScheduleID,
		ParentWorkflowID: record.ParentWorkflowID,
		PlanID:           record.Detail.PlanID,
		// The place travels as the facts that were recorded, not as a location: the
		// core owns what a directory and its repository mean, and this adapter must not
		// decide it on the way past.
		Place: agenthub.RecordedPlace{
			Directory:  record.Detail.Directory,
			Repository: record.Detail.Repository,
		},
	}
	for _, node := range record.Detail.Nodes {
		e.NodeOutcomes = append(e.NodeOutcomes, agenthub.NodeOutcome{
			NodeID:  node.ID,
			Outcome: nodeOutcomeFrom(node.Status),
		})
	}
	for _, use := range record.Detail.Instructions {
		e.Instructions = append(e.Instructions, agenthub.InstructionUse{
			Key:     use.Key,
			Scope:   use.Scope,
			Version: use.Version,
			Hash:    use.Hash,
		})
	}
	return e
}

// outcomeFrom maps a recorded status onto the API's outcome vocabulary. An
// unrecognised status is reported as failed rather than as success: the API never
// claims an outcome it cannot evidence.
func outcomeFrom(status execstore.Status) agenthub.ExecutionOutcome {
	switch status {
	case execstore.StatusRunning:
		return agenthub.OutcomeRunning
	case execstore.StatusSucceeded:
		return agenthub.OutcomeSucceeded
	case execstore.StatusSkipped:
		return agenthub.OutcomeSkipped
	default:
		return agenthub.OutcomeFailed
	}
}

// nodeOutcomeFrom maps a fleet parent's per-node status onto the API's outcomes.
// It is the only place the fleet's own "blocked" — a recoverable stop that needs a
// human — enters the read path, because a blocked node's child execution records
// itself as a plain failure.
func nodeOutcomeFrom(status string) agenthub.ExecutionOutcome {
	switch fleet.NodeStatus(status) {
	case fleet.StatusSucceeded:
		return agenthub.OutcomeSucceeded
	case fleet.StatusBlocked:
		return agenthub.OutcomeBlocked
	case fleet.StatusSkipped:
		return agenthub.OutcomeSkipped
	default:
		return agenthub.OutcomeFailed
	}
}

// kindFor maps a requested execution class onto the recorded kind that holds it.
// A fleet node is recorded as a develop execution (it is one — the fleet is what
// makes it a node), and a class the record has no kind for imposes no constraint,
// leaving the caller's other filters to narrow the read.
func kindFor(class wfid.Class) execstore.Kind {
	switch class {
	case wfid.ClassRun:
		return execstore.KindRun
	case wfid.ClassDevelop, wfid.ClassFleetNode:
		return execstore.KindDevelop
	case wfid.ClassReview:
		return execstore.KindReview
	case wfid.ClassPilot:
		return execstore.KindPilot
	case wfid.ClassFleet:
		return execstore.KindFleet
	case wfid.ClassFleetPlan:
		return execstore.KindFleetPlan
	case wfid.ClassSteering:
		return execstore.KindSteering
	default:
		return ""
	}
}
