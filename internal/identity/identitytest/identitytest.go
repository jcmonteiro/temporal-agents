// Package identitytest provides in-memory implementations of the identity ports for
// tests.
//
// They are fakes, not mocks: the ports are a handful of records with two rules that
// matter (taking a pending sign-in consumes it, a session is found by the digest of
// the browser's token), so a test that signs in and then asserts on what a request
// can do says something about the behaviour, while a test that asserts on which port
// method was called only restates the implementation.
//
// The same fakes serve the core's tests, the provider adapter's container suite and
// the HTTP adapter's, so there is one stand-in to keep truthful rather than one per
// package.
package identitytest

import (
	"context"
	"sync"
	"time"

	"temporal-agents/internal/identity"
)

// Store is an in-memory implementation of every identity store port. The zero value
// is unusable; build one with NewStore.
type Store struct {
	// mu guards the state, so a test may drive the service from several goroutines.
	mu sync.Mutex
	// sessions, pending and principals are keyed by their identity: a token digest
	// for the first two, and issuer plus subject for the last.
	sessions   map[string]identity.Session
	pending    map[string]identity.PendingSignIn
	principals map[string]identity.Principal
	// FailWith, when set, is what every session read reports: a store that cannot be
	// reached, which must never be mistaken for a refused credential.
	FailWith error
}

// Compile-time proof the fake stands in for every port it is injected as.
var (
	_ identity.SessionStore       = (*Store)(nil)
	_ identity.PrincipalStore     = (*Store)(nil)
	_ identity.PendingSignInStore = (*Store)(nil)
)

// NewStore builds an empty store: nobody signed in, nothing in flight.
func NewStore() *Store {
	return &Store{
		sessions:   map[string]identity.Session{},
		pending:    map[string]identity.PendingSignIn{},
		principals: map[string]identity.Principal{},
	}
}

// key is how a token digest addresses a record.
func key(hash []byte) string { return string(hash) }

// CreateSession implements identity.SessionStore.
func (s *Store) CreateSession(_ context.Context, session identity.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.principals[session.Principal.ID()] = session.Principal
	s.sessions[key(session.TokenHash)] = session
	return nil
}

// Session implements identity.SessionStore.
func (s *Store) Session(_ context.Context, hash []byte) (identity.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailWith != nil {
		return identity.Session{}, s.FailWith
	}
	session, ok := s.sessions[key(hash)]
	if !ok {
		return identity.Session{}, identity.ErrNoSession
	}
	return session, nil
}

// UpdateSessionTokens implements identity.SessionStore.
func (s *Store) UpdateSessionTokens(_ context.Context, hash []byte, tokens identity.Tokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key(hash)]
	if !ok {
		return identity.ErrNoSession
	}
	session.Tokens = tokens
	s.sessions[key(hash)] = session
	return nil
}

// EndSession implements identity.SessionStore.
func (s *Store) EndSession(_ context.Context, hash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[key(hash)]; !ok {
		return identity.ErrNoSession
	}
	delete(s.sessions, key(hash))
	return nil
}

// DeleteExpiredSessions implements identity.SessionStore.
func (s *Store) DeleteExpiredSessions(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for hash, session := range s.sessions {
		if session.Expired(before) {
			delete(s.sessions, hash)
			removed++
		}
	}
	return removed, nil
}

// StartSignIn implements identity.PendingSignInStore.
func (s *Store) StartSignIn(_ context.Context, pending identity.PendingSignIn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[key(pending.TokenHash)] = pending
	return nil
}

// TakePendingSignIn implements identity.PendingSignInStore, consuming the record so
// a callback can be completed exactly once.
func (s *Store) TakePendingSignIn(_ context.Context, hash []byte) (identity.PendingSignIn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[key(hash)]
	if !ok {
		return identity.PendingSignIn{}, identity.ErrNoPendingSignIn
	}
	delete(s.pending, key(hash))
	return pending, nil
}

// DeleteExpiredSignIns implements identity.PendingSignInStore.
func (s *Store) DeleteExpiredSignIns(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for hash, pending := range s.pending {
		if pending.Expired(before) {
			delete(s.pending, hash)
			removed++
		}
	}
	return removed, nil
}

// UpsertPrincipal implements identity.PrincipalStore.
func (s *Store) UpsertPrincipal(_ context.Context, principal identity.Principal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.principals[principal.ID()] = principal
	return nil
}

// Principal implements identity.PrincipalStore.
func (s *Store) Principal(_ context.Context, issuer, subject string) (identity.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	principal, ok := s.principals[identity.Principal{Issuer: issuer, Subject: subject}.ID()]
	if !ok {
		return identity.Principal{}, identity.ErrNoPrincipal
	}
	return principal, nil
}

// Sessions reports how many sessions are held, so a test can assert that an ended
// session left nothing behind.
func (s *Store) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// PendingSignIns reports how many sign-ins are in flight.
func (s *Store) PendingSignIns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}
