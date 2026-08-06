// Package execstore owns the durable record of what the tool has executed: the
// port (repository interface) the application core persists through and the
// record types that cross it.
//
// Temporal's event history is retention-limited and wiped whenever its own
// datastore is reset, so it cannot answer "what have I run, with what outcome,
// at what token cost?" over time. The records here are that lasting memory: one
// row per Temporal run ID, written when a workflow starts and updated when it
// settles, plus the fleet plans an operator approves.
//
// The package follows the same hexagonal split as codereview and fleet, and
// enforces it with the compiler: this package is the port and the record types
// (standard library only, no SQL and no driver dependency at all), while the
// driven adapter lives in the nested execpg package. Only main imports execpg, so
// a domain package cannot start depending on pgx by accident. The
// Persist<Type>WorkflowState activities that drive the port live next to the
// workflows that call them (the root bundle for PromptWorkflow,
// codereview.Activities, fleet.Activities), so no SQL or pgx type ever reaches
// workflow or domain code.
package execstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotConfigured is returned by an activity asked to persist without a store
// wired in. It turns a misconfigured worker into a clear activity failure (and,
// because recording must succeed, a failed workflow) instead of a nil-pointer
// panic.
var ErrNotConfigured = errors.New("execution store is not configured (is DATABASE_URL set?)")

// ErrNotMigrated is returned by a read against a database whose schema has not
// been created yet. Only the worker applies migrations, so a reader that runs
// first gets this instead of a raw "relation does not exist" from Postgres.
var ErrNotMigrated = errors.New("the execution store schema does not exist yet; start the worker once to create it")

// ErrNoSuchPlan is returned when a plan handle resolves to nothing, so a caller
// can tell "no such plan" apart from a store outage and abort with a precise
// message either way.
var ErrNoSuchPlan = errors.New("no such plan")

// Kind discriminates which command produced a record. It doubles as the label
// `history` prints, and matches the classification the live `list` view derives
// from workflow-ID prefixes.
type Kind string

const (
	// KindRun is a PromptWorkflow execution: a `run`, a `template run`, or a
	// schedule-fired run (told apart by a non-empty ScheduleID rather than by a
	// kind of its own).
	KindRun Kind = "run"
	// KindDevelop is a DevelopWorkflow execution, whether standalone (`code
	// develop`) or a fleet node (told apart by ParentWorkflowID).
	KindDevelop Kind = "develop"
	// KindReview is a ReviewWorkflow execution, whether standalone (`code review`)
	// or the child of a develop run (told apart by ParentWorkflowID).
	KindReview Kind = "review"
	// KindPilot is a PilotWorkflow execution.
	KindPilot Kind = "pilot"
	// KindFleet is a FleetWorkflow execution: the `fleet execute` parent that
	// orchestrates the graph.
	KindFleet Kind = "fleet"
	// KindFleetPlan is a FleetPlanWorkflow execution: the `fleet plan` agent run
	// that produces a graph, recorded separately from the run that executes it.
	KindFleetPlan Kind = "fleet-plan"
)

// Kinds lists every recorded kind, in a stable order, for CLI validation and
// help text.
func Kinds() []Kind {
	return []Kind{KindRun, KindDevelop, KindReview, KindPilot, KindFleet, KindFleetPlan}
}

