package steering

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file is the application service an operator's surface drives: read the
// rounds that are waiting, read one of them with everything needed to decide it, and
// decide it.
//
// It owns the order the two writes happen in, and that order is the whole point.
// The decision is recorded durably *first* and the waiting round is resumed after,
// because a decision that cannot be written must fail in front of the operator
// rather than resume a loop nobody can prove was steered. A delivery that then fails
// is reported too: the record survives, so sending the decision again returns the
// one that was recorded and delivers it once more.

// SessionStore is the driven port for everything durable about a session. It is
// declared here, in the core's own vocabulary, so no SQL and no driver type reaches
// this package.
type SessionStore interface {
	// WaitingSessions returns every session still waiting for an operator, oldest
	// first: the one that has been waiting longest is the one most in need of an
	// answer.
	WaitingSessions(ctx context.Context) ([]Session, error)
	// Session returns one session, or ErrNoSuchSession.
	Session(ctx context.Context, id string) (Session, error)
	// Messages returns the session's turns after a sequence, in sequence order.
	// Passing 0 returns the whole conversation, which is what makes a stream
	// resumable by sequence rather than by time.
	Messages(ctx context.Context, id string, afterSequence int64) ([]Message, error)
	// AppendMessage appends one turn and returns it with the sequence it was given.
	// Sequences are per session, dense and monotonic.
	AppendMessage(ctx context.Context, message Message) (Message, error)
	// Events returns small hub notifications after a global sequence.
	Events(ctx context.Context, afterSequence int64, limit int) ([]Event, error)
	// SetGuidance replaces the editable guidance draft while the session waits.
	SetGuidance(ctx context.Context, id, guidance string) error
	// RecordDecision records a decision against a waiting session and returns the
	// session as it stands afterwards. It must be first-decision-wins: a session that
	// already carries one is returned unchanged, so a retried request and a second
	// browser tab both learn what was decided instead of deciding again.
	RecordDecision(ctx context.Context, id string, decision Decision, at time.Time) (Session, error)
}

// SessionRecorder is the driven port the orchestrated session writes through: it
// opens the row before it waits, and settles it once it has an answer. It is
// narrower than the store on purpose — the waiting unit has no business reading the
// conversation.
type SessionRecorder interface {
	// OpenSession records a round as waiting. It is idempotent on the session's
	// identity, because a replayed activity must not open a second session or
	// overwrite a decision that has already been recorded against this one.
	OpenSession(ctx context.Context, session Session) (Session, error)
	// CloseSession settles the session: it stops waiting. The decision is written only
	// when none was recorded — a decision sent through the API is already the
	// authoritative one, and a session that was signalled directly still has to end
	// with what it was told. A settlement carrying no decision at all is a session
	// whose loop is gone, and it is recorded as abandoned rather than left waiting.
	CloseSession(ctx context.Context, id string, decision Decision, at time.Time) error
}

// DecisionSignaller is the driven port that resumes the waiting round. It exists so
// the core never names an orchestrator: what the core knows is that a session with
// an identity has to be told what was decided.
type DecisionSignaller interface {
	// SignalDecision delivers a decision to the session waiting under this identity.
	// Delivering the same decision twice is safe: the waiting unit ignores everything
	// after the first.
	SignalDecision(ctx context.Context, sessionID string, decision Decision) error
}

// QuestionTurn identifies one operator turn for the bounded questioning workflow.
// The text itself stays in the durable conversation; orchestration receives only
// this reference, so the transcript is not copied into workflow history.
type QuestionTurn struct {
	SessionID        string
	OperatorSequence int64
	Finish           bool
}

// Questioner runs one read-only agent exchange and writes its result to the durable
// conversation before it returns.
type Questioner interface {
	RunQuestionTurn(ctx context.Context, turn QuestionTurn) error
}

// QuestionRequest is one contribution from an operator. Finish asks the agent to
// condense the exchange into the editable guidance draft.
type QuestionRequest struct {
	Text      string
	Principal string
	Finish    bool
}

// ErrQuestioningUnavailable reports only the optional conversational path. It does
// not affect direct guidance, skip, or stop.
var ErrQuestioningUnavailable = errors.New("the questioning agent is unavailable")

// Service is the driving surface of this context: what an operator's interface
// calls. It is a value with two ports and a clock, so a test drives the whole
// decision path without a database and without an orchestrator.
type Service struct {
	// Sessions is the durable store. It is required.
	Sessions SessionStore
	// Signals resumes a waiting round. It is required.
	Signals DecisionSignaller
	// Questioner runs one optional agent turn. When absent, every decision path still
	// works and Question reports that only questioning is unavailable.
	Questioner Questioner
	// Now supplies the current time, defaulting to time.Now.
	Now func() time.Time
}

