package agenthub

import (
	"context"
	"errors"
	"time"

	"temporal-agents/internal/wfid"
)

// ErrNotFound is returned when a requested item does not exist, so the transport
// can answer 404 without inspecting adapter-specific errors.
var ErrNotFound = errors.New("no such item")

// ErrNoPlan is returned by a PlanSource for a fleet whose plan cannot be
// resolved. It is not a failure of the read: a fleet whose plan is unknown is
// still reported, with its own execution's status and no nodes, rather than
// omitted or invented.
var ErrNoPlan = errors.New("no plan for this fleet")

// ErrNoExecution is returned by an ExecutionSource asked about an execution the
// orchestrator does not know. It is how the service tells "still running" apart
// from "recorded as running, but gone".
var ErrNoExecution = errors.New("no such execution")

// Execution is one execution of some unit of work, in the API's own vocabulary.
// Both sides of the read path speak it: the live orchestration state and the
// durable record. Which fields a source fills differs — the live source knows
// what is running now, the record knows what was asked and what it cost — and the
// service joins the two by workflow ID (see MergeExecutions).
type Execution struct {
	// WorkflowID identifies the execution chain. It is stable across
	// continue-as-new iterations, which is what makes it the identity of a run
	// satellite.
	WorkflowID string
	// RunID identifies this single iteration, and is empty when the source does not
	// distinguish iterations.
	RunID string
	// Class is what the execution is (a fleet parent, a fleet node, a run, ...),
	// classified from the workflow-ID convention.
	Class wfid.Class
	// Outcome is how the execution stands.
	Outcome ExecutionOutcome
	// Label is what was asked: the prompt, the develop instruction or the fleet's
	// goal. It is empty when the source does not know it.
	Label string
	// StartedAt is when the execution started.
	StartedAt time.Time
	// EndedAt is when it settled, or the zero time while it is running.
	EndedAt time.Time
	// Tokens is the execution's own token usage, never an inclusive total, so
	// summing a chain's iterations cannot double-count.
	Tokens int
	// ScheduleID is the schedule that fired this execution, or empty when it was
	// started directly. A schedule-fired run is represented by its schedule, so it
	// is not a run satellite of its own.
	ScheduleID string
	// ParentWorkflowID is the execution that started this one as a child, or empty
	// for a top-level execution.
	ParentWorkflowID string
	// PlanID is the stored plan a fleet execution executes, when the source knows
	// it.
	PlanID string
	// NodeOutcomes is a fleet parent's per-node breakdown, which only the durable
	// record has and which is the sole home of a skipped node's outcome: a skipped
	// node starts no execution, so it has none of its own.
	NodeOutcomes []NodeOutcome
}

// Running reports whether the execution has not settled.
func (e Execution) Running() bool { return e.Outcome == OutcomeRunning }

// NodeOutcome is one plan node's outcome inside a fleet parent's record.
type NodeOutcome struct {
	// NodeID is the plan node the outcome belongs to.
	NodeID string
	// Outcome is what happened to it.
	Outcome ExecutionOutcome
}

// RecordQuery narrows a durable-record read. Zero-valued fields impose no
// constraint.
type RecordQuery struct {
	// Class, when set, keeps only executions of that class.
	Class wfid.Class
	// WorkflowID, when set, keeps one execution together with its children, so a
	// fleet and its nodes come back in a single read.
	WorkflowID string
	// ScheduleID, when set, keeps only the executions that schedule fired, which is
	// how a schedule's label and latest outcome are recovered once the orchestrator
	// no longer retains the action.
	ScheduleID string
	// Limit caps how many executions are returned. A non-positive limit lets the
	// adapter apply its own default.
	Limit int
}

// ExecutionSource is the driven port for the live orchestration state: what is
// running right now. It exists so the core never depends on an orchestration SDK,
// and so the same core can be driven by a purpose-built projection later.
type ExecutionSource interface {
	// RunningExecutions returns the executions that are in flight, capped at limit.
	RunningExecutions(ctx context.Context, limit int) ([]Execution, error)
	// Execution returns the current state of one execution chain's latest
	// iteration, or ErrNoExecution when the orchestrator does not know it (its
	// retention has passed, or it was terminated and never settled). It is asked
	// only about executions the durable record still calls running, so the API does
	// not report work as in progress that nothing is running.
	Execution(ctx context.Context, workflowID string) (Execution, error)
}

// RecordSource is the driven port for the durable execution record: the memory
// that outlives the orchestrator's retention. It is what lets a finished item stay
// on the overview until an operator dismisses it.
type RecordSource interface {
	// RecordedExecutions returns the recorded executions matching q, newest first.
	RecordedExecutions(ctx context.Context, q RecordQuery) ([]Execution, error)
}

// PlanSource is the driven port that resolves a fleet's approved plan by the
// fleet's ID. Keeping it a port of its own is what lets the plan come from the
// plan store today and from anywhere else tomorrow without touching the readers
// that reconcile a plan against its executions.
type PlanSource interface {
	// PlanFor returns the plan the fleet executes, or ErrNoPlan when it cannot be
	// resolved.
	PlanFor(ctx context.Context, fleetID string) (Plan, error)
}

// ScheduleState is one schedule as the orchestration source knows it, before the
// status derivation is applied.
type ScheduleState struct {
	// ID is the schedule's identity.
	ID string
	// Spec is a human-readable rendering of when it fires.
	Spec string
	// Paused reports whether it is paused.
	Paused bool
	// RunningActions is how many of its actions are in flight.
	RunningActions int
	// LastRunAt is when it last fired, or the zero time when it never has.
	LastRunAt time.Time
	// NextRunAt is when it fires next, or the zero time when there is no next
	// action.
	NextRunAt time.Time
	// LastOutcome is how its most recent completed action ended, or empty when it
	// has never completed one.
	LastOutcome ExecutionOutcome
}

// ScheduleSource is the driven port for the schedules. It is separate from
// ExecutionSource because a schedule is not an execution: it has no outcome of its
// own, and it exists while nothing at all is running.
type ScheduleSource interface {
	// Schedules returns the configured schedules, capped at limit.
	Schedules(ctx context.Context, limit int) ([]ScheduleState, error)
}

// DismissalStore is the driven port for the operator's dismissals: the only
// mutable state this API owns, kept in a port of its own so the read path stays
// read-only by construction.
type DismissalStore interface {
	// Dismissals returns every dismissal currently in force.
	Dismissals(ctx context.Context) ([]Dismissal, error)
	// Dismiss records d and returns the stored dismissal. It must be idempotent on
	// the dismissal's identity (kind plus item), so a retry returns the original
	// timestamp rather than creating or reporting a different resource.
	Dismiss(ctx context.Context, d Dismissal) (Dismissal, error)
	// Undismiss removes the dismissal of one item, and reports ErrNotFound when
	// there was none, so the transport can tell a no-op apart from a deletion.
	Undismiss(ctx context.Context, kind ItemKind, itemID string) error
}
