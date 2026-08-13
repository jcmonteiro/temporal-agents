// Package agenthub is the application core of the Agent Hub read API: the model
// an operator's overview is expressed in, the rules that derive it, and the ports
// it reaches its facts through.
//
// It exposes the model of the *work* — fleets, runs, schedules, and the plan graph
// a fleet executes — not the model of the systems underneath it. Nothing here
// mentions Temporal, SQL, or HTTP: the orchestrator's execution states arrive
// already translated into this package's own vocabulary (see ExecutionOutcome),
// the durable record arrives as the same neutral Execution type, and the HTTP
// representation of everything below is the transport adapter's business. That is
// what lets the first implementation reconstruct a fleet from the workflow-ID
// convention and a later one read it from a purpose-built table without either the
// contract or its consumers changing.
//
// Statuses are only ever *derived* from facts that exist (an execution's outcome,
// a plan's edges, a schedule's state). None of them is fabricated by instrumenting
// the workflows, and a status that has no source is not emitted.
package agenthub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid marks a failure caused by what was asked rather than by the state of
// the system: an unknown item kind, an identifier of the wrong shape, a limit
// outside the published bounds. The transport answers those as a refusal a consumer
// must change its request to fix, and never as a server failure.
var ErrInvalid = errors.New("invalid request")

// WorkStatus is the status vocabulary the overview is drawn in. It is a closed
// set: every item an operator sees is in exactly one of these states, and each
// has exactly one meaning across fleets, runs and schedules.
type WorkStatus string

const (
	// StatusTodo is work that is ready to start but has not started: a plan node
	// whose prerequisites are all done, or a schedule that has never fired.
	StatusTodo WorkStatus = "todo"
	// StatusInProgress is work an execution is currently running.
	StatusInProgress WorkStatus = "in-progress"
	// StatusPaused is work that will not proceed on its own but did not fail
	// itself: a plan node skipped because a prerequisite did not succeed, or a
	// paused schedule.
	StatusPaused WorkStatus = "paused"
	// StatusWaitingInput is work that stopped in a recoverable way and needs a
	// human: a node whose dependency merge conflicted, or whose review loop never
	// converged.
	StatusWaitingInput WorkStatus = "waiting-input"
	// StatusWaiting is work that is blocked purely on ordering: a plan node with a
	// prerequisite that has not finished yet.
	StatusWaiting WorkStatus = "waiting"
	// StatusDone is work that completed successfully.
	StatusDone WorkStatus = "done"
	// StatusFailed is work that ended unsuccessfully, including a cancellation, a
	// timeout and a termination.
	StatusFailed WorkStatus = "failed"
)

// WorkStatuses lists every status, in the order the legend reads, so a consumer
// (and the published schema) can enumerate the vocabulary instead of hard-coding
// it.
func WorkStatuses() []WorkStatus {
	return []WorkStatus{
		StatusTodo, StatusInProgress, StatusPaused, StatusWaitingInput,
		StatusWaiting, StatusDone, StatusFailed,
	}
}

// Terminal reports whether the status is an end state, i.e. nothing further will
// happen without an operator.
func (s WorkStatus) Terminal() bool {
	return s == StatusDone || s == StatusFailed
}

// ItemKind discriminates the three kinds of top-level item an overview shows.
// Each kind is a resource of its own, and identity means something different in
// each: a fleet is its parent workflow, a run is its workflow-ID chain, a
// schedule is the schedule itself.
type ItemKind string

const (
	// KindFleet is a fleet: one orchestrated plan graph.
	KindFleet ItemKind = "fleet"
	// KindRun is one independently represented workflow execution chain. It is
	// usually top-level, but can be a detached child whose parent no longer owns its
	// lifecycle.
	KindRun ItemKind = "run"
	// KindSchedule is a schedule, not one of the runs it fires.
	KindSchedule ItemKind = "schedule"
)

