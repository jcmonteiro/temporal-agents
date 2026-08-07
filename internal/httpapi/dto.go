package httpapi

import (
	"math"
	"time"

	"temporal-agents/internal/agenthub"
)

// This file holds the wire representations: the published shape of every resource,
// separate from the model it is projected from.
//
// The separation is the point. The core's types may be refactored, and the source
// the facts come from may be replaced (today a workflow-ID convention over a
// durable record, tomorrow a purpose-built projection) without either being visible
// here — a consumer's contract is these structs and the schemas generated from
// them, nothing else. Every field is a fact the API can evidence: there is no
// owner, no estimate and no free-text description, because nothing in the system
// knows them.

// collection is the envelope every collection response uses. One envelope for all
// of them means a consumer writes the "read a page" code once, and leaves room to
// describe a page (its cap, how much of it came back) without changing an item's
// shape.
type collection[T any] struct {
	// Items are the page's items, newest first.
	Items []T `json:"items"`
	// Count is how many items this page carries.
	Count int `json:"count"`
	// Limit is the cap that was applied, whether the caller asked for it or it is
	// the default.
	Limit int `json:"limit"`
}

// newCollection wraps items in the envelope, normalising a nil slice to an empty
// one so a consumer never has to tell "no items" apart from "no field".
func newCollection[T any](items []T, limit int) collection[T] {
	if items == nil {
		items = []T{}
	}
	return collection[T]{Items: items, Count: len(items), Limit: limit}
}

// fleetResource is a fleet: an orchestrated plan, its aggregated status and its
// derived progress.
//
// Nodes are present in the single-fleet representation and omitted from the
// collection, so an overview read does not carry every plan's whole graph. UpNext
// is the server's answer to "what has not started yet", which keeps the queue's
// meaning from drifting between consumers.
type fleetResource struct {
	// ID is the fleet's identity: its parent workflow ID.
	ID string `json:"id"`
	// Kind is always "fleet", so a consumer that merges the three collections into
	// one overview can tell items apart without tracking where each came from.
	Kind agenthub.ItemKind `json:"kind"`
	// Label is what the fleet is: its plan's goal.
	Label string `json:"label"`
	// Status is the aggregated status of the whole fleet.
	Status agenthub.WorkStatus `json:"status"`
	// Progress is done nodes over total nodes.
	Progress progressResource `json:"progress"`
	// PlanID is the stored plan the fleet executes, when it is known.
	PlanID string `json:"planId,omitempty"`
	// StartedAt is when the fleet started, in UTC.
	StartedAt *string `json:"startedAt"`
	// EndedAt is when it settled, or null while it is still running.
	EndedAt *string `json:"endedAt"`
	// Dismissible reports whether the operator may hide it from the overview.
	Dismissible bool `json:"dismissible"`
	// UpNext are the nodes that have not started: runnable first, then waiting.
	UpNext []fleetNodeResource `json:"upNext,omitempty"`
	// Nodes is the plan's graph with each node's status, present only in the
	// single-fleet representation.
	Nodes []fleetNodeResource `json:"nodes,omitempty"`
}

// fleetNodeResource is one plan node with what happened to it.
type fleetNodeResource struct {
	// ID is the node's ID within its plan.
	ID string `json:"id"`
	// Label is what to call the node. It is the node's ID: a plan node has no
	// separate name, and inventing one would be a fabrication.
	Label string `json:"label"`
	// Prompt is the instruction the node runs.
	Prompt string `json:"prompt,omitempty"`
	// DependsOn lists the nodes this one runs after. The edges are the graph.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Status is the node's derived status.
	Status agenthub.WorkStatus `json:"status"`
	// Execution is the child execution the node ran in, or null when it never
	// started.
	Execution *nodeExecutionResource `json:"execution"`
}

// nodeExecutionResource is the execution a plan node ran in.
type nodeExecutionResource struct {
	// WorkflowID is the child execution's workflow ID.
	WorkflowID string `json:"workflowId"`
	// RunID is the latest iteration's run ID.
	RunID string `json:"runId,omitempty"`
	// StartedAt is when the child started, in UTC.
	StartedAt *string `json:"startedAt"`
	// EndedAt is when it settled, or null while it runs.
	EndedAt *string `json:"endedAt"`
	// Tokens is the child's own token usage.
	Tokens int `json:"tokens,omitempty"`
}

