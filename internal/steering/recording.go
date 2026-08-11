package steering

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/place"
	"temporal-agents/internal/wfrecord"
)

// This file is the recording half of the steering session: the state it persists,
// the activity that writes it through the execstore port, and the workflow-side
// helpers that call it. It mirrors codereview's recording exactly — every write
// happens in an activity, keyed on the Temporal run ID, so the workflow stays
// deterministic and no SQL reaches this package.
//
// The session records a row of its own because it costs tokens of its own (an
// operator who asks to be questioned is billed for it) and that cost must be
// attributable without the session becoming a second item on the overview. Its kind
// is deliberately absent from the kinds the overview draws items from: one piece of
// work must not appear twice.

// SessionRecord is the typed input to PersistSteeringSessionState.
type SessionRecord struct {
	// WorkflowID and RunID are the Temporal correlation handles; RunID is the key
	// every write upserts on.
	WorkflowID string
	RunID      string
	// ParentWorkflowID is the loop whose round is waiting, so the session's row is
	// reconstructably part of that run's tree.
	ParentWorkflowID string
	// Round is which pause point the session is.
	Round Round
	// StartedAt and EndedAt come from the workflow's deterministic clock; EndedAt is
	// the zero time while the session waits.
	StartedAt time.Time
	EndedAt   time.Time
	// Status is StatusRunning while waiting and the outcome afterwards.
	Status execstore.Status
	// Tokens is the session's own agent usage — nothing until the operator asks to
	// be questioned.
	Tokens int
	// Decision is what the operator decided, once they have. The guidance text
	// itself is not recorded here: it is the round's input, and it stays where the
	// conversation that produced it is authoritative.
	Decision Decision
	// Place is where the paused work runs.
	Place place.Facts
	// Error is the failure text when the session failed.
	Error string
}

// Activities bundles the driven adapters the steering session orchestrates. It is
// registered with the Temporal worker; each exported method is an activity.
type Activities struct {
	// Store is the durable execution history port. A nil store makes the write fail
	// loudly rather than panic, since recording is a hard dependency.
	Store execstore.ExecutionWriter
	// Sessions is the narrow store used by the durable pause itself.
	Sessions SessionRecorder
	// Conversation is the full steering store used by bounded questioning turns.
	Conversation SessionStore
	// Instructions resolves the governed questioning prompt at the paused place.
	Instructions instruction.Reader
	// QuestioningAgent runs one read-only Pi exchange under the stable session ID.
	QuestioningAgent QuestioningAgent
}

// ErrNoStore is returned by an activity asked to write without a steering store
// wired in. It turns a misconfigured worker into a clear activity failure instead of
// a nil-pointer panic.
var ErrNoStore = errors.New("steering store is not configured (is DATABASE_URL set?)")

// PersistSteeringSessionState records a steering session's state. It is called when
// the session starts waiting and again when it settles.
func (a *Activities) PersistSteeringSessionState(ctx context.Context, in SessionRecord) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindSteering,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail: execstore.Detail{
			Round:      string(in.Round),
			Decision:   string(in.Decision.Choice),
			Principal:  in.Decision.Principal,
			Error:      in.Error,
			Directory:  in.Place.Directory,
			Repository: in.Place.Repository,
		},
	})
}

// startSessionRecord writes the "started" record for the waiting session.
func startSessionRecord(ctx workflow.Context, in SessionInput) (SessionRecord, error) {
	if !wfrecord.Enabled(ctx) {
		return SessionRecord{}, nil
	}
	id := wfrecord.Of(ctx)
	st := SessionRecord{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		Round:            in.Round,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
		Place:            in.Place,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistSteeringSessionState, st).Get(opts, nil); err != nil {
		return SessionRecord{}, fmt.Errorf("record the steering session as started: %w", err)
	}
	return st, nil
}

// finishSessionRecord records the session's terminal state on a disconnected
// context, so a cancelled session still settles its row rather than staying
// "running" forever.
func finishSessionRecord(ctx workflow.Context, st SessionRecord, err error) error {
	if !wfrecord.Enabled(ctx) {
		return nil
	}
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistSteeringSessionState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the steering session's terminal state: %w", perr)
	}
	return nil
}
