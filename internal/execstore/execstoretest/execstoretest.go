// Package execstoretest provides an in-memory implementation of the execstore
// ports for tests.
//
// Every workflow now records itself, so every workflow test needs the port
// satisfied. One shared fake keeps that stand-in in a single place instead of
// re-implementing it per package, and it is a fake rather than a mock on purpose:
// the ports are a handful of methods over plain record types, so asserting on the
// records that were written is both cheaper and far more revealing than asserting
// on which method was called.
package execstoretest

import (
	"context"
	"sync"
	"testing"

	"temporal-agents/internal/execstore"
)

// Store is an in-memory execstore.Store and execstore.PlanStore. The zero value
// is ready to use and records everything written to it; use Failing to stand in
// for a store outage.
type Store struct {
	// mu guards the recorded state: a workflow under test may write from several
	// activity goroutines.
	mu sync.Mutex
	// saved holds the executions in write order, so a test can inspect the start
	// write and the terminal write separately.
	saved []execstore.Execution
	// plans holds the stored plans by handle.
	plans map[string]execstore.Plan
	// err, when set, fails every operation, standing in for a store outage.
	err error
}

// Compile-time proof the fake satisfies the ports it is injected as.
var (
	_ execstore.Store     = (*Store)(nil)
	_ execstore.PlanStore = (*Store)(nil)
)

// New returns an empty store.
func New() *Store { return &Store{} }

// Failing returns a store whose every operation fails with err, which is how a
// test drives the "recording is a hard dependency" paths.
func Failing(err error) *Store { return &Store{err: err} }

// SaveExecution appends the record, or fails when the store is failing.
func (s *Store) SaveExecution(_ context.Context, e execstore.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, e)
	return nil
}

// ListExecutions returns every recorded execution. The filter is ignored: the
// filtering itself is SQL, and is covered by the adapter's own suite.
func (s *Store) ListExecutions(_ context.Context, _ execstore.Filter) ([]execstore.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.records(), nil
}

// SavePlan stores the plan under its handle, or fails when the store is failing.
func (s *Store) SavePlan(_ context.Context, plan execstore.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.plans == nil {
		s.plans = map[string]execstore.Plan{}
	}
	s.plans[plan.ID] = plan
	return nil
}

// Plan resolves a plan by handle, returning execstore.ErrNoSuchPlan for an
// unknown one so a caller can tell that apart from an outage.
func (s *Store) Plan(_ context.Context, id string) (execstore.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return execstore.Plan{}, s.err
	}
	plan, ok := s.plans[id]
	if !ok {
		return execstore.Plan{}, execstore.ErrNoSuchPlan
	}
	return plan, nil
}

// ListPlans returns the stored plans. The limit is ignored, for the same reason
// ListExecutions ignores its filter.
func (s *Store) ListPlans(_ context.Context, _ int) ([]execstore.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]execstore.Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	return out, nil
}

// Records returns the executions written so far, in write order.
func (s *Store) Records() []execstore.Execution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records()
}

// records copies the recorded executions. The caller must hold mu.
func (s *Store) records() []execstore.Execution {
	return append([]execstore.Execution{}, s.saved...)
}

// Last returns the most recent write, which for a settled workflow is its
// terminal record. It fails the test when nothing was recorded at all.
func (s *Store) Last(t testing.TB) execstore.Execution {
	t.Helper()
	recs := s.Records()
	if len(recs) == 0 {
		t.Fatal("expected the workflow to record its state, but nothing was written")
	}
	return recs[len(recs)-1]
}