// progressResource is how much of a plan is done. Fraction is included so every
// consumer draws the same bar rather than each rounding its own way; skipped and
// blocked nodes count in the total but never in done.
type progressResource struct {
	// Done is how many nodes completed successfully.
	Done int `json:"done"`
	// Total is how many nodes the plan has.
	Total int `json:"total"`
	// Fraction is Done/Total in [0,1], and 0 for an empty plan.
	Fraction float64 `json:"fraction"`
}

// runResource is one standalone execution chain.
type runResource struct {
	// ID is the chain's identity: its workflow ID, stable across every
	// continue-as-new iteration.
	ID string `json:"id"`
	// Kind is always "run".
	Kind agenthub.ItemKind `json:"kind"`
	// Type says which command started it, so a consumer can label it without
	// parsing its ID.
	Type agenthub.RunType `json:"type"`
	// Label is what was asked of the agent.
	Label string `json:"label"`
	// Status is the latest iteration's status.
	Status agenthub.WorkStatus `json:"status"`
	// StartedAt is when the chain's earliest known iteration started, in UTC.
	StartedAt *string `json:"startedAt"`
	// EndedAt is when the latest iteration settled, or null while it runs.
	EndedAt *string `json:"endedAt"`
	// Iterations is how many iterations of this chain are known, which is how a
	// consumer shows "looping" without one item per loop.
	Iterations int `json:"iterations"`
	// Tokens is the token usage summed over the chain's known iterations.
	Tokens int `json:"tokens,omitempty"`
	// Dismissible reports whether the operator may hide it from the overview.
	Dismissible bool `json:"dismissible"`
}

// scheduleResource is one schedule. It carries no progress: a schedule is
// recurring, so there is no finite amount of work to be a fraction of.
type scheduleResource struct {
	// ID is the schedule's identity.
	ID string `json:"id"`
	// Kind is always "schedule".
	Kind agenthub.ItemKind `json:"kind"`
	// Label is what the scheduled run asks of the agent, when it is known.
	Label string `json:"label"`
	// Spec is a human-readable rendering of when it fires.
	Spec string `json:"spec,omitempty"`
	// Status is paused when it is paused, in progress while an action runs, else
	// the outcome of its most recent completed action, and todo when it never fired.
	Status agenthub.WorkStatus `json:"status"`
	// Paused reports whether it is currently paused.
	Paused bool `json:"paused"`
	// RunningActions is how many of its runs are in flight right now.
	RunningActions int `json:"runningActions"`
	// LastRunAt is when it last fired, or null when it never has.
	LastRunAt *string `json:"lastRunAt"`
	// NextRunAt is when it fires next, or null when it is paused or has no further
	// action.
	NextRunAt *string `json:"nextRunAt"`
	// Dismissible is always false: a schedule has no finished state, so hiding it
	// would hide live configuration.
	Dismissible bool `json:"dismissible"`
}

// dismissalResource is one dismissal: the operator's view state over a finished
// item.
type dismissalResource struct {
	// ID is the dismissal's own identifier, "<kind>:<itemId>", and the last path
	// segment of its resource.
	ID string `json:"id"`
	// Kind is the kind of item that was dismissed.
	Kind agenthub.ItemKind `json:"kind"`
	// ItemID is the dismissed item's identity.
	ItemID string `json:"itemId"`
	// DismissedAt is when it was dismissed, in UTC.
	DismissedAt *string `json:"dismissedAt"`
}

// dismissalRequest is the body of a dismissal write. It names the item to hide, and
// nothing else: what is hidden is derived, never client-supplied, so a client
// cannot invent a dismissal of something that does not exist or has not finished.
type dismissalRequest struct {
	// Kind is the kind of item to dismiss ("fleet" or "run").
	Kind agenthub.ItemKind `json:"kind"`
	// ItemID is the item's identity.
	ItemID string `json:"itemId"`
}

