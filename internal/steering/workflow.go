package steering

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/wfid"
	"temporal-agents/internal/wfnotify"
	"temporal-agents/internal/wfrecord"
)

const (
	// DecisionSignal is the name a decision is sent to the waiting session under.
	DecisionSignal = "steering-decision"
	// DecisionQuery is the name the session's own state is read under. It answers a
	// repeat with the decision that was recorded, so sending one twice is observable
	// as "already decided" rather than as an error.
	DecisionQuery = "steering-decision"
)

// Round says which pause point a session belongs to. There are exactly two, both
// immediately before the agent acts on review material.
type Round string

const (
	// RoundLocalReview is the pause after a local review has produced its findings
	// and before the agent implements them.
	RoundLocalReview Round = "local-review"
	// RoundRemoteComments is the pause after the pull request's unresolved comments
	// have been fetched and before the agent addresses them.
	RoundRemoteComments Round = "remote-comments"
	// RoundPassLimit is the human checkpoint reached after the autonomous pass budget.
	RoundPassLimit Round = "pass-limit"
)

// SessionInput is what the pausing loop tells the session about the round it is
// waiting on.
//
// It deliberately carries no review material. The material is large, it is already
// durable where it was produced, and orchestration history is for decisions — not
// for a copy of what the decision was about.
type SessionInput struct {
	// ItemID is the run that waits and the inbox link returns to.
	ItemID string
	// Recipient is the initiating principal, or empty for a broadcast notice.
	Recipient string
	// Round is which pause point this session is.
	Round Round
	// Place is where the paused work runs, so the session's own execution row lands
	// in the same place as the loop that is waiting.
	Place place.Facts
}

// SessionID names the session that pauses one round.
//
// It is derived from the run that pauses, so it is stable for the session's whole
// life: the session neither continues as new nor restarts, and a loop's later pass
// is a later run and therefore a session of its own. That stability is load-bearing
// — conversational memory is keyed on this identity (see the questioning agent) —
// so nothing may make it depend on anything that changes while a human is thinking.
func SessionID(loopRunID string) string { return wfid.SteeringWorkflowID(loopRunID) }

// SessionIDFor keeps distinct checkpoints in one workflow run from reusing a
// decided session. Each identity remains stable for that checkpoint's whole life.
func SessionIDFor(loopRunID string, round Round) string {
	id := SessionID(loopRunID)
	if round == RoundPassLimit {
		return id + "-pass-limit"
	}
	return id
}

// SessionState is what a reader of the waiting session is told: whether it is still
// waiting, and the decision it recorded once it is not.
// ValidateDecision checks both the decision's shape and the vocabulary of this
// checkpoint mode.
func (r Round) ValidateDecision(decision Decision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if r == RoundPassLimit {
		if decision.Choice == ChoiceContinue || decision.Choice == ChoiceAccept || decision.Choice == ChoiceStop {
			return nil
		}
		return fmt.Errorf("%w: %s is not a pass-limit decision", ErrInvalidDecision, decision.Choice)
	}
	if decision.Choice == ChoiceGuide || decision.Choice == ChoiceSkip || decision.Choice == ChoiceStop {
		return nil
	}
	return fmt.Errorf("%w: %s is not a review-round decision", ErrInvalidDecision, decision.Choice)
}

type SessionState struct {
	// Waiting reports whether the session is still waiting for a decision.
	Waiting bool
	// Decision is the decision that won, and the zero value while none has.
	Decision Decision
}