// ItemKinds lists every item kind in a stable order, for validation and for the
// published schema.
func ItemKinds() []ItemKind { return []ItemKind{KindFleet, KindRun, KindSchedule} }

// ValidItemKind reports whether kind is one of the three kinds, so a mistyped
// kind in a request is refused rather than silently matching nothing.
func ValidItemKind(kind ItemKind) bool {
	for _, k := range ItemKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// ExecutionOutcome is how one execution stands, in this package's own vocabulary.
// Adapters translate their source's states into it — the orchestrator's
// cancellations, timeouts and terminations all arrive as OutcomeFailed, a
// continued-as-new iteration as OutcomeRunning — so the derivation rules below
// never branch on a foreign enum.
type ExecutionOutcome string

const (
	// OutcomeRunning means the execution has not settled yet.
	OutcomeRunning ExecutionOutcome = "running"
	// OutcomeSucceeded means it settled without error.
	OutcomeSucceeded ExecutionOutcome = "succeeded"
	// OutcomeFailed means it settled with an error, was cancelled, timed out or
	// was terminated.
	OutcomeFailed ExecutionOutcome = "failed"
	// OutcomeBlocked means it stopped in a recoverable way that needs a human: the
	// node could not integrate its dependencies' work, or its review never
	// converged.
	OutcomeBlocked ExecutionOutcome = "blocked"
	// OutcomeSkipped means the unit of work never ran because a prerequisite did
	// not succeed. Only a plan node is skipped, and a skipped node has no
	// execution of its own.
	OutcomeSkipped ExecutionOutcome = "skipped"
)

// WorkStatus maps an execution outcome onto the status vocabulary. It is the only
// place the two vocabularies meet.
func (o ExecutionOutcome) WorkStatus() WorkStatus {
	switch o {
	case OutcomeRunning:
		return StatusInProgress
	case OutcomeSucceeded:
		return StatusDone
	case OutcomeBlocked:
		return StatusWaitingInput
	case OutcomeSkipped:
		return StatusPaused
	default:
		// An unknown outcome is reported as failed rather than invented into a
		// friendlier state: the API never claims progress it cannot evidence.
		return StatusFailed
	}
}

// Progress is how much of a fleet's plan is done. Skipped and blocked nodes count
// in the denominator but never in the numerator: they are work the plan still
// contains and that nothing has completed.
type Progress struct {
	// Done is how many plan nodes completed successfully.
	Done int
	// Total is how many nodes the plan has.
	Total int
}

// Fraction returns the completed share of the plan in [0,1], and 0 for an empty
// plan (rather than a division by zero).
func (p Progress) Fraction() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Done) / float64(p.Total)
}

// Plan is a fleet's approved dependency graph, as the API models it: a goal and
// the nodes that decompose it. It deliberately mirrors — rather than reuses — the
// orchestration core's plan type, so the published contract does not move when an
// internal type does.
type Plan struct {
	// Goal is the high-level prompt the plan decomposes.
	Goal string
	// Nodes are the plan's units of work.
	Nodes []PlanNode
}

// PlanNode is one unit of work in a plan.
type PlanNode struct {
	// ID identifies the node within its plan.
	ID string
	// Prompt is the instruction the node's execution was handed.
	Prompt string
	// DependsOn lists the IDs of the nodes that must succeed before this one runs.
	// An edge is both an ordering and a layering rule.
	DependsOn []string
}

