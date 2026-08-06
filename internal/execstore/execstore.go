// Package execstore owns the durable record of what the tool has executed: the
// port (repository interface) the application core persists through, the record
// types that cross it, and the Postgres adapter that implements it.
//
// Temporal's event history is retention-limited and wiped whenever its own
// datastore is reset, so it cannot answer "what have I run, with what outcome,
// at what token cost?" over time. The records here are that lasting memory: one
// row per Temporal run ID, written when a workflow starts and updated when it
// settles, plus the fleet plans an operator approves.
//
// The package follows the same hexagonal split as codereview and fleet: this
// file is the port and the record types (standard library only, no SQL or driver
// types), while postgres.go is the driven adapter. The Persist<Type>WorkflowState
// activities that drive the port live next to the workflows that call them (the
// root bundle for PromptWorkflow, codereview.Activities, fleet.Activities), so
// no SQL or pgx type ever reaches workflow or domain code.
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

// Store is the port the application core persists and reads execution history
// through. The Postgres adapter in this package implements it.
//
// SaveExecution must be idempotent on Execution.RunID: Temporal may re-run an
// activity whose result was lost after a partial success, so a retried start or
// terminal write must neither duplicate a row nor corrupt the existing one.
type Store interface {
	// SaveExecution inserts or updates the record for e.RunID.
	SaveExecution(ctx context.Context, e Execution) error
	// ListExecutions returns the records matching f, newest first.
	ListExecutions(ctx context.Context, f Filter) ([]Execution, error)
}
