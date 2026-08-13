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
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

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
	// current holds the row view keyed by run ID, as the production upsert does.
	current map[string]execstore.Execution
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
	_ execstore.OverviewReader  = (*Store)(nil)
	_ execstore.PlanStore       = (*Store)(nil)
)

// New returns an empty store.
func New() *Store { return &Store{current: map[string]execstore.Execution{}} }

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
	if s.current == nil {
		s.current = map[string]execstore.Execution{}
	}
	previous, exists := s.current[e.RunID]
	if !exists || previous.Status == execstore.StatusRunning || e.Status != execstore.StatusRunning {
		s.current[e.RunID] = e
	}
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
	return s.currentRecords(), nil
}

// ListExecutionChains returns fully aggregated chains after identity selection.
func (s *Store) ListExecutionChains(_ context.Context, filter execstore.ChainFilter) ([]execstore.ExecutionChain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	return executionChains(s.currentRecords(), filter), nil
}

// ListExecutionChainPage returns one stable page of fully aggregated chains.
func (s *Store) ListExecutionChainPage(_ context.Context, filter execstore.ChainFilter) (execstore.ExecutionChainPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return execstore.ExecutionChainPage{}, err
	}
	return executionChainPage(s.currentRecords(), filter)
}

// ListExecutionTrees returns selected root chains with their direct children.
func (s *Store) ListExecutionTrees(_ context.Context, filter execstore.ChainFilter) ([]execstore.ExecutionTree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	executions := s.currentRecords()
	chains := executionChains(executions, filter)
	trees := make([]execstore.ExecutionTree, 0, len(chains))
	for _, chain := range chains {
		tree := execstore.ExecutionTree{Chain: chain}
		for _, execution := range executions {
			if execution.WorkflowID == chain.Latest.WorkflowID || execution.ParentWorkflowID == chain.Latest.WorkflowID {
				tree.Executions = append(tree.Executions, execution)
			}
		}
		trees = append(trees, tree)
	}
	return trees, nil
}

// ListExecutionTreePage returns one stable page of roots with direct children.
func (s *Store) ListExecutionTreePage(_ context.Context, filter execstore.ChainFilter) (execstore.ExecutionTreePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return execstore.ExecutionTreePage{}, err
	}
	executions := s.currentRecords()
	chainPage, err := executionChainPage(executions, filter)
	if err != nil {
		return execstore.ExecutionTreePage{}, err
	}
	trees := make([]execstore.ExecutionTree, 0, len(chainPage.Items))
	for _, chain := range chainPage.Items {
		tree := execstore.ExecutionTree{Chain: chain}
		for _, execution := range executions {
			if execution.WorkflowID == chain.Latest.WorkflowID || execution.ParentWorkflowID == chain.Latest.WorkflowID {
				tree.Executions = append(tree.Executions, execution)
			}
		}
		trees = append(trees, tree)
	}
	return execstore.ExecutionTreePage{Items: trees, Next: chainPage.Next}, nil
}

// ListScheduleActionChains returns a bounded action-chain sample for every schedule.
func (s *Store) ListScheduleActionChains(_ context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]execstore.ExecutionChain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	wanted := stringsOf(scheduleIDs)
	groups := make(map[string]map[string][]execstore.Execution, len(scheduleIDs))
	for _, execution := range s.currentRecords() {
		if !wanted[execution.ScheduleID] {
			continue
		}
		if groups[execution.ScheduleID] == nil {
			groups[execution.ScheduleID] = map[string][]execstore.Execution{}
		}
		actionID := execution.FirstRunID
		if actionID == "" {
			actionID = execution.RunID
		}
		groups[execution.ScheduleID][actionID] = append(
			groups[execution.ScheduleID][actionID], execution)
	}
	out := make(map[string][]execstore.ExecutionChain, len(scheduleIDs))
	for scheduleID, actions := range groups {
		out[scheduleID] = groupedExecutionChains(actions, perScheduleLimit)
	}
	return out, nil
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