// Fleet is one fleet satellite: an orchestrated plan, its already-aggregated
// status, its derived progress, and its nodes.
//
// Status and Progress are computed here, on the server, so every consumer reads
// the same numbers rather than each re-deriving them from the node list.
type Fleet struct {
	// ID is the fleet's identity: its parent workflow ID.
	ID string
	// Running reports whether the parent fleet execution is unsettled. It is
	// separate from Status, which aggregates node states.
	Running bool
	// Goal is the plan's goal, and the fleet's label.
	Goal string
	// PlanID is the stored plan the fleet executes, when it is known.
	PlanID string
	// Status is the aggregated status of the whole fleet (see AggregateStatus).
	Status WorkStatus
	// Progress is done nodes over total nodes.
	Progress Progress
	// StartedAt is when the fleet started.
	StartedAt time.Time
	// EndedAt is when it settled, or the zero time while it is still running.
	EndedAt time.Time
	// Nodes are the plan's nodes with their derived status and execution, in plan
	// order. It is empty when the fleet's plan could not be resolved, in which case
	// the fleet still reports its own execution's status.
	Nodes []FleetNode
	// Location is where the fleet runs. The zero value is the unknown place, so a
	// fleet whose place was never recorded reports the unknown one rather than none.
	Location Location
}

// Dismissible reports whether the fleet may be dismissed from the overview.
// Every observed fleet state may be hidden until that state changes.
func (f Fleet) Dismissible() bool { return true }

// StateRevision identifies the fleet state an operator reviewed. Any observable
// change produces a different revision, so an old dismissal cannot hide new work.
func (f Fleet) StateRevision() string {
	parts := []string{
		f.ID, strconv.FormatBool(f.Running), f.Goal, f.PlanID, string(f.Status),
		strconv.Itoa(f.Progress.Done), strconv.Itoa(f.Progress.Total),
		timeRevision(f.StartedAt), timeRevision(f.EndedAt), f.Location.ID(),
	}
	for _, node := range f.Nodes {
		parts = append(parts,
			node.ID, node.Prompt, string(node.Status), node.Location.ID(),
			strconv.Itoa(len(node.DependsOn)),
		)
		parts = append(parts, node.DependsOn...)
		if node.Execution == nil {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, node.Execution.WorkflowID, node.Execution.RunID,
			timeRevision(node.Execution.StartedAt), timeRevision(node.Execution.EndedAt),
			strconv.Itoa(node.Execution.Tokens))
	}
	return stateRevision(parts...)
}

// UpNext returns the nodes that have not started, prerequisites-ready first
// (todo) and then merely waiting, in plan order within each group. It is the
// server-side answer to the overview's "up next" queue, so the queue's meaning
// cannot drift between consumers.
func (f Fleet) UpNext() []FleetNode {
	var todo, waiting []FleetNode
	for _, n := range f.Nodes {
		switch n.Status {
		case StatusTodo:
			todo = append(todo, n)
		case StatusWaiting:
			waiting = append(waiting, n)
		}
	}
	return append(todo, waiting...)
}

// FleetNode is one plan node together with what happened to it.
type FleetNode struct {
	// ID is the node's ID within the plan.
	ID string
	// Prompt is the instruction the node runs.
	Prompt string
	// DependsOn lists the nodes this one runs after.
	DependsOn []string
	// Status is the node's derived status (see DeriveNodeStatuses).
	Status WorkStatus
	// Execution is the child execution the node ran in, or nil when it never
	// started.
	Execution *NodeExecution
	// Location is where the node runs. A node genuinely differs from its fleet: it
	// can develop in a worktree of its own.
	Location Location
}

// NodeExecution is the child execution a plan node ran in. It carries only what
// the API can evidence: the identifiers, the timestamps and the node's own token
// usage.
type NodeExecution struct {
	// WorkflowID is the child execution's workflow ID ("<fleetID>-<nodeID>").
	WorkflowID string
	// RunID is the latest iteration's run ID.
	RunID string
	// StartedAt is when the child started.
	StartedAt time.Time
	// EndedAt is when it settled, or the zero time while it runs.
	EndedAt time.Time
	// Tokens is the child's own token usage, or 0 when it reported none.
	Tokens int
}

// RunType says which command produced a run satellite. It is descriptive only —
// every run is the same kind of item — and lets a consumer label a satellite
// without parsing its ID.
type RunType string