// fleetFrom projects a fleet onto its representation. withNodes is false for a
// collection, where carrying every plan's whole graph would make an overview read
// grow with the size of the plans rather than with the number of fleets.
func fleetFrom(fleet agenthub.Fleet, withNodes bool) fleetResource {
	resource := fleetResource{
		ID:          fleet.ID,
		Kind:        agenthub.KindFleet,
		Label:       fleet.Goal,
		Status:      fleet.Status,
		Progress:    progressFrom(fleet.Progress),
		PlanID:      fleet.PlanID,
		StartedAt:   timestamp(fleet.StartedAt),
		EndedAt:     timestamp(fleet.EndedAt),
		Dismissible: fleet.Dismissible(),
		UpNext:      nodesFrom(fleet.UpNext()),
	}
	if withNodes {
		resource.Nodes = nodesFrom(fleet.Nodes)
	}
	return resource
}

// nodesFrom projects plan nodes onto their representation, preserving order.
func nodesFrom(nodes []agenthub.FleetNode) []fleetNodeResource {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]fleetNodeResource, 0, len(nodes))
	for _, node := range nodes {
		resource := fleetNodeResource{
			ID:        node.ID,
			Label:     node.ID,
			Prompt:    node.Prompt,
			DependsOn: node.DependsOn,
			Status:    node.Status,
		}
		if node.Execution != nil {
			resource.Execution = &nodeExecutionResource{
				WorkflowID: node.Execution.WorkflowID,
				RunID:      node.Execution.RunID,
				StartedAt:  timestamp(node.Execution.StartedAt),
				EndedAt:    timestamp(node.Execution.EndedAt),
				Tokens:     node.Execution.Tokens,
			}
		}
		out = append(out, resource)
	}
	return out
}

// progressFrom projects progress, rounding the fraction to three decimals so the
// same input always serialises to the same bytes (which is what makes a response's
// entity tag stable).
func progressFrom(progress agenthub.Progress) progressResource {
	return progressResource{
		Done:     progress.Done,
		Total:    progress.Total,
		Fraction: math.Round(progress.Fraction()*1000) / 1000,
	}
}

// runFrom projects a run onto its representation.
func runFrom(run agenthub.Run) runResource {
	return runResource{
		ID:          run.ID,
		Kind:        agenthub.KindRun,
		Type:        run.Type,
		Label:       run.Label,
		Status:      run.Status,
		StartedAt:   timestamp(run.StartedAt),
		EndedAt:     timestamp(run.EndedAt),
		Iterations:  run.Iterations,
		Tokens:      run.Tokens,
		Dismissible: run.Dismissible(),
	}
}

// scheduleFrom projects a schedule onto its representation.
func scheduleFrom(schedule agenthub.Schedule) scheduleResource {
	return scheduleResource{
		ID:             schedule.ID,
		Kind:           agenthub.KindSchedule,
		Label:          schedule.Label,
		Spec:           schedule.Spec,
		Status:         schedule.Status,
		Paused:         schedule.Paused,
		RunningActions: schedule.RunningActions,
		LastRunAt:      timestamp(schedule.LastRunAt),
		NextRunAt:      timestamp(schedule.NextRunAt),
		Dismissible:    schedule.Dismissible(),
	}
}

// dismissalFrom projects a dismissal onto its representation.
func dismissalFrom(dismissal agenthub.Dismissal) dismissalResource {
	return dismissalResource{
		ID:          dismissal.ID(),
		Kind:        dismissal.Kind,
		ItemID:      dismissal.ItemID,
		DismissedAt: timestamp(dismissal.DismissedAt),
	}
}

// timestamp renders a time as RFC 3339 in UTC, and as null when there is no time.
//
// Both halves matter for a client anywhere in the world: the offset is always Z, so
// no consumer has to guess a server's local zone, and an absent time is absent
// rather than the zero date, which a consumer would otherwise render as the year 1.
func timestamp(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	rendered := t.UTC().Format(time.RFC3339)
	return &rendered
}
