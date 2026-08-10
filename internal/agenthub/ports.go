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
	// FirstRunID identifies the whole continue-as-new chain. It distinguishes
	// separate schedule firings that reuse one workflow ID.
	FirstRunID string
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
	// Instructions is which stored instruction each governed key of this execution
	// resolved to. Only the durable record has it, and it is empty for an execution
	// that resolves none — and for one that ran before instructions were stored.
	Instructions []InstructionUse
	// Place is what was recorded about where the execution ran. Only the durable
	// record has it — the orchestrator knows what is running, not where — and it is
	// the zero value for an execution whose place was never established, which the
	// core reads as the unknown place.
	Place RecordedPlace
	// WaitingSince is when the execution started waiting for an operator's decision,
	// and the zero time whenever it is not waiting. Only the durable record has it:
	// to the orchestrator a paused round is a workflow that is running, which is
	// exactly what it is — but not what an operator needs to be told.
	WaitingSince time.Time
}

// Status is how the execution stands in the overview's vocabulary.
//
// It is the outcome's status, except for the one thing an outcome cannot express: a
// running execution that has stopped for a human is not making progress, and saying
// "in progress" about it would hide the decision it is waiting for. The waiting
// round's own session is never an item of its own, so this is the only place that
// work is visible.
func (e Execution) Status() WorkStatus {
	if e.Outcome == OutcomeRunning && !e.WaitingSince.IsZero() {
		return StatusWaitingInput
	}
	return e.Outcome.WorkStatus()
}

// InstructionUse is one instruction a unit of work ran under: which governed
// instruction, where the value that won came from, and which version of it.
//
// The text is deliberately absent. "Which instruction produced this" is answered by
// naming the version, not by copying it: a copy would grow every record with the
// text it used and would drift from the version it claims to be. The hash makes the
// naming verifiable.
type InstructionUse struct {
	// Key is the governed instruction ("review.perform").
	Key string
	// Scope is where the value that won came from ("global", "factory",
	// "directory:<path>").
	Scope string
	// Version is which version of that key and scope was used, or 0 when the value
	// the build ships answered because storage held none.
	Version int
	// Hash is the content hash of the instruction text that was used.
	Hash string
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

// ExecutionPageQuery selects one bounded source-native page of running
// executions. Cursor is opaque to the application core.
type ExecutionPageQuery struct {
	Limit  int
	Cursor []byte
}

// ExecutionPage is one source-native page of running executions.
type ExecutionPage struct {
	Items []Execution
	Next  []byte
}

// ExecutionSource is the driven port for the live orchestration state: what is
// running right now. It exists so the core never depends on an orchestration SDK,
// and so the same core can be driven by a purpose-built projection later.
type ExecutionSource interface {
	// RunningExecutions returns the executions that are in flight, capped at limit.
	RunningExecutions(ctx context.Context, limit int) ([]Execution, error)
	// RunningPage returns exactly one bounded source page. It does not materialize
	// all running executions before it applies the limit.
	RunningPage(ctx context.Context, query ExecutionPageQuery) (ExecutionPage, error)
	// Execution returns the current state of one execution chain's latest
	// iteration, or ErrNoExecution when the orchestrator does not know it.
	Execution(ctx context.Context, workflowID string) (Execution, error)
	// Executions resolves many latest chain states in one adapter operation. Unknown
	// executions are omitted. Collection reconciliation uses it to avoid one
	// sequential adapter call per stale durable record.
	Executions(ctx context.Context, workflowIDs []string) (map[string]Execution, error)
}

// RecordSource is the driven port for individual durable execution trees. It is
// used by item detail reads; collection selection and aggregation use
// CollectionSource so a row limit can never become a resource limit by accident.
type RecordSource interface {
	// RecordedExecutions returns the recorded executions matching q, newest first.
	RecordedExecutions(ctx context.Context, q RecordQuery) ([]Execution, error)
}

// ChainQuery selects execution-chain resources before a collection limit is
// applied. ExcludedWorkflowIDs are dismissals that must be removed by the adapter
// before selection. RequiredWorkflowIDs are fully aggregated in addition to the
// normal page, so established collection reads can merge current live state
// without creating a partial chain from only its latest iteration.
type ChainQuery struct {
	WorkflowID          string
	RequiredWorkflowIDs []string
	ExcludedWorkflowIDs []string
	Limit               int
}

// ExecutionChain is one fully aggregated continue-as-new chain.
type ExecutionChain struct {
	Latest     Execution
	StartedAt  time.Time
	Iterations int
	Tokens     int
}

// FleetTree carries one selected fleet chain and all direct child execution rows.
type FleetTree struct {
	Chain      ExecutionChain
	Executions []Execution
}

// CollectionSource is the driven port for collection-oriented record reads. The
// adapter selects resource identities before limits and aggregates every row that
// belongs to those resources.
type CollectionSource interface {
	RunChains(ctx context.Context, query ChainQuery) ([]ExecutionChain, error)
	FleetTrees(ctx context.Context, query ChainQuery) ([]FleetTree, error)
	ScheduleActionChains(ctx context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]ExecutionChain, error)
}

// PlanReference identifies the stored plan used by one fleet.
type PlanReference struct {
	FleetID string
	PlanID  string
}

// PlanSource is the driven port that resolves fleets' approved plans by their
// fleet's ID. Keeping it a port of its own is what lets the plan come from the
// plan store today and from anywhere else tomorrow without touching the readers
// that reconcile a plan against its executions.
type PlanSource interface {
	// Plans returns every resolvable plan, keyed by fleet ID. Missing plans are
	// omitted so a fleet can still be shown without an invented graph.
	Plans(ctx context.Context, refs []PlanReference) (map[string]Plan, error)
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

// SchedulePageQuery selects one bounded source-native schedule page.
type SchedulePageQuery struct {
	Limit  int
	Cursor []byte
}

// ScheduleStatePage is one source-native page of schedule states.
type ScheduleStatePage struct {
	Items []ScheduleState
	Next  []byte
}

// ScheduleSource is the driven port for the schedules. It is separate from
// ExecutionSource because a schedule is not an execution: it has no outcome of its
// own, and it exists while nothing at all is running.
type ScheduleSource interface {
	// Schedules returns the configured schedules, capped at limit.
	Schedules(ctx context.Context, limit int) ([]ScheduleState, error)
	// SchedulePage returns exactly one bounded source page.
	SchedulePage(ctx context.Context, query SchedulePageQuery) (ScheduleStatePage, error)
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