// Plans resolves all existing handles in one read.
func (s *Store) Plans(_ context.Context, ids []string) (map[string]execstore.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.outage(); err != nil {
		return nil, err
	}
	out := make(map[string]execstore.Plan, len(ids))
	for _, id := range ids {
		if plan, ok := s.plans[id]; ok {
			out[id] = plan
		}
	}
	return out, nil
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

// currentRecords copies the upserted read view. The caller must hold mu.
func (s *Store) currentRecords() []execstore.Execution {
	out := make([]execstore.Execution, 0, len(s.current))
	for _, execution := range s.current {
		out = append(out, execution)
	}
	return out
}

func executionChains(executions []execstore.Execution, filter execstore.ChainFilter) []execstore.ExecutionChain {
	kinds := make(map[execstore.Kind]bool, len(filter.Kinds))
	for _, kind := range filter.Kinds {
		kinds[kind] = true
	}
	excluded := stringsOf(filter.ExcludedWorkflowIDs)
	groups := map[string][]execstore.Execution{}
	for _, execution := range executions {
		if len(kinds) > 0 && !kinds[execution.Kind] || excluded[execution.WorkflowID] {
			continue
		}
		if filter.WorkflowID != "" && execution.WorkflowID != filter.WorkflowID {
			continue
		}
		if (execution.ParentWorkflowID != "" && !execution.Detail.Detached) || execution.ScheduleID != "" {
			continue
		}
		groups[execution.WorkflowID] = append(groups[execution.WorkflowID], execution)
	}
	chains := groupedExecutionChains(groups, 0)
	required := stringsOf(filter.RequiredWorkflowIDs)
	if filter.Limit <= 0 || len(chains) <= filter.Limit {
		return chains
	}
	selected := append([]execstore.ExecutionChain(nil), chains[:filter.Limit]...)
	for _, chain := range chains[filter.Limit:] {
		if required[chain.Latest.WorkflowID] {
			selected = append(selected, chain)
		}
	}
	return selected
}

type chainCursor struct {
	StartedAt  time.Time `json:"startedAt"`
	WorkflowID string    `json:"workflowId"`
}

func executionChainPage(executions []execstore.Execution, filter execstore.ChainFilter) (execstore.ExecutionChainPage, error) {
	unbounded := filter
	unbounded.Limit = 0
	unbounded.RequiredWorkflowIDs = nil
	unbounded.Cursor = nil
	chains := executionChains(executions, unbounded)
	if len(filter.Cursor) > 0 {
		var cursor chainCursor
		if err := json.Unmarshal(filter.Cursor, &cursor); err != nil || cursor.StartedAt.IsZero() || cursor.WorkflowID == "" {
			return execstore.ExecutionChainPage{}, errors.New("invalid execution-chain cursor")
		}
		first := 0
		for first < len(chains) && (chains[first].StartedAt.After(cursor.StartedAt) ||
			chains[first].StartedAt.Equal(cursor.StartedAt) && chains[first].Latest.WorkflowID <= cursor.WorkflowID) {
			first++
		}
		chains = chains[first:]
	}
	limit := execstore.EffectiveLimit(filter.Limit, execstore.DefaultHistoryLimit)
	if len(chains) <= limit {
		return execstore.ExecutionChainPage{Items: chains}, nil
	}
	items := append([]execstore.ExecutionChain(nil), chains[:limit]...)
	required := stringsOf(filter.RequiredWorkflowIDs)
	for _, chain := range chains[limit:] {
		if required[chain.Latest.WorkflowID] {
			items = append(items, chain)
		}
	}
	last := chains[limit-1]
	next, err := json.Marshal(chainCursor{StartedAt: last.StartedAt, WorkflowID: last.Latest.WorkflowID})
	if err != nil {
		return execstore.ExecutionChainPage{}, err
	}
	return execstore.ExecutionChainPage{Items: items, Next: next}, nil
}

func groupedExecutionChains(groups map[string][]execstore.Execution, limit int) []execstore.ExecutionChain {
	chains := make([]execstore.ExecutionChain, 0, len(groups))
	for _, group := range groups {
		chain := execstore.ExecutionChain{Iterations: len(group)}
		for _, execution := range group {
			chain.Tokens += execution.Tokens
			if chain.StartedAt.IsZero() || execution.StartedAt.Before(chain.StartedAt) {
				chain.StartedAt = execution.StartedAt
			}
			if chain.Latest.WorkflowID == "" || execution.StartedAt.After(chain.Latest.StartedAt) ||
				execution.StartedAt.Equal(chain.Latest.StartedAt) && execution.RunID > chain.Latest.RunID {
				chain.Latest = execution
			}
		}
		chain.Latest.StartedAt = chain.StartedAt
		chain.Latest.Tokens = chain.Tokens
		chains = append(chains, chain)
	}
	sort.SliceStable(chains, func(i, j int) bool {
		if chains[i].StartedAt.Equal(chains[j].StartedAt) {
			return chains[i].Latest.WorkflowID < chains[j].Latest.WorkflowID
		}
		return chains[i].StartedAt.After(chains[j].StartedAt)
	})
	if limit > 0 && len(chains) > limit {
		chains = chains[:limit]
	}
	return chains
}

func stringsOf(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
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
