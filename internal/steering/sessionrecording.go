package steering

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/place"
	"temporal-agents/internal/wfrecord"
)

// This file is the durable side of the session as the orchestrated unit writes it:
// the row that says a round is waiting and what the decision is about, and the write
// that settles it once an answer is in.
//
// It is separate from the execution row the session also records (recording.go).
// That one exists so the session's token cost is attributable; this one is what an
// operator reads and decides. Both are written through ports, in activities, so no
// SQL reaches workflow code.
//
// The material travels as the *opening activity's* input rather than as the waiting
// unit's input, which is what keeps the review's findings out of the wait's own
// history: the session carries an identity, and everything an operator has to read
// stays in the store that is authoritative for it.

// SessionOpening is the typed input to OpenSteeringSession: what the pausing loop
// knows about the round it is about to stop.
type SessionOpening struct {
	// ID is the session's identity, minted from the run that pauses.
	ID string
	// ItemID is the run that is waiting — the work an operator sees.
	ItemID string
	// Round is which pause point this is.
	Round Round
	// Material is what the decision is about, as the agent would have received it.
	Material string
	// Place is where the paused work runs.
	Place place.Facts
	// OpenedAt is when the round started waiting, from the workflow's deterministic
	// clock.
	OpenedAt time.Time
}

// SessionSettlement is the typed input to CloseSteeringSession.
type SessionSettlement struct {
	// ID is the session that has been answered.
	ID string
	// Decision is what it was answered with. It is written only where the store holds
	// none, because a decision made through the API is already the authoritative one.
	Decision Decision
	// SettledAt is when the session stopped waiting.
	SettledAt time.Time
}

// OpenSteeringSession records a round as waiting, with the material the decision is
// about. It is idempotent on the session's identity: this activity is replayed, and
// a replay must not reopen a session that has since been decided.
func (a *Activities) OpenSteeringSession(ctx context.Context, in SessionOpening) (Session, error) {
	if a.Sessions == nil {
		return Session{}, ErrNoStore
	}
	return a.Sessions.OpenSession(ctx, Session{
		ID:       in.ID,
		ItemID:   in.ItemID,
		Round:    in.Round,
		Material: in.Material,
		Place:    in.Place,
		OpenedAt: in.OpenedAt,
	})
}

// CloseSteeringSession settles the session's stored row.
func (a *Activities) CloseSteeringSession(ctx context.Context, in SessionSettlement) error {
	if a.Sessions == nil {
		return ErrNoStore
	}
	return a.Sessions.CloseSession(ctx, in.ID, in.Decision, in.SettledAt)
}

// openSession writes the waiting round before anything waits on it, so the moment a
// loop stops there is something for an operator to find. A failure here fails the
// pause rather than being swallowed: a round waiting in a session nobody can read is
// a loop stuck with no visible cause, which is the exact failure this feature exists
// to remove.
func openSession(ctx workflow.Context, id string, in Pause) error {
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	opening := SessionOpening{
		ID:       id,
		ItemID:   workflow.GetInfo(ctx).WorkflowExecution.ID,
		Round:    in.Round,
		Material: in.Material,
		Place:    in.Place,
		OpenedAt: workflow.Now(ctx),
	}
	if err := workflow.ExecuteActivity(opts, a.OpenSteeringSession, opening).Get(opts, nil); err != nil {
		return fmt.Errorf("open the steering session for the %s round: %w", in.Round, err)
	}
	return nil
}

// closeSession settles the stored row on a disconnected context, so a cancelled
// session still stops reporting itself as waiting rather than staying open forever.
func closeSession(ctx workflow.Context, id string, decision Decision) error {
	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	settlement := SessionSettlement{ID: id, Decision: decision, SettledAt: workflow.Now(ctx)}
	if err := workflow.ExecuteActivity(dctx, a.CloseSteeringSession, settlement).Get(dctx, nil); err != nil {
		return fmt.Errorf("settle the steering session: %w", err)
	}
	return nil
}
