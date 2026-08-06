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
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
)

// Identity is a workflow execution's correlation handles, copied into every
// record it writes.
type Identity struct {
	// WorkflowID groups a chained run's continue-as-new iterations and correlates
	// a tree of executions.
	WorkflowID string
	// RunID is unique per continue-as-new iteration and is the key each write
	// upserts on.
	RunID string
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
	}
	// ParentWorkflowExecution is nil for a top-level execution, which is the normal
	// case for every standalone command.
	if info.ParentWorkflowExecution != nil {
		id.ParentWorkflowID = info.ParentWorkflowExecution.ID
	}
	return id
}

// WithOptions returns ctx carrying the shared policy for the persistence
// activities. Recording is a hard dependency, not best-effort: the retries let
// Temporal absorb a transient Postgres outage, and exhausting them fails the
// workflow rather than silently dropping the record. The attempt budget spans a
// couple of minutes of backoff, long enough for a restart but short enough that a
// genuinely down store surfaces promptly.
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
// not fail. It is the workflow error verbatim: current workflow errors carry no
// secrets, and the record is local, but an activity error that ever embeds a
// token must be sanitized at its source.
func FailureText(err error) string {
	if err == nil || IsContinueAsNew(err) {
		return ""
	}
	return err.Error()
}