const (
	// RunTypePrompt is a `run` (or a `template run`).
	RunTypePrompt RunType = "run"
	// RunTypeDevelop is a `code develop`.
	RunTypeDevelop RunType = "develop"
	// RunTypeReview is a `code review`.
	RunTypeReview RunType = "review"
	// RunTypePilot is a `code pilot`.
	RunTypePilot RunType = "pilot"
	// RunTypeFleetPlan is a `fleet plan`: the agent run that proposes a plan.
	RunTypeFleetPlan RunType = "fleet-plan"
)

// Run is one independently represented execution chain: a satellite that is not
// part of a fleet or owned by a supervising parent.
//
// Identity is the workflow ID, which is stable across a chained run's
// continue-as-new iterations, so a chain that has looped a thousand times is one
// satellite showing its latest iteration's status — never one satellite per
// retained iteration.
type Run struct {
	// ID is the chain's identity: its workflow ID.
	ID string
	// Running reports whether the latest chain iteration is unsettled.
	Running bool
	// Type is which command started it.
	Type RunType
	// Label is what was asked of the agent, i.e. the run's prompt.
	Label string
	// Status is the latest iteration's status.
	Status WorkStatus
	// StartedAt is when the chain's earliest known iteration started.
	StartedAt time.Time
	// EndedAt is when the latest iteration settled, or the zero time while it runs.
	EndedAt time.Time
	// Iterations is how many continue-as-new iterations of this chain are known,
	// which is how a consumer can show "looping" without seeing one item per loop.
	Iterations int
	// Tokens is the token usage summed over the chain's known iterations.
	Tokens int
	// Location is where the run runs.
	Location Location
	// StartedBy identifies who started the run from the hub, and is empty for a run
	// started from the command line or by a schedule: the hub records who asked, and
	// never invents an operator for work nobody asked it for.
	StartedBy string
	// Instructions is which stored instruction the run resolved for each governed
	// key, so "which instruction produced this" stays answerable.
	Instructions []InstructionUse
}

// Dismissible reports whether the run may be dismissed from the overview.
// Every observed run state may be hidden until that state changes.
func (r Run) Dismissible() bool { return true }

// StateRevision identifies the run state an operator reviewed. It covers every
// fact published in the collection, plus liveness, so a changed iteration, outcome,
// cost, label, or place makes a dismissed run visible again.
func (r Run) StateRevision() string {
	return stateRevision(
		r.ID, strconv.FormatBool(r.Running), string(r.Type), r.Label, string(r.Status),
		timeRevision(r.StartedAt), timeRevision(r.EndedAt), strconv.Itoa(r.Iterations),
		strconv.Itoa(r.Tokens), r.Location.ID(),
	)
}