// ValidKind reports whether k is a kind a workflow records under, so a mistyped
// `history --kind` filter is rejected instead of silently matching nothing.
func ValidKind(k Kind) bool {
	for _, known := range Kinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Status is how a recorded execution ended, or that it has not ended yet.
type Status string

const (
	// StatusRunning is the state a record starts in: the workflow recorded itself
	// and is still in flight. In-flight rows are shown by `history` so a live run
	// is visible in the durable record too.
	StatusRunning Status = "running"
	// StatusSucceeded means the workflow reached a terminal step without error. A
	// chained iteration that continues as new also succeeded: its work landed and
	// the next iteration is a row of its own.
	StatusSucceeded Status = "succeeded"
	// StatusFailed means the workflow ended with an error (including a
	// cancellation, which surfaces as one).
	StatusFailed Status = "failed"
	// StatusSkipped means the unit of work never ran because a prerequisite did
	// not succeed. Only fleet nodes are skipped, and a skipped node has no child
	// workflow — so no row of its own; it lives in the parent fleet row's Detail
	// and `history` expands it from there.
	StatusSkipped Status = "skipped"
)

// Execution is one recorded workflow execution.
//
// Every kind shares these columns; the fields that differ per kind live in
// Detail, so a new per-kind field needs no migration. RunID is the unique key: a
// chained run loops via continue-as-new, and each iteration is its own row under
// the same WorkflowID.
type Execution struct {
	// WorkflowID is the Temporal workflow ID. It groups a chained run's
	// continue-as-new iterations and correlates a tree of executions (see
	// ParentWorkflowID).
	WorkflowID string
	// RunID is the Temporal run ID: unique per continue-as-new iteration, and the
	// key every write upserts on so a retried activity neither duplicates a row
	// nor corrupts an existing one.
	RunID string
	// Kind is the command type that produced the record.
	Kind Kind
	// Prompt is what was asked: the run's prompt, the develop instruction, or the
	// fleet's goal. It is empty for workflows that take no instruction of their own
	// (e.g. a review loop).
	Prompt string
	// StartedAt is when the workflow recorded itself as started, taken from the
	// workflow's deterministic clock.
	StartedAt time.Time
	// EndedAt is when the workflow settled. It is the zero time while the
	// execution is still running.
	EndedAt time.Time
	// Status is the terminal outcome, or StatusRunning while in flight.
	Status Status
	// Tokens is this execution's **own incremental** token usage — the tokens of
	// its own Pi session(s), never the inclusive running total a workflow
	// propagates for its result text. Summing rows therefore gives a true total
	// with no double-counting across the fleet→node→review tree.
	Tokens int
	// ScheduleID is the schedule that fired this run, or empty when it was
	// started directly. Only KindRun records carry one.
	ScheduleID string
	// ParentWorkflowID is the workflow that started this one as a child, or empty
	// for a top-level execution. It makes the fleet→node and develop→review trees
	// reconstructable, and tells a child review apart from a standalone one.
	ParentWorkflowID string
	// Detail holds the kind-specific fields, stored as jsonb.
	Detail Detail
}

// Running reports whether the execution has not settled yet.
func (e Execution) Running() bool { return e.Status == StatusRunning }

// Duration returns how long the execution took, or 0 while it is still running.
func (e Execution) Duration() time.Duration {
	if e.EndedAt.IsZero() {
		return 0
	}
	return e.EndedAt.Sub(e.StartedAt)
}

// Detail carries the fields that only some kinds have. It is stored as a single
// jsonb column, so adding a field is a code change rather than a migration.
// Every field is omitempty: a record only carries what its kind produced.
type Detail struct {
	// Branch is the branch a develop execution worked on.
	Branch string `json:"branch,omitempty"`
	// PRURL is the pull request a develop (via its --with-remote open-PR stage) or
	// pilot execution operated on. Open-PR is not a kind of its own: it runs only
	// inside the develop pipeline, so its outcome is folded in here.
	PRURL string `json:"prURL,omitempty"`
	// Converged reports whether a review loop ended because the agent found
	// nothing left to change (rather than hitting the pass cap). It is nil for
	// kinds that do not review.
	Converged *bool `json:"converged,omitempty"`
	// Addressed reports whether a pilot pass actually addressed comments. It is
	// nil for kinds that do not pilot.
	Addressed *bool `json:"addressed,omitempty"`
	// Pass is which pass of a looping workflow this row is, since every pass
	// continues as new and is therefore a row of its own.
	Pass int `json:"pass,omitempty"`
	// Nodes is a fleet parent's per-node breakdown. It is the only home for a
	// skipped node's outcome: a skipped node starts no child workflow, so it has
	// no run ID and therefore no row of its own.
	Nodes []NodeOutcome `json:"nodes,omitempty"`
	// PlanID is the stored fleet plan a fleet-plan or fleet execution produced or
	// executed, correlating a plan handle with the runs it drove.
	PlanID string `json:"planID,omitempty"`
	// PlanNodes is how many nodes the plan has, so `history` can describe a fleet
	// or fleet-plan record without reading the plan.
	PlanNodes int `json:"planNodes,omitempty"`
	// Error is the failure text of a failed execution, so the durable record says
	// why it failed and not merely that it did.
	Error string `json:"error,omitempty"`
}

// NodeOutcome is one fleet node's outcome inside a fleet parent's Detail.
type NodeOutcome struct {
	// ID is the plan node this outcome belongs to.
	ID string `json:"id"`
	// Status is the node's terminal outcome. It reuses the record statuses plus
	// the fleet's own "blocked", which is neither a plain failure nor a skip.
	Status string `json:"status"`
	// Detail is the node's summary (on success) or the reason it failed, was
	// blocked, or was skipped.
	Detail string `json:"detail,omitempty"`
	// Tokens is the node's reported token usage, or 0 when it never ran.
	Tokens int `json:"tokens,omitempty"`
}

// Filter narrows a history query. Zero-valued fields impose no constraint.
type Filter struct {
	// Kind, when set, keeps only records of that command type.
	Kind Kind
	// WorkflowID, when set, keeps one execution and its children: rows whose
	// workflow ID matches (every continue-as-new iteration) and rows whose parent
	// workflow ID matches, so a run's whole tree is shown.
	WorkflowID string
	// ScheduleID, when set, keeps only the runs that schedule fired.
	ScheduleID string
	// Limit caps how many records are returned. A non-positive limit falls back
	// to DefaultHistoryLimit.
	Limit int
}

// DefaultHistoryLimit is how many records a history query returns when the
// caller sets no limit.
const DefaultHistoryLimit = 20

// DefaultPlanLimit is how many stored plans a listing returns when the caller
// sets no limit. It is a constant of its own rather than a reuse of
// DefaultHistoryLimit because a plan listing and a history query are different
// views that may want different defaults.
const DefaultPlanLimit = 20

// MaxListLimit is the largest limit a listing accepts, for history and for stored
// plans alike. Both read their whole result set into memory and print it as one
// table, so an absurd limit would be answered by pulling the table into the CLI
// rather than by refusing; the same rule applies to both because the reason is the
// same.
const MaxListLimit = 1000

// MaxPlanDocument is the largest plan document the store accepts, in bytes.
//
// The document is the only stored text that cannot go through the free-text funnel:
// truncating it would leave undecodable JSON, and a plan that does not decode is
// worthless. So the budget is enforced as a refusal instead of a trim. It is
// generous next to any plan a human would approve — a plan is a list of node
// prompts, and the largest realistic one is a few hundred KiB — so reaching it means
// the planning agent produced something no operator was going to read.
const MaxPlanDocument = 1 << 20 // 1 MiB

// Plan is a stored fleet plan: the approved graph an operator reviews and later
// executes by handle. It replaces the loose fleet-plan.json the plan used to live
// in, so a plan cannot be lost, overwritten, or left uncorrelated with the runs it
// drove.
type Plan struct {
	// ID is the generated handle the plan is referred to by. It is the canonical —
	// and only — way to resolve a plan for execution.
	ID string
	// Name is an optional operator-chosen label. It is display-only metadata, never
	// a selector: nothing makes it unique, so it could not resolve a plan
	// deterministically.
	Name string
	// Goal is the plan's high-level goal, stored alongside the document so a
	// listing needs no decode.
	Goal string
	// Nodes is the plan's node count, likewise stored for listing.
	Nodes int
	// Document is the plan itself, as the JSON encoding of the caller's plan type.
	// The store keeps it opaque so the plan's schema stays owned by the fleet core
	// rather than by the database.
	Document []byte
	// CreatedAt is when the plan was stored.
	CreatedAt time.Time
}

// ExecutionWriter is the port a workflow records through. It is deliberately
// separate from ExecutionReader: the two have exactly one caller each — an
// activity bundle only ever writes, the CLI only ever reads — so splitting them
// makes that a fact the compiler holds rather than a rule stated in a comment.
//
// SaveExecution must be idempotent on Execution.RunID: Temporal may re-run an
// activity whose result was lost after a partial success, so a retried start or
// terminal write must neither duplicate a row nor corrupt the existing one.
type ExecutionWriter interface {
	// SaveExecution inserts or updates the record for e.RunID.
	SaveExecution(ctx context.Context, e Execution) error
}

// ExecutionReader is the port the `history` command reads through.
type ExecutionReader interface {
	// ListExecutions returns the records matching f, newest first.
	ListExecutions(ctx context.Context, f Filter) ([]Execution, error)
}

// PlanStore is the port for the fleet plan store. Like execution recording it is
// authoritative rather than best-effort: a failed read or write is reported and
// aborts the operation, never silently swallowed, because the store is the only
// source of truth for a plan.
//
// SavePlan must be idempotent on Plan.ID for the same reason SaveExecution is: it
// is driven from an activity Temporal may retry.
type PlanStore interface {
	// SavePlan inserts or updates the plan under p.ID.
	SavePlan(ctx context.Context, p Plan) error
	// Plan resolves a plan by its handle, returning ErrNoSuchPlan when there is
	// none.
	Plan(ctx context.Context, id string) (Plan, error)
	// ListPlans returns the stored plans, newest first, capped at limit (a
	// non-positive limit falls back to DefaultPlanLimit).
	ListPlans(ctx context.Context, limit int) ([]Plan, error)
}
