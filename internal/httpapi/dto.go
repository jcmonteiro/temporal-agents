package httpapi

import (
	"math"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/setting"
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

// collection is the original v1 envelope used by dismissals: the items, how many
// there are, and the cap that was applied. It stays separate from the additive paged
// active-work contract so existing model names and required fields do not change in
// place.
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

// locatedCollection is the envelope for a collection of work: the same page, plus the
// registry every item's locationId resolves against.
//
// It is a type of its own rather than an optional field on collection because the
// published schema marks locations required here and does not have it at all on
// dismissals (whose items are not work and therefore have no place). One shared
// struct would need `omitempty`, and the response would then satisfy its own schema
// only by way of an invariant three layers away.
type locatedCollection[T any] struct {
	// Items are the page's items, newest first.
	Items []T `json:"items"`
	// Count is how many items this page carries.
	Count int `json:"count"`
	// Limit is the cap that was applied.
	Limit int `json:"limit"`
	// Locations is the flat registry of the places the page's items refer to, closed
	// under ancestry and ordered parents-first. It carries no `omitempty`: the field is
	// required, and an empty page still publishes the unknown place.
	Locations []locationResource `json:"locations"`
}

// newLocatedCollection is newCollection for a collection of work. The registry always
// carries at least the unknown place (see agenthub.NewLocationRegistry), so a page
// with no items still publishes a registry rather than an empty or absent one.
func newLocatedCollection[T any](items []T, limit int, registry agenthub.LocationRegistry) locatedCollection[T] {
	if items == nil {
		items = []T{}
	}
	return locatedCollection[T]{
		Items: items, Count: len(items), Limit: limit, Locations: locationsFrom(registry),
	}
}

// activeWorkCollection is the additive paged contract used by the CLI. It is a
// separate model so existing v1 collection models do not gain required fields.
type activeWorkCollection struct {
	Items []activeWorkResource `json:"items"`
	Count int                  `json:"count"`
	Limit int                  `json:"limit"`
	Next  *string              `json:"next"`
	// Locations is optional in the schema, because this model must stay decodable by a
	// consumer written before it existed. The server nevertheless always sends it: the
	// registry holds at least the unknown place, so the omitempty never fires and a
	// reader here should not conclude the field is sometimes absent.
	Locations []locationResource `json:"locations,omitempty"`
}

// activeWorkResource is one top-level unsettled execution or configured schedule.
// LocationID is optional here, and only here: the CLI and hubclient decode this
// model, and a required field would be a breaking change to a working consumer.
type activeWorkResource struct {
	ID         string                  `json:"id"`
	Type       agenthub.ActiveWorkType `json:"type"`
	Status     agenthub.WorkStatus     `json:"status"`
	Running    bool                    `json:"running"`
	LocationID string                  `json:"locationId,omitempty"`
}

func newActiveWorkCollection(items []activeWorkResource, limit int, next string, registry agenthub.LocationRegistry) activeWorkCollection {
	if items == nil {
		items = []activeWorkResource{}
	}
	document := activeWorkCollection{
		Items: items, Count: len(items), Limit: limit, Locations: locationsFrom(registry),
	}
	if next != "" {
		document.Next = &next
	}
	return document
}

// locationResource is one place in the registry: the published form of the core's
// tagged union, discriminated by kind. A variant carries exactly its own natural key
// — a directory has a path and no ref, a remote has a ref and no path, the unknown
// place has neither and no parent — so a consumer switches on one field instead of
// deducing meaning from which fields happen to be null.
type locationResource struct {
	// ID is the server-issued identity. It is opaque: a consumer references it and
	// never takes it apart.
	ID string `json:"id"`
	// Kind is the union's discriminator.
	Kind agenthub.LocationKind `json:"kind"`
	// Label is what to call the place, computed by the server so no consumer parses a
	// path.
	Label string `json:"label"`
	// ParentID references the place this one is part of, and is null for a root. The
	// parent is always published in the same registry, before this entry.
	ParentID *string `json:"parentId"`
	// Directory is the absolute, cleaned path, present only on the directory variant.
	Directory string `json:"directory,omitempty"`
	// Ref is the bounded reference, present only on the remote variant.
	Ref string `json:"ref,omitempty"`
}

// locationsFrom projects a registry onto its published form, preserving the core's
// parents-first order. The order is the core's, not this layer's: an entity tag is
// computed over these bytes, so re-sorting here would be a second source of truth for
// something that must never move.
func locationsFrom(registry agenthub.LocationRegistry) []locationResource {
	locations := registry.Locations()
	resources := make([]locationResource, 0, len(locations))
	for _, location := range locations {
		resource := locationResource{
			ID:    location.ID(),
			Kind:  location.Kind(),
			Label: location.Label(),
		}
		if parent, ok := location.Parent(); ok {
			id := parent.ID()
			resource.ParentID = &id
		}
		if directory, ok := location.Directory(); ok {
			resource.Directory = directory
		}
		if ref, ok := location.Ref(); ok {
			resource.Ref = ref
		}
		resources = append(resources, resource)
	}
	return resources
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
	// LocationID references the place the fleet runs, resolved against the response's
	// locations registry.
	LocationID string `json:"locationId"`
	// UpNext are the nodes that have not started: runnable first, then waiting.
	UpNext []fleetNodeResource `json:"upNext,omitempty"`
	// Nodes is the plan's graph with each node's status, present only in the
	// single-fleet representation.
	Nodes []fleetNodeResource `json:"nodes,omitempty"`
	// Locations is the registry this resource's references resolve against, present
	// only in the single-fleet representation: in a collection the envelope carries
	// one registry for every item instead of one per item.
	Locations []locationResource `json:"locations,omitempty"`
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
	// LocationID references the place the node runs. A node can develop in a worktree
	// of its own, so it genuinely differs from its fleet.
	LocationID string `json:"locationId"`
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
	// LocationID references the place the run runs.
	LocationID string `json:"locationId"`
	// Locations is the registry this resource's references resolve against, present
	// only in the single-run representation.
	Locations []locationResource `json:"locations,omitempty"`
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
	// LocationID references the place the runs it fires run.
	LocationID string `json:"locationId"`
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

// fleetFrom projects a fleet onto its representation and reports the places that
// representation refers to. withNodes is false for a collection, where carrying every
// plan's whole graph would make an overview read grow with the size of the plans
// rather than with the number of fleets.
//
// The places are returned rather than gathered again by the caller: UpNext() derives
// and copies its answer on every call, and the nodes projected here are exactly the
// nodes whose locations the response refers to. In a collection the caller hands them
// all to one registry; the single-fleet representation carries its own.
func fleetFrom(fleet agenthub.Fleet, withNodes bool) (fleetResource, []agenthub.Location) {
	upNext := fleet.UpNext()
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
		LocationID:  fleet.Location.ID(),
		UpNext:      nodesFrom(upNext),
	}
	// The whole graph contains the up-next nodes, so the single-fleet representation
	// refers to the graph's places and nothing besides.
	referred := upNext
	if withNodes {
		resource.Nodes = nodesFrom(fleet.Nodes)
		referred = fleet.Nodes
	}
	locations := make([]agenthub.Location, 0, len(referred)+1)
	locations = append(locations, fleet.Location)
	for _, node := range referred {
		locations = append(locations, node.Location)
	}
	if withNodes {
		resource.Locations = locationsFrom(agenthub.NewLocationRegistry(locations...))
	}
	return resource, locations
}

// nodesFrom projects plan nodes onto their representation, preserving order.
func nodesFrom(nodes []agenthub.FleetNode) []fleetNodeResource {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]fleetNodeResource, 0, len(nodes))
	for _, node := range nodes {
		resource := fleetNodeResource{
			ID:         node.ID,
			Label:      node.ID,
			Prompt:     node.Prompt,
			DependsOn:  node.DependsOn,
			Status:     node.Status,
			LocationID: node.Location.ID(),
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

// runFrom projects a run onto its representation. withRegistry is true for the
// single-run representation, which has no envelope to carry the registry for it.
func runFrom(run agenthub.Run, withRegistry bool) runResource {
	resource := runResource{
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
		LocationID:  run.Location.ID(),
	}
	if withRegistry {
		resource.Locations = locationsFrom(agenthub.NewLocationRegistry(run.Location))
	}
	return resource
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
		LocationID:     schedule.Location.ID(),
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

// settingResource is one governed setting as the API publishes it: what it is here,
// and where that answer came from.
type settingResource struct {
	// Key is the governed setting, e.g. "steering.enabled".
	Key string `json:"key"`
	// Purpose is what the setting decides, for a person about to change it.
	Purpose string `json:"purpose"`
	// Enabled is the effective value.
	Enabled bool `json:"enabled"`
	// Source is the kind of scope the value came from — a place, the installation, or
	// the value this build ships. It is the kind and not the scope itself: a scope
	// names an absolute path on the server, and this field is read for "where was this
	// set", not for the machine's directory layout.
	Source string `json:"source"`
	// Version is which stored version answered, or 0 when the answer is what the build
	// ships and storage holds none.
	Version int `json:"version"`
}

// settingFrom projects a resolved setting onto its representation. The purpose comes
// from the catalogue, so the API describes a setting with the same words every other
// surface does.
func settingFrom(value setting.Value) settingResource {
	resource := settingResource{
		Key:     string(value.Key),
		Enabled: value.Enabled,
		Source:  value.Scope.Kind(),
		Version: value.Version,
	}
	if spec, ok := setting.SpecFor(value.Key); ok {
		resource.Purpose = spec.Purpose
	}
	return resource
}
