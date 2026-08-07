// Package wfrecord holds the workflow-side helpers every
// Persist<Type>WorkflowState call shares: the correlation handles read from the
// workflow's own execution info, the must-succeed activity policy, and the
// mapping from a workflow's outcome to a recorded status.
//
// It depends on both the SDK-free execstore port and the Temporal SDK, mirroring
// how wfnotify carries the workflow-side half of the notification port so that
// port stays SDK-free. Centralizing the policy here keeps every workflow's
// recording behavior identical: one place decides how hard a record write is
// retried and how long a workflow waits for it.
package wfrecord

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/wfnotify"
)

// recordingChangeID names the workflow change that introduced durable recording,
// so histories written before it replay unchanged. See Enabled.
const recordingChangeID = "record-execution-state"

// Enabled reports whether the executing workflow records its state.
//
// Recording added activity calls to workflows that were already running, so it is
// gated behind a workflow version: an execution whose history predates the change
// would otherwise replay against code that schedules a record write its history
// lacks, and fail nondeterministically. Long-lived executions make this concrete —
// a chained run, a schedule, and the pilot loop can all be in flight across the
// worker upgrade that turns recording on.
//
// The consequence is deliberate: an execution started before the upgrade is never
// recorded, while every execution started after it (including the next iteration
// of a chain, which begins a fresh history) is. It is safe to call more than once
// per execution — the same change ID always yields the same answer, and only the
// first call records the marker — so the terminal write can consult it again.
func Enabled(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, recordingChangeID, workflow.DefaultVersion, 1) == 1
}

// Identity is a workflow execution's correlation handles, copied into every
// record it writes.
type Identity struct {
	// WorkflowID groups a chained run's continue-as-new iterations and correlates
	// a tree of executions.
	WorkflowID string
	// RunID is unique per continue-as-new iteration and is the key each write
	// upserts on.
	RunID string
	// FirstRunID identifies the whole continue-as-new chain. Unlike WorkflowID, it
	// changes when a schedule starts a new firing with the same workflow ID.
	FirstRunID string
	// ParentWorkflowID is the workflow that started this one as a child, or empty
	// for a top-level execution.
	ParentWorkflowID string
}

// Of reads the executing workflow's correlation handles. The parent handle comes
// from the workflow's own execution info rather than from ID-prefix parsing, so
// the fleet→node and develop→review trees are reconstructable and a child review
// is distinguishable from a standalone one.
func Of(ctx workflow.Context) Identity {
	info := workflow.GetInfo(ctx)
	id := Identity{
		WorkflowID: info.WorkflowExecution.ID,
		RunID:      info.WorkflowExecution.RunID,
		FirstRunID: info.FirstRunID,
	}
	if id.FirstRunID == "" {
		// Older test environments and legacy histories can omit this field. A single
		// run is still a valid one-iteration chain, so its own ID is the safe identity.
		id.FirstRunID = id.RunID
	}
	// ParentWorkflowExecution is nil for a top-level execution, which is the normal
	// case for every standalone command.
	if info.ParentWorkflowExecution != nil {
		id.ParentWorkflowID = info.ParentWorkflowExecution.ID
	}
	return id
}

// WithOptions returns ctx carrying the shared policy for the persistence
// activities. Recording is not best-effort: the retries let Temporal absorb a
// transient Postgres outage rather than silently dropping the record. The attempt
// budget spans a couple of minutes of backoff, long enough for a restart but short
// enough that a genuinely down store surfaces promptly. What an exhausted budget
// costs depends on the write: a start write fails its workflow, a terminal one is
// reported (see TerminalWriteFailed).
func WithOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    10,
		},
	})
}

// TerminalOptions returns a context for the terminal record write: the shared
// policy on a disconnected context, plus its cancel function for the caller to
// defer.
//
// The disconnected context matters for the case a record is most needed: a
// workflow that failed because it was cancelled. Scheduling on the (already
// cancelled) workflow context would fail immediately, leaving the row stuck at
// "running" forever, so the terminal write is deliberately made immune to the
// cancellation it is recording.
func TerminalOptions(ctx workflow.Context) (workflow.Context, workflow.CancelFunc) {
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	return WithOptions(dctx), cancel
}