// NewService builds the service, refusing to build one that cannot do its job: a
// surface that can read a waiting decision but never deliver one is worse than none,
// because it would offer an operator a button that quietly does nothing.
func NewService(sessions SessionStore, signals DecisionSignaller) (*Service, error) {
	if sessions == nil {
		return nil, errors.New("the steering store is required")
	}
	if signals == nil {
		return nil, errors.New("a way to resume the waiting round is required")
	}
	return &Service{Sessions: sessions, Signals: signals}, nil
}

// now reads the clock, defaulting to the wall clock.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Waiting returns the rounds currently waiting for an operator.
func (s *Service) Waiting(ctx context.Context) ([]Session, error) {
	sessions, err := s.Sessions.WaitingSessions(ctx)
	if err != nil {
		return nil, unavailable("read the waiting steering sessions", err)
	}
	return sessions, nil
}

// Conversation returns one session with everything needed to decide it.
// ConversationMessages returns turns after a sequence for a resumable stream.
func (s *Service) ConversationMessages(ctx context.Context, id string, after int64) ([]Message, error) {
	messages, err := s.Sessions.Messages(ctx, id, after)
	if err != nil {
		return nil, unavailable("read the steering conversation", err)
	}
	return messages, nil
}

// Events returns small hub notifications after a sequence.
func (s *Service) Events(ctx context.Context, after int64, limit int) ([]Event, error) {
	events, err := s.Sessions.Events(ctx, after, limit)
	if err != nil {
		return nil, unavailable("read hub events", err)
	}
	return events, nil
}

func (s *Service) Conversation(ctx context.Context, id string) (Conversation, error) {
	session, err := s.Sessions.Session(ctx, id)
	if err != nil {
		return Conversation{}, unavailable("read the steering session", err)
	}
	messages, err := s.Sessions.Messages(ctx, id, 0)
	if err != nil {
		return Conversation{}, unavailable("read the steering conversation", err)
	}
	return Conversation{Session: session, Messages: messages}, nil
}

// Question appends one attributed operator turn, runs one bounded agent exchange,
// and returns the conversation as durably stored afterwards.
func (s *Service) Question(ctx context.Context, id string, request QuestionRequest) (Conversation, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(request.Text) == "" {
		return Conversation{}, fmt.Errorf("%w: a question needs a session and text", ErrInvalidMessage)
	}
	if strings.TrimSpace(request.Principal) == "" {
		return Conversation{}, fmt.Errorf("%w: an operator turn needs its author", ErrInvalidMessage)
	}
	if s.Questioner == nil {
		return Conversation{}, ErrQuestioningUnavailable
	}
	session, err := s.Sessions.Session(ctx, id)
	if err != nil {
		return Conversation{}, unavailable("read the steering session", err)
	}
	if !session.Waiting() {
		return Conversation{}, fmt.Errorf("%w: the steering session is no longer waiting", ErrInvalidMessage)
	}
	message, err := s.Sessions.AppendMessage(ctx, Message{
		SessionID: id,
		Role:      RoleOperator,
		Author:    request.Principal,
		Text:      request.Text,
		At:        s.now(),
	})
	if err != nil {
		return Conversation{}, unavailable("record the operator's answer", err)
	}
	if err := s.Questioner.RunQuestionTurn(ctx, QuestionTurn{
		SessionID: id, OperatorSequence: message.Sequence, Finish: request.Finish,
	}); err != nil {
		return Conversation{}, fmt.Errorf("%w: %v", ErrQuestioningUnavailable, err)
	}
	return s.Conversation(ctx, id)
}

// Decide records an operator's decision and resumes the round it was waiting on.
//
// The decision is validated before anything is written, so a refusal explains itself
// without the store having been touched. A repeat is not a failure: the recorded
// decision is returned and delivered again, because two tabs and a retried request
// are normal and neither may start a second implementation pass.
func (s *Service) Decide(ctx context.Context, id string, decision Decision) (Session, error) {
	if strings.TrimSpace(id) == "" {
		return Session{}, fmt.Errorf("%w: a decision names the session it answers", ErrInvalidDecision)
	}
	session, err := s.Sessions.Session(ctx, id)
	if err != nil {
		return Session{}, unavailable("read the steering session", err)
	}
	if err := session.Round.ValidateDecision(decision); err != nil {
		return Session{}, err
	}
	recorded, err := s.Sessions.RecordDecision(ctx, id, decision, s.now())
	if err != nil {
		// Nothing is delivered on this path. A decision the store did not take is not a
		// decision, and reporting it as one would leave the operator believing a loop is
		// moving while it waits.
		return Session{}, unavailable("record the steering decision", err)
	}
	if err := s.Signals.SignalDecision(ctx, id, recorded.Decision); err != nil {
		return Session{}, fmt.Errorf("%w: the decision is recorded but the waiting round "+
			"could not be resumed; send it again: %v", ErrUnavailable, err)
	}
	return recorded, nil
}

// unavailable wraps a store failure as an outage, passing "no such session" through
// unchanged: a reader must be able to tell an answer that does not exist from one it
// could not reach.
func unavailable(what string, err error) error {
	if err == nil || errors.Is(err, ErrNoSuchSession) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, what, err)
}