// SessionWorkflow is the durable wait itself: it records itself as an execution,
// waits for a decision, and returns the first valid one.
//
// The wait is unbounded by construction. Nothing here starts a timer, and the
// session carries no execution timeout, because a human is what it is waiting for
// and no clock the tool owns can say how long that may take. It is an orchestrated
// unit rather than an activity for the same reason: an activity holding a human
// wait occupies a worker slot and must heartbeat for days, and a worker restart
// would lose the wait entirely.
//
// The first valid decision wins. Later ones are drained and ignored, so two browser
// tabs and a retried request cannot start two implementation passes; the decision
// that won stays readable through DecisionQuery.
func SessionWorkflow(ctx workflow.Context, in SessionInput) (decision Decision, err error) {
	// Record the session as started before it waits, so a session waiting for days
	// is in the durable record the whole time and its cost is attributable to it
	// rather than to the loop it paused.
	rec, perr := startSessionRecord(ctx, in)
	if perr != nil {
		return Decision{}, perr
	}
	defer func() {
		rec.Decision = decision
		if perr := finishSessionRecord(ctx, rec, err); perr != nil {
			wfrecord.TerminalWriteFailed(ctx, "steering session", string(decision.Choice), err, perr)
		}
		// The stored session stops waiting whatever ended it. A session whose loop was
		// cancelled is recorded as abandoned rather than left asking a question nobody
		// can answer any more.
		if cerr := closeSession(ctx, workflow.GetInfo(ctx).WorkflowExecution.ID, decision); cerr != nil {
			workflow.GetLogger(ctx).Error("could not settle the stored steering session",
				"round", in.Round, "error", cerr)
		}
	}()

	if qerr := workflow.SetQueryHandler(ctx, DecisionQuery, func() (SessionState, error) {
		return SessionState{Waiting: !decision.Made(), Decision: decision}, nil
	}); qerr != nil {
		return Decision{}, fmt.Errorf("publish the steering session's state: %w", qerr)
	}

	decisions := workflow.GetSignalChannel(ctx, DecisionSignal)
	reminder := 0
	notifyWaiting(ctx, in, reminder)
	for !decision.Made() {
		timer := workflow.NewTimer(ctx, 24*time.Hour)
		var sent Decision
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(decisions, func(channel workflow.ReceiveChannel, _ bool) {
			channel.Receive(ctx, &sent)
		})
		selector.AddFuture(timer, func(workflow.Future) {
			reminder++
			notifyWaiting(ctx, in, reminder)
		})
		selector.Select(ctx)
		if !sent.Made() {
			continue
		}
		if verr := in.Round.ValidateDecision(sent); verr != nil {
			workflow.GetLogger(ctx).Warn("refused a steering decision", "round", in.Round, "error", verr)
			continue
		}
		decision = sent
	}
	// Anything already queued behind the winner is a repeat — a second tab, a retried
	// request — and is dropped here rather than left for a reader to mistake for a
	// pending decision.
	for {
		var repeat Decision
		if !decisions.ReceiveAsync(&repeat) {
			break
		}
		workflow.GetLogger(ctx).Info("ignored a repeated steering decision",
			"round", in.Round, "recorded", decision.Choice, "ignored", repeat.Choice)
	}
	rec.Tokens, err = conversationTokens(ctx, workflow.GetInfo(ctx).WorkflowExecution.ID)
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func notifyWaiting(ctx workflow.Context, in SessionInput, reminder int) {
	id := workflow.GetInfo(ctx).WorkflowExecution.ID
	title := "Review guidance is waiting"
	body := "A review round is waiting for an operator decision."
	if reminder > 0 {
		title = "Review guidance is still waiting"
		body = fmt.Sprintf("Reminder %d: a review round is still waiting for an operator decision.", reminder)
	}
	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		ID: fmt.Sprintf("%s:waiting:%d", id, reminder), Kind: "steering",
		Recipient: in.Recipient, SessionID: id, CreatedAt: workflow.Now(ctx),
		Title: title, Body: body, URL: "/#/runs/" + in.ItemID,
	})
}

// Pause is what the pausing loop knows at the pause point: which round is stopping,
// where it runs, and the material the operator has to read to decide.
//
// The material is deliberately not part of the waiting unit's own input. It is
// written to the store the conversation is authoritative in, and the wait carries
// only an identity, so the review's findings never become part of a history that is
// replayed every time the session is loaded.
type Pause struct {
	// Recipient is the initiating principal, empty for CLI and scheduled work.
	Recipient string
	// Round is which pause point this is.
	Round Round
	// Place is where the paused work runs.
	Place place.Facts
	// Material is what the decision is about, as the agent would have received it.
	Material string
}

// Ask pauses the calling loop on the operator: it records the waiting round with the
// material it is about, runs the session as a child of the loop, and returns the
// decision the operator made.
//
// The stored session is opened before the wait begins, so the moment a loop stops
// there is something for an operator to find. The session is a child rather than a
// workflow of its own so it cannot outlive the work it is steering: a cancelled loop
// takes its waiting session with it. It is given no timeout of any kind, at any
// level, because the wait is unbounded.
func Ask(ctx workflow.Context, in Pause) (Decision, error) {
	id := SessionIDFor(workflow.GetInfo(ctx).WorkflowExecution.RunID, in.Round)
	if err := openSession(ctx, id, in); err != nil {
		return Decision{}, err
	}
	opts := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: id})
	var decision Decision
	input := SessionInput{
		ItemID:    workflow.GetInfo(ctx).WorkflowExecution.ID,
		Recipient: in.Recipient,
		Round:     in.Round,
		Place:     in.Place,
	}
	if err := workflow.ExecuteChildWorkflow(opts, SessionWorkflow, input).Get(opts, &decision); err != nil {
		return Decision{}, fmt.Errorf("wait for the operator's decision on the %s round: %w", in.Round, err)
	}
	return decision, nil
}