// TerminalWriteFailed reports a terminal record write that could not be made.
//
// It is the one place the terminal-write policy lives, and the policy is: a failed
// terminal write never changes the outcome of the work it recorded. The *start*
// write stays a hard dependency — nothing has happened yet, so refusing to run
// unrecorded costs nothing and keeps the history complete — but by the time the
// terminal write runs, an agent has done up to an hour of work and Temporal has
// already retried the write for about two minutes (see WithOptions). Failing there
// would trade a bookkeeping outage for lost agent work: the record is
// bookkeeping, the result is the product.
//
// It also makes the guarantee uniform across the looping workflows. On a
// continue-as-new path the workflow's error *is* the control signal, so a deferred
// writer that replaced it would strand the loop; and a writer that only replaced a
// nil error left those paths recorded differently from the non-looping ones.
//
// The failure is surfaced rather than swallowed: it is logged with the result the
// record no longer holds, and — when there is a result to preserve — delivered as
// a best-effort notification, on a disconnected context so a cancelled workflow
// can still send it. The row itself stays "running", which `history --help`
// already tells the operator to read as abandoned.
//
// what names the execution in operator wording ("run", "develop run", "review
// pass"), result is the work the execution produced, outcome is the execution's
// own error (nil, or the continue-as-new signal, when its work landed) and
// writeErr is why the record could not be written.
func TerminalWriteFailed(ctx workflow.Context, what, result string, outcome, writeErr error) {
	if outcome != nil && !IsContinueAsNew(outcome) {
		// A failed execution produced nothing to rescue.
		result = ""
	}
	if result == "" {
		// There is no product to rescue — the execution failed, or produced nothing — so
		// the log is enough. A notification here would only add noise next to the
		// failure the workflow already announces itself.
		workflow.GetLogger(ctx).Error("could not record the "+what+"'s terminal state; its row stays \"running\"",
			"error", writeErr)
		return
	}
	workflow.GetLogger(ctx).Error("the "+what+" finished, but its terminal record could not be written; "+
		"the result is only available here", "error", writeErr, "result", result)
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	wfnotify.NotifyBestEffort(dctx, notification.Notification{
		Title: "Record not written: " + what,
		Body: "The " + what + " finished, but its terminal record could not be written, so it stays " +
			"\"running\" in history.\n\nrecord error: " + writeErr.Error() + "\n\nresult:\n" + result,
	})
}

// StatusOf maps a workflow's outcome to the status recorded for it. A
// continue-as-new is a control signal, not a failure: the iteration that emits it
// did its own work and settled, and the next iteration is a row of its own, so it
// is recorded as succeeded.
func StatusOf(err error) execstore.Status {
	if err == nil || IsContinueAsNew(err) {
		return execstore.StatusSucceeded
	}
	return execstore.StatusFailed
}

// IsContinueAsNew reports whether err is the continue-as-new control signal.
func IsContinueAsNew(err error) bool {
	var canErr *workflow.ContinueAsNewError
	return errors.As(err, &canErr)
}

// FailureText renders err for the record's detail, or "" when the execution did
// not fail. The text goes through Sanitize, so no caller has to remember to
// redact or cap it.
func FailureText(err error) string {
	if err == nil || IsContinueAsNew(err) {
		return ""
	}
	return Sanitize(err.Error())
}

// MaxDetailText is the byte budget for one free-text field of a record (a
// failure's text, a fleet node's summary). Agent output and git stderr have no
// natural bound — a single failure can carry megabytes — and a fleet parent's
// detail holds one entry per node, so an uncapped field would let one bad run
// bloat a row indefinitely. The budget is generous enough to keep the part an
// operator reads: a stack trace or a command's output tail.
const MaxDetailText = 8 << 10 // 8 KiB

// truncationMarker replaces what the cap dropped, so a shortened value reads as
// deliberately shortened rather than as output that simply stopped.
const truncationMarker = "… [truncated]"

// urlCredential matches the "user:secret@host" form of a URL. git echoes the
// remote it failed on verbatim, and a token-authenticated remote embeds the token
// there ("https://x-access-token:ghs_…@github.com/o/r.git"), so this is the
// pattern a recorded failure leaks a credential through most easily.
var urlCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

// bareToken matches the GitHub token shapes on their own, for the case a token
// reaches the text outside a URL (agent output quoting a header or an env var).
// It is defence in depth: such text passes through no URL-shaped funnel at all.
var bareToken = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,})`)

// redacted is what a removed secret is replaced by. It is deliberately visible:
// the operator must be able to see that something was removed.
const redacted = "REDACTED"

// Sanitize prepares free text for the durable record: it removes the credentials
// a failure text can carry and caps the length.
//
// It is the single funnel every recorded free-text field passes through — the
// failure text of any workflow (see FailureText), a prompt or goal, and a fleet
// node's own detail — so redaction and truncation are decided in one place instead
// of at each call site. The record is long-lived and local, so a leaked token would
// sit in it indefinitely, and an uncapped detail would grow a row without bound.
//
// What is deliberately *exempt*, and why, so the next field is not added on the
// wrong side by default:
//   - Structured, self-bounded values the tool produces itself, not text an agent or
//     a subprocess wrote: a branch name, a PR URL, a plan handle, a plan node ID, a
//     schedule ID. They have a shape, they cannot carry a credential the tool did not
//     put there, and capping one would corrupt an identifier rather than shorten a
//     message.
//   - The stored plan's document, which must stay decodable: trimming it would leave
//     invalid JSON, so it is size-guarded (execstore.MaxPlanDocument) and an oversized
//     plan is refused instead of mangled.
//
// Anything that is free text — anything an agent, a git command or an operator
// typed — belongs on this side of the funnel.
func Sanitize(text string) string {
	if text == "" {
		return ""
	}
	clean := urlCredential.ReplaceAllString(text, "${1}"+redacted+"@")
	clean = bareToken.ReplaceAllString(clean, redacted)
	return capText(clean)
}

// capText shortens text to MaxDetailText bytes, keeping the head (where the
// reason for a failure usually is) and reporting how much was dropped. The cut is
// moved back to a rune boundary so the result stays valid UTF-8 and survives the
// jsonb round-trip.
func capText(text string) string {
	if len(text) <= MaxDetailText {
		return text
	}
	cut := MaxDetailText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	dropped := len(text) - cut
	return strings.TrimRight(text[:cut], " \t\r\n") +
		fmt.Sprintf("\n%s %d more byte(s)", truncationMarker, dropped)
}
