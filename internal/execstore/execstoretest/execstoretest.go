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
	"sort"
	"sync"
	"testing"

	"temporal-agents/internal/execstore"
)

// Store is an in-memory implementation of every execstore port. The zero value
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
	// healthy is how many operations still succeed before err takes effect, so a
	// test can put the outage between the start write and the terminal one (see
	// FailingAfter).
	healthy int
}

// Compile-time proof the fake satisfies the ports it is injected as.
var (
	_ execstore.ExecutionWriter = (*Store)(nil)
	_ execstore.ExecutionReader = (*Store)(nil)
	_ execstore.PlanStore       = (*Store)(nil)
)

// New returns an empty store.
func New() *Store { return &Store{} }

// Failing returns a store whose every operation fails with err, which is how a
// test drives the "a start write must succeed" paths.
func Failing(err error) *Store { return &Store{err: err} }

// FailingAfter returns a store whose first healthy operations succeed and whose
// later ones fail with err. It stands in for a store that goes down mid-execution,
// which is the only way to reach the terminal-write path with the start write
// already landed.
func FailingAfter(healthy int, err error) *Store { return &Store{err: err, healthy: healthy} }

// outage reports the error the next operation must fail with, consuming the
// allowance FailingAfter granted. The caller must hold mu.
func (s *Store) outage() error {
	if s.err == nil {
		return nil
	}
	if s.healthy > 0 {
		s.healthy--
		return nil
	}
	return s.err
}

// SaveExecution appends the record, or fails when the store is failing.
func (s *Store) SaveExecution(_ context.Context, e execstore.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return err
	}
	s.saved = append(s.saved, e)
	return nil
}

// ListExecutions returns every recorded execution. The filter is ignored: the
// filtering itself is SQL, and is covered by the adapter's own suite.
func (s *Store) ListExecutions(_ context.Context, _ execstore.Filter) ([]execstore.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	return s.records(), nil
}

// SavePlan stores the plan under its handle, or fails when the store is failing.
func (s *Store) SavePlan(_ context.Context, plan execstore.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return err
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
	if err := s.outage(); err != nil {
		return execstore.Plan{}, err
	}
	plan, ok := s.plans[id]
	if !ok {
		return execstore.Plan{}, execstore.ErrNoSuchPlan
	}
	return plan, nil
}

// ListPlans returns the stored plans, newest first. The limit is ignored, for the
// same reason ListExecutions ignores its filter, but the order is not: the port
// promises newest first, and a fake that returned map order would make a test
// asserting that order flaky and hide the ordering as a real requirement.
func (s *Store) ListPlans(_ context.Context, _ int) ([]execstore.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	out := make([]execstore.Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	// Ties on the creation time are broken by handle, the same way the adapter's SQL
	// does, so the order is total and the fake cannot return two different answers
	// for the same content.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
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