func stateRevision(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func timeRevision(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// Schedule is one schedule satellite. A schedule is recurring, so it is never
// "done": its status describes its latest action, and it carries no progress —
// there is no finite amount of work to be a fraction of.
type Schedule struct {
	// ID is the schedule's identity.
	ID string
	// Label is what the scheduled run asks of the agent, when it is known.
	Label string
	// Spec is a human-readable rendering of when the schedule fires.
	Spec string
	// Status is derived per ScheduleStatus.
	Status WorkStatus
	// Paused reports whether the schedule is currently paused.
	Paused bool
	// RunningActions is how many of its runs are in flight right now.
	RunningActions int
	// LastRunAt is when it last fired, or the zero time when it never has.
	LastRunAt time.Time
	// NextRunAt is when it will fire next, or the zero time when it is paused or
	// has no further action.
	NextRunAt time.Time
	// Location is where the runs it fires run.
	Location Location
}

// Dismissible reports whether the schedule may be dismissed. It never can: a
// schedule has no terminal state, so hiding it would hide live configuration.
func (s Schedule) Dismissible() bool { return false }

// ActiveWorkType identifies how one active-work item is shown. Fleet and schedule
// are top-level resource types. The other values are standalone run types.
type ActiveWorkType string

const (
	ActiveWorkFleet     ActiveWorkType = "fleet"
	ActiveWorkRun       ActiveWorkType = "run"
	ActiveWorkDevelop   ActiveWorkType = "develop"
	ActiveWorkReview    ActiveWorkType = "review"
	ActiveWorkPilot     ActiveWorkType = "pilot"
	ActiveWorkFleetPlan ActiveWorkType = "fleet-plan"
	ActiveWorkSchedule  ActiveWorkType = "schedule"
)

// ActiveWorkTypes lists the closed active-work type vocabulary.
func ActiveWorkTypes() []ActiveWorkType {
	return []ActiveWorkType{
		ActiveWorkFleet, ActiveWorkRun, ActiveWorkDevelop, ActiveWorkReview,
		ActiveWorkPilot, ActiveWorkFleetPlan, ActiveWorkSchedule,
	}
}

// ActiveWorkItem is one top-level item returned by the additive active-work use
// case. Execution items publish only the facts from their bounded live source
// page. Schedule items publish configuration and the last observed outcome, but
// make no claim about current action liveness.
type ActiveWorkItem struct {
	ID      string
	Type    ActiveWorkType
	Status  WorkStatus
	Running bool
	// Location is where the item runs. It is published as an optional reference, so
	// the CLI's paged contract gains a field and loses none.
	Location Location
}

// PageQuery selects one bounded page. Cursor is an opaque value returned by the
// previous page and is empty for the first page.
type PageQuery struct {
	Limit  int
	Cursor []byte
}

// Page is one collection page. Next is empty on the final page.
type Page[T any] struct {
	Items []T
	Next  []byte
}

// ViewerID is the stable identity whose private view state is being read. The
// local value covers the explicit unauthenticated mode without turning it into
// shared state with any authenticated principal.
type ViewerID string

const LocalViewerID ViewerID = "local-operator"

// Dismissal records that one viewer dismissed one observed state of an item. It
// is view state: it hides an item, and never touches the work.
type Dismissal struct {
	// Viewer is the only principal this dismissal applies to.
	Viewer ViewerID
	// Kind is the kind of item dismissed, which is part of its identity: a fleet
	// and a run could in principle carry the same ID.
	Kind ItemKind
	// ItemID is the item's stable identity — a fleet's parent workflow ID or a
	// run chain's workflow ID.
	ItemID string
	// StateRevision is the exact state the viewer acknowledged. A changed item has
	// a different revision and is visible again without deleting this record first.
	StateRevision string
	// DismissedAt is when it was dismissed.
	DismissedAt time.Time
}

// ID is the dismissal's own identifier, and the last path segment of its
// resource: "<kind>:<itemID>". It is derived rather than generated so that
// dismissing the same item twice is the same resource, which is what makes the
// write idempotent for a client that retries.
func (d Dismissal) ID() string { return string(d.Kind) + ":" + d.ItemID }

// itemIDPattern constrains the item identity a dismissal may reference. Every ID
// the tool mints (a workflow ID, a schedule ID) is a slug of this shape, so the
// constraint costs nothing and keeps a hostile or mistaken value out of both the
// store and the URL of the resource it creates.
const maxItemIDLength = 255

// ParseDismissalID splits a dismissal identifier back into the kind and item it
// refers to, rejecting anything that is not a well-formed identifier of a valid
// kind.
func ParseDismissalID(id string) (ItemKind, string, error) {
	kind, itemID, found := strings.Cut(id, ":")
	if !found {
		return "", "", fmt.Errorf("%w: the dismissal id %q must be of the form <kind>:<itemId>", ErrInvalid, id)
	}
	if err := ValidateDismissalTarget(ItemKind(kind), itemID); err != nil {
		return "", "", err
	}
	return ItemKind(kind), itemID, nil
}

// ValidateItemID checks that an identity can be addressed as one path segment. Every
// ID the tool mints is a slug of this shape. Applying the same rule to reads and
// writes keeps malformed input away from every adapter, not only from the dismissal
// table.
func ValidateItemID(itemID string) error {
	if strings.TrimSpace(itemID) == "" {
		return fmt.Errorf("%w: the item id is required", ErrInvalid)
	}
	if !utf8.ValidString(itemID) || utf8.RuneCountInString(itemID) > maxItemIDLength {
		return fmt.Errorf("%w: the item id must be at most %d characters", ErrInvalid, maxItemIDLength)
	}
	if strings.ContainsAny(itemID, "/?#%") || strings.IndexFunc(itemID, invalidItemIDRune) >= 0 {
		return fmt.Errorf("%w: the item id %q contains characters that cannot appear in an identifier", ErrInvalid, itemID)
	}
	return nil
}

func invalidItemIDRune(r rune) bool {
	return unicode.IsControl(r) || unicode.IsSpace(r) || r == '\ufeff'
}

// ValidateDismissalTarget checks that kind and itemID can identify a dismissible
// item at all, independently of whether such an item currently exists.
func ValidateDismissalTarget(kind ItemKind, itemID string) error {
	if !ValidItemKind(kind) {
		return fmt.Errorf("%w: unknown item kind %q", ErrInvalid, kind)
	}
	if kind == KindSchedule {
		return fmt.Errorf("%w: a schedule cannot be dismissed", ErrInvalid)
	}
	return ValidateItemID(itemID)
}

// AggregateStatus reduces a fleet's node statuses to the fleet's own status by a
// fixed precedence, first match wins:
//
//  1. no nodes                                  -> todo
//  2. any node failed                           -> failed
//  3. any node needs a human (waiting-input)    -> waiting-input
//  4. any node in progress                      -> in-progress
//  5. any node paused (a skipped node)          -> paused
//  6. every node done                           -> done
//  7. otherwise: in-progress if anything is done, else todo
//
// The precedence is part of the published contract: the server aggregates so that
// two consumers cannot disagree about what a half-failed fleet is, and the order
// puts the states that need an operator's attention ahead of the ones that do
// not.
func AggregateStatus(nodes []FleetNode) WorkStatus {
	if len(nodes) == 0 {
		return StatusTodo
	}
	var anyDone, anyOther bool
	counts := map[WorkStatus]int{}
	for _, n := range nodes {
		counts[n.Status]++
		if n.Status == StatusDone {
			anyDone = true
		} else {
			anyOther = true
		}
	}
	switch {
	case counts[StatusFailed] > 0:
		return StatusFailed
	case counts[StatusWaitingInput] > 0:
		return StatusWaitingInput
	case counts[StatusInProgress] > 0:
		return StatusInProgress
	case counts[StatusPaused] > 0:
		return StatusPaused
	case !anyOther:
		return StatusDone
	case anyDone:
		return StatusInProgress
	default:
		return StatusTodo
	}
}

// NodeProgress counts the done nodes over the total (see Progress).
func NodeProgress(nodes []FleetNode) Progress {
	done := 0
	for _, n := range nodes {
		if n.Status == StatusDone {
			done++
		}
	}
	return Progress{Done: done, Total: len(nodes)}
}

// DeriveNodeStatuses derives a status for every node of plan, given the outcomes
// of the nodes that have one (from their child execution, or from the fleet's
// recorded per-node breakdown).
//
// A node with an outcome takes that outcome's status. A node without one is
// explained by its dependencies, which is the only honest source for it:
//
//   - a dependency that failed, was skipped or needs a human -> paused (this node
//     was skipped, or will be)
//   - every dependency done                                  -> todo (runnable)
//   - otherwise                                              -> waiting
//
// Nodes are resolved in dependency order so a chain of not-yet-started nodes
// behind a failed prerequisite is all paused, not just the first one. A node
// caught in a dependency cycle (which a validated plan cannot contain, but a
// reader must not hang on) is reported as waiting.
func DeriveNodeStatuses(plan Plan, outcomes map[string]ExecutionOutcome) map[string]WorkStatus {
	statuses := make(map[string]WorkStatus, len(plan.Nodes))
	nodes := make(map[string]PlanNode, len(plan.Nodes))
	for _, n := range plan.Nodes {
		nodes[n.ID] = n
		if outcome, ok := outcomes[n.ID]; ok {
			statuses[n.ID] = outcome.WorkStatus()
		}
	}
	for _, id := range dependencyOrder(plan) {
		if _, decided := statuses[id]; decided {
			continue
		}
		statuses[id] = statusFromDependencies(nodes[id], statuses)
	}
	// Anything left is in a cycle: no dependency order reached it. It is waiting on
	// something that cannot complete, which "waiting" describes without claiming
	// more than is known.
	for _, n := range plan.Nodes {
		if _, decided := statuses[n.ID]; !decided {
			statuses[n.ID] = StatusWaiting
		}
	}
	return statuses
}

// statusFromDependencies explains a node that never started by the state of the
// nodes it runs after. Dependencies are resolved before their dependents (see
// dependencyOrder), so the statuses map already holds every answer it needs.
func statusFromDependencies(node PlanNode, statuses map[string]WorkStatus) WorkStatus {
	ready := true
	for _, dep := range node.DependsOn {
		switch statuses[dep] {
		case StatusFailed, StatusPaused, StatusWaitingInput:
			// A prerequisite that did not succeed is never going to: this node is
			// skipped rather than merely waiting.
			return StatusPaused
		case StatusDone:
			// Satisfied; keep looking at the rest.
		default:
			ready = false
		}
	}
	if ready {
		return StatusTodo
	}
	return StatusWaiting
}

// dependencyOrder lists node IDs so that every node comes after the nodes it
// depends on, breaking ties by ID for a deterministic result. Nodes in a
// dependency cycle are omitted: there is no position that satisfies their edges,
// and the caller reports them as waiting rather than looping forever.
func dependencyOrder(plan Plan) []string {
	remaining := make(map[string][]string, len(plan.Nodes))
	for _, n := range plan.Nodes {
		remaining[n.ID] = n.DependsOn
	}
	placed := make(map[string]bool, len(plan.Nodes))
	order := make([]string, 0, len(plan.Nodes))
	for len(placed) < len(remaining) {
		var ready []string
		for id, deps := range remaining {
			if placed[id] {
				continue
			}
			satisfied := true
			for _, dep := range deps {
				// An edge to a node that is not in the plan cannot be satisfied and cannot
				// be waited for either; treat it as satisfied so the rest of the graph is
				// still ordered.
				if _, known := remaining[dep]; known && !placed[dep] {
					satisfied = false
					break
				}
			}
			if satisfied {
				ready = append(ready, id)
			}
		}
		if len(ready) == 0 {
			// Every remaining node is in a cycle.
			break
		}
		sort.Strings(ready)
		for _, id := range ready {
			placed[id] = true
			order = append(order, id)
		}
	}
	return order
}

// ScheduleStatus derives a schedule's status from its state, in this order: a
// paused schedule is paused, a schedule with an action in flight is in progress,
// otherwise it takes the outcome of its most recent completed action, and a
// schedule that has never fired is todo.
//
// Pausedness comes first deliberately: a paused schedule with a stale successful
// action is paused, not done — what an operator needs to see is that it will not
// fire.
func ScheduleStatus(paused bool, runningActions int, lastOutcome ExecutionOutcome) WorkStatus {
	switch {
	case paused:
		return StatusPaused
	case runningActions > 0:
		return StatusInProgress
	case lastOutcome == "":
		return StatusTodo
	default:
		return lastOutcome.WorkStatus()
	}
}
