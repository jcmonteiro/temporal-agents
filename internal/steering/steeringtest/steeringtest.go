// Package steeringtest provides an in-memory implementation of the steering
// context's ports for tests.
//
// It is a fake rather than a mock, for the same reason execstoretest is: the ports
// are a handful of methods over plain record types, so a test that asserts on the
// sessions and messages that were stored says far more about behaviour than one that
// asserts on which method was called. The fake enforces the two rules the real store
// is bought for — first decision wins, and per-session sequences that only increase
// — so a test that passes against it is testing the contract and not the adapter.
package steeringtest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"temporal-agents/internal/steering"
)

// Store is an in-memory steering store. Its zero value is not usable; call New.
type Store struct {
	mu       sync.Mutex
	sessions map[string]steering.Session
	messages map[string][]steering.Message
	// Failure, when set, is returned by every operation, so a test can drive the
	// "the store cannot be reached" path that must never be reported as success.
	Failure error
	// Signalled records every decision delivered through SignalDecision, in order, so
	// a test can pin that a repeat is delivered again and that nothing is delivered
	// when the write failed.
	Signalled []Delivery
	// SignalFailure, when set, is returned by SignalDecision.
	SignalFailure error
}

// Delivery is one decision handed to the waiting round.
type Delivery struct {
	SessionID string
	Decision  steering.Decision
}

// New returns an empty store.
func New() *Store {
	return &Store{sessions: map[string]steering.Session{}, messages: map[string][]steering.Message{}}
}

// Compile-time proof the fake satisfies every port it stands in for.
var (
	_ steering.SessionStore      = (*Store)(nil)
	_ steering.SessionRecorder   = (*Store)(nil)
	_ steering.DecisionSignaller = (*Store)(nil)
)

// WithSession seeds a session, so a test starts from a round that is already
// waiting.
func (s *Store) WithSession(session steering.Session) *Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.State == "" {
		session.State = steering.StateWaiting
	}
	s.sessions[session.ID] = session
	return s
}

// OpenSession implements steering.SessionRecorder.
func (s *Store) OpenSession(_ context.Context, session steering.Session) (steering.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return steering.Session{}, s.Failure
	}
	if existing, ok := s.sessions[session.ID]; ok {
		return existing, nil
	}
	session.State = steering.StateWaiting
	s.sessions[session.ID] = session
	return session, nil
}

// CloseSession implements steering.SessionRecorder.
func (s *Store) CloseSession(_ context.Context, id string, decision steering.Decision, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return s.Failure
	}
	session, ok := s.sessions[id]
	if !ok {
		return steering.ErrNoSuchSession
	}
	session.State = steering.StateDecided
	if !session.Decision.Made() {
		session.Decision = decision
		session.DecidedAt = at
	}
	s.sessions[id] = session
	return nil
}

// WaitingSessions implements steering.SessionStore.
func (s *Store) WaitingSessions(context.Context) ([]steering.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return nil, s.Failure
	}
	waiting := make([]steering.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session.Waiting() {
			waiting = append(waiting, session)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].OpenedAt.Before(waiting[j].OpenedAt) })
	return waiting, nil
}

// Session implements steering.SessionStore.
func (s *Store) Session(_ context.Context, id string) (steering.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return steering.Session{}, s.Failure
	}
	session, ok := s.sessions[id]
	if !ok {
		return steering.Session{}, steering.ErrNoSuchSession
	}
	return session, nil
}

// Messages implements steering.SessionStore.
func (s *Store) Messages(_ context.Context, id string, afterSequence int64) ([]steering.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return nil, s.Failure
	}
	if _, ok := s.sessions[id]; !ok {
		return nil, steering.ErrNoSuchSession
	}
	after := make([]steering.Message, 0, len(s.messages[id]))
	for _, message := range s.messages[id] {
		if message.Sequence > afterSequence {
			after = append(after, message)
		}
	}
	return after, nil
}

// AppendMessage implements steering.SessionStore.
func (s *Store) AppendMessage(_ context.Context, message steering.Message) (steering.Message, error) {
	if err := message.Validate(); err != nil {
		return steering.Message{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return steering.Message{}, s.Failure
	}
	if _, ok := s.sessions[message.SessionID]; !ok {
		return steering.Message{}, steering.ErrNoSuchSession
	}
	message.Sequence = int64(len(s.messages[message.SessionID])) + 1
	s.messages[message.SessionID] = append(s.messages[message.SessionID], message)
	return message, nil
}

// RecordDecision implements steering.SessionStore, first decision wins.
func (s *Store) RecordDecision(_ context.Context, id string, decision steering.Decision, at time.Time) (steering.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Failure != nil {
		return steering.Session{}, s.Failure
	}
	session, ok := s.sessions[id]
	if !ok {
		return steering.Session{}, steering.ErrNoSuchSession
	}
	if session.Decision.Made() {
		return session, nil
	}
	session.Decision = decision
	session.DecidedAt = at
	session.State = steering.StateDecided
	if decision.Choice == steering.ChoiceGuide {
		session.Guidance = decision.Guidance
	}
	s.sessions[id] = session
	return session, nil
}

// SignalDecision implements steering.DecisionSignaller by journalling the delivery.
func (s *Store) SignalDecision(_ context.Context, sessionID string, decision steering.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SignalFailure != nil {
		return s.SignalFailure
	}
	s.Signalled = append(s.Signalled, Delivery{SessionID: sessionID, Decision: decision})
	return nil
}

// Deliveries returns the decisions delivered so far.
func (s *Store) Deliveries() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.Signalled...)
}

// ErrStoreDown is a convenient outage for a test that drives the failure path.
var ErrStoreDown = errors.New("the store is down")
