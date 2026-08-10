// Package instructiontest provides an in-memory implementation of the instruction
// ports for tests, in the same spirit as execstoretest: one stand-in every suite
// shares instead of a copy per package that drifts.
//
// It is a normal (non _test) package because Go cannot import another package's test
// files.
package instructiontest

import (
	"context"
	"fmt"
	"sync"

	"temporal-agents/internal/instruction"
)

// Store is an in-memory instruction store. It keeps versions append-only and one
// pointer per (key, scope), exactly as the Postgres adapter does, so a test that
// passes against it is testing the same rules.
type Store struct {
	mu sync.Mutex
	// versions holds every version ever appended, keyed by key and scope.
	versions map[instruction.Key]map[instruction.Scope][]instruction.Record
	// pointers is which version each (key, scope) currently resolves to.
	pointers map[instruction.Key]map[instruction.Scope]int
	// Err, when set, fails every read: the case where resolution must fail the unit
	// of work rather than substitute a default.
	Err error
	// Reads counts how many times the store was read, so a test can prove resolution
	// happens once per unit of work rather than once per pass.
	Reads int
}

// Compile-time proof the fake satisfies the ports it stands in for.
var _ instruction.Store = (*Store)(nil)

// New returns an empty store: nothing published, nothing overridden.
func New() *Store {
	return &Store{
		versions: map[instruction.Key]map[instruction.Scope][]instruction.Record{},
		pointers: map[instruction.Key]map[instruction.Scope]int{},
	}
}

// Set appends a version of key at scope and points that scope at it, which is what
// saving an override does. It returns the stored record.
func (s *Store) Set(key instruction.Key, scope instruction.Scope, text string) instruction.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendVersion(key, scope, text)
}

// Clear removes the pointer at scope without removing its versions, which is what
// resetting to the inherited value does.
func (s *Store) Clear(key instruction.Key, scope instruction.Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pointers[key], scope)
}

// Versions reports every version ever appended for one (key, scope), so a test can
// prove that an edit adds a version instead of rewriting one.
func (s *Store) Versions(key instruction.Key, scope instruction.Scope) []instruction.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.versions[key][scope]
	listed := make([]instruction.Record, len(stored))
	copy(listed, stored)
	return listed
}

// Current implements instruction.Reader.
func (s *Store) Current(_ context.Context, keys []instruction.Key, scopes []instruction.Scope) ([]instruction.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Reads++
	if s.Err != nil {
		return nil, s.Err
	}
	var records []instruction.Record
	for _, key := range keys {
		for _, scope := range scopes {
			version, ok := s.pointers[key][scope]
			if !ok {
				continue
			}
			records = append(records, s.versions[key][scope][version-1])
		}
	}
	return records, nil
}

// Version implements instruction.Reader.
func (s *Store) Version(_ context.Context, key instruction.Key, scope instruction.Scope, version int) (instruction.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return instruction.Record{}, s.Err
	}
	stored := s.versions[key][scope]
	if version < 1 || version > len(stored) {
		return instruction.Record{}, fmt.Errorf("%w: %s at %s v%d", instruction.ErrNoSuchVersion, key, scope, version)
	}
	return stored[version-1], nil
}

// PublishFactory implements instruction.Publisher: it appends a version only when
// the shipped text differs from the one the factory scope already points at.
func (s *Store) PublishFactory(_ context.Context, key instruction.Key, text string) (instruction.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return instruction.Record{}, s.Err
	}
	if version, ok := s.pointers[key][instruction.FactoryScope]; ok {
		if current := s.versions[key][instruction.FactoryScope][version-1]; current.Text == text {
			return current, nil
		}
	}
	return s.appendVersion(key, instruction.FactoryScope, text), nil
}

// appendVersion is the one write path, so the fake cannot mutate a version any more
// than the real adapter can. The caller holds the lock.
func (s *Store) appendVersion(key instruction.Key, scope instruction.Scope, text string) instruction.Record {
	if s.versions[key] == nil {
		s.versions[key] = map[instruction.Scope][]instruction.Record{}
		s.pointers[key] = map[instruction.Scope]int{}
	}
	record := instruction.Record{
		Key:     key,
		Scope:   scope,
		Version: len(s.versions[key][scope]) + 1,
		Text:    text,
		Hash:    instruction.Hash(text),
	}
	s.versions[key][scope] = append(s.versions[key][scope], record)
	s.pointers[key][scope] = record.Version
	return record
}
