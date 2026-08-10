// Package scopedtest provides an in-memory implementation of the scoped
// configuration ports for tests, in the same spirit as execstoretest: one stand-in
// every suite shares instead of a copy per package that drifts.
//
// It is a normal (non _test) package because Go cannot import another package's test
// files.
package scopedtest

import (
	"context"
	"fmt"
	"sync"

	"temporal-agents/internal/scoped"
)

// Store is an in-memory store of scoped values. It keeps versions append-only and one
// pointer per (key, scope), exactly as the Postgres adapter does, so a test that
// passes against it is testing the same rules.
type Store struct {
	mu sync.Mutex
	// versions holds every version ever appended, keyed by key and scope.
	versions map[scoped.Key]map[scoped.Scope][]scoped.Record
	// pointers is which version each (key, scope) currently resolves to.
	pointers map[scoped.Key]map[scoped.Scope]int
	// Err, when set, fails every read: the case where resolution must fail the unit
	// of work rather than substitute a default.
	Err error
	// Reads counts how many times the store was read, so a test can prove resolution
	// happens once per unit of work rather than once per pass.
	Reads int
}

// Compile-time proof the fake satisfies the ports it stands in for.
var _ scoped.Store = (*Store)(nil)

// New returns an empty store: nothing published, nothing overridden.
func New() *Store {
	return &Store{
		versions: map[scoped.Key]map[scoped.Scope][]scoped.Record{},
		pointers: map[scoped.Key]map[scoped.Scope]int{},
	}
}

// Set implements scoped.Writer: it appends a version of key at scope and points that
// scope at it, which is what saving an override does.
func (s *Store) Set(_ context.Context, key scoped.Key, scope scoped.Scope, text string) (scoped.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return scoped.Record{}, s.Err
	}
	return s.appendVersion(key, scope, text), nil
}

// Store records a value the way a test states its setup: the same append-and-point
// write as Set, without a context or an error the test would only have to check.
func (s *Store) Store(key scoped.Key, scope scoped.Scope, text string) scoped.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendVersion(key, scope, text)
}

// Clear removes the pointer at scope without removing its versions, which is what
// resetting to the inherited value does.
func (s *Store) Clear(key scoped.Key, scope scoped.Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pointers[key], scope)
}

// Versions reports every version ever appended for one (key, scope), so a test can
// prove that an edit adds a version instead of rewriting one.
func (s *Store) Versions(key scoped.Key, scope scoped.Scope) []scoped.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.versions[key][scope]
	listed := make([]scoped.Record, len(stored))
	copy(listed, stored)
	return listed
}

// Current implements scoped.Reader.
func (s *Store) Current(_ context.Context, keys []scoped.Key, scopes []scoped.Scope) ([]scoped.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Reads++
	if s.Err != nil {
		return nil, s.Err
	}
	var records []scoped.Record
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

// Version implements scoped.Reader.
func (s *Store) Version(_ context.Context, key scoped.Key, scope scoped.Scope, version int) (scoped.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return scoped.Record{}, s.Err
	}
	stored := s.versions[key][scope]
	if version < 1 || version > len(stored) {
		return scoped.Record{}, fmt.Errorf("%w: %s at %s v%d", scoped.ErrNoSuchVersion, key, scope, version)
	}
	return stored[version-1], nil
}

// PublishFactory implements scoped.Publisher: it appends a version only when
// the shipped text differs from the one the factory scope already points at.
func (s *Store) PublishFactory(_ context.Context, key scoped.Key, text string) (scoped.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return scoped.Record{}, s.Err
	}
	if version, ok := s.pointers[key][scoped.FactoryScope]; ok {
		if current := s.versions[key][scoped.FactoryScope][version-1]; current.Text == text {
			return current, nil
		}
	}
	return s.appendVersion(key, scoped.FactoryScope, text), nil
}

// appendVersion is the one write path, so the fake cannot mutate a version any more
// than the real adapter can. The caller holds the lock.
func (s *Store) appendVersion(key scoped.Key, scope scoped.Scope, text string) scoped.Record {
	if s.versions[key] == nil {
		s.versions[key] = map[scoped.Scope][]scoped.Record{}
		s.pointers[key] = map[scoped.Scope]int{}
	}
	record := scoped.Record{
		Key:     key,
		Scope:   scope,
		Version: len(s.versions[key][scope]) + 1,
		Text:    text,
		Hash:    scoped.Hash(text),
	}
	s.versions[key][scope] = append(s.versions[key][scope], record)
	s.pointers[key][scope] = record.Version
	return record
}
