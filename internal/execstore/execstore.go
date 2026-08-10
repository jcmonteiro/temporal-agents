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
	// KindSteering is a steering session: the durable wait a review round pauses in
	// while an operator decides. It is recorded so its own token cost is
	// attributable, and it is deliberately not an item of its own anywhere an
	// operator looks — the loop it paused is the work.
	KindSteering Kind = "steering"
)

// Kinds lists every recorded kind, in a stable order, for CLI validation and
// help text.
func Kinds() []Kind {
	return []Kind{KindRun, KindDevelop, KindReview, KindPilot, KindFleet, KindFleetPlan, KindSteering}
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
	// FirstRunID is Temporal's first run ID for the execution chain. It stays the
	// same across continue-as-new iterations but changes for each schedule firing,
	// even when those firings reuse one workflow ID.
	FirstRunID string
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
	//
	// It is kept beside Ending, which says the same thing and more: a loop can now
	// also end because an operator stopped or accepted it, which a single flag
	// cannot express. Rows written before Ending existed carry only this flag, so
	// both are written and neither is derived from the other.
	Converged *bool `json:"converged,omitempty"`
	// Ending names why a review or pilot loop ended: it converged, an operator
	// accepted the work, an operator stopped it, or it reached the pass cap. It is
	// empty for a row that has not ended, and for kinds that do not loop.
	Ending string `json:"ending,omitempty"`
	// WaitingSince is when the execution started waiting for an operator's decision,
	// and nil whenever it is not waiting. It is what lets a run that is technically
	// still running report that it needs a human, and say since when. It is a
	// pointer because a zero time is a value jsonb would keep: a row that never
	// waited must carry no such field at all.
	WaitingSince *time.Time `json:"waitingSince,omitempty"`
	// Round is which pause point a steering session belongs to.
	Round string `json:"round,omitempty"`
	// Decision is what an operator decided in a steering session: guide, skip or
	// stop. It is empty while the session is still waiting.
	Decision string `json:"decision,omitempty"`
	// Principal is who made that decision, recorded for audit. Any signed-in
	// operator may answer, so this says who did, never who was allowed to.
	Principal string `json:"principal,omitempty"`
	// Addressed reports whether a pilot pass actually addressed comments. It is
	// nil for kinds that do not pilot.
	Addressed *bool `json:"addressed,omitempty"`
	// Pass is which pass of a looping workflow this row is, since every pass
	// continues as new and is therefore a row of its own.
	Pass int `json:"pass,omitempty"`
	// Nodes is a fleet parent's per-node breakdown. It is the only home for a
	// skipped node's outcome: a skipped node starts no child workflow, so it has
	// no run ID and therefore no row of its own.
	//
	// Each entry's Detail is capped individually (wfrecord.MaxDetailText, 8 KiB), so a
	// fleet parent's row is bounded at nodes × that cap: a 100-node plan can reach
	// roughly 800 KiB of jsonb. That is the deliberate trade — a node's outcome is only
	// readable here — and it is why the node count of a plan an operator approves is the
	// thing that keeps the row small, not the per-field cap alone.
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
	// Directory is the absolute path of the working tree the execution ran in, as
	// the location probe established it. It is empty when the probe failed, found no
	// repository, or never ran, which is what makes an execution's place honestly
	// unknown instead of guessed.
	Directory string `json:"directory,omitempty"`
	// Repository is the absolute path of the repository that working tree belongs
	// to, recorded only when the probe found the two to differ (a linked worktree).
	// It is never derived from one path containing the other: git puts a worktree
	// outside its repository by default.
	Repository string `json:"repository,omitempty"`
	// Instructions is which stored instruction each governed key of this execution
	// resolved to. It is empty for an execution that resolves none, and for one
	// started before instructions were stored.
	Instructions []InstructionUse `json:"instructions,omitempty"`
}

// InstructionUse is one instruction an execution ran under, recorded as values
// rather than as a reference: the record and the instruction store are separate
// contexts, so neither may key into the other's tables.
//
// The text is deliberately absent. It stays in the version record the three
// identifying fields name, so a row cannot grow with the instruction it used and one
// instruction's text is stored once however many executions used it. The hash makes
// the naming verifiable even by hand.
type InstructionUse struct {
	// Key is the governed instruction ("review.perform").
	Key string `json:"key"`
	// Scope is where the value that won came from ("global", "factory",
	// "directory:<path>").
	Scope string `json:"scope"`
	// Version is which version of that (key, scope) was used. It is 0 when the
	// execution used the value its build ships, because storage held none yet.
	Version int `json:"version,omitempty"`
	// Hash is the content hash of the instruction text that was used.
	Hash string `json:"hash,omitempty"`
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
//
// It is a property of the port, not of one caller: an adapter resolves every limit
// through EffectiveLimit, so a consumer that forgets to check still cannot ask for
// more. The CLI checks it too, but for a different purpose — a typed --limit above
// the cap is refused with a message, rather than silently served as something
// smaller.
const MaxListLimit = 1000

// EffectiveLimit resolves how many rows a listing actually returns: def when n is
// non-positive (no limit asked for), and never more than MaxListLimit.
//
// It takes the default as a parameter because each listing has its own
// (DefaultHistoryLimit, DefaultPlanLimit), while the cap is shared.
func EffectiveLimit(n, def int) int {
	if n <= 0 {
		n = def
	}
	if n > MaxListLimit {
		return MaxListLimit
	}
	return n
}

// MaxPlanDocument is the largest plan document the store accepts, in bytes. Like
// MaxListLimit it is enforced by the adapter as well as by the calling activity, so
// it describes the port rather than one of its callers.
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

// ChainFilter selects execution-chain resources. The adapter applies Limit to
// workflow IDs and only then loads every iteration for those IDs. It also loads
// RequiredWorkflowIDs in full when they are outside that normal page.
type ChainFilter struct {
	Kinds               []Kind
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

// ExecutionTree is one selected root chain and its direct child records.
type ExecutionTree struct {
	Chain      ExecutionChain
	Executions []Execution
}

// OverviewReader is the purpose-built read port for resource collections. It
// prevents a limit on execution rows from being mistaken for a limit on chains.
type OverviewReader interface {
	ListExecutionChains(ctx context.Context, filter ChainFilter) ([]ExecutionChain, error)
	ListExecutionTrees(ctx context.Context, filter ChainFilter) ([]ExecutionTree, error)
	ListScheduleActionChains(ctx context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]ExecutionChain, error)
}

// PlanReader is the read-only half of the authoritative fleet plan store.
type PlanReader interface {
	// Plan resolves a plan by its handle, returning ErrNoSuchPlan when there is
	// none.
	Plan(ctx context.Context, id string) (Plan, error)
	// Plans resolves all existing handles in one read, keyed by handle.
	Plans(ctx context.Context, ids []string) (map[string]Plan, error)
	// ListPlans returns the stored plans, newest first, capped at limit.
	ListPlans(ctx context.Context, limit int) ([]Plan, error)
}

// PlanWriter is the write-only half used by workflow activities.
type PlanWriter interface {
	// SavePlan inserts or updates the plan under p.ID. It must be idempotent because
	// Temporal can retry the activity after a successful write.
	SavePlan(ctx context.Context, p Plan) error
}

// PlanStore combines plan reads and writes for callers that own both operations.
type PlanStore interface {
	PlanReader
	PlanWriter
}
