// Package agenthubtest provides in-memory implementations of the agenthub ports
// for tests.
//
// They are fakes, not mocks: the ports are a handful of reads over plain records,
// so a test that feeds in executions and asserts on the satellites that come out
// says something about the behaviour, while a test that asserts on which port
// method was called only restates the implementation. The same fake serves the
// core's tests and the HTTP adapter's, so there is one stand-in to keep truthful
// rather than one per package.
package agenthubtest

import (
	"context"
	"sort"
	"sync"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/wfid"
)

// Source is an in-memory implementation of every agenthub port. The zero value is
// an empty world: no executions, no plans, no schedules, nothing dismissed.
type Source struct {
	// mu guards the state, so a test may drive the service from several goroutines.
	mu sync.Mutex
	// running holds the executions the orchestrator is running now.
	running []agenthub.Execution
	// recorded holds the durable execution records.
	recorded []agenthub.Execution
	// plans holds the plans by fleet ID.
	plans map[string]agenthub.Plan
	// schedules holds the schedule states.
	schedules []agenthub.ScheduleState
	// dismissals holds the dismissals by their identifier.
	dismissals map[string]agenthub.Dismissal
	// err, when set, fails every operation, standing in for an unreachable
	// dependency.
	err error
}

// Compile-time proof the fake satisfies every port it is injected as.
var (
	_ agenthub.ExecutionSource  = (*Source)(nil)
	_ agenthub.RecordSource     = (*Source)(nil)
	_ agenthub.CollectionSource = (*Source)(nil)
	_ agenthub.PlanSource       = (*Source)(nil)
	_ agenthub.ScheduleSource   = (*Source)(nil)
	_ agenthub.DismissalStore   = (*Source)(nil)
)

// New returns an empty source.
func New() *Source {
	return &Source{plans: map[string]agenthub.Plan{}, dismissals: map[string]agenthub.Dismissal{}}
}

// Failing returns a source whose every operation fails with err, which is how a
// test drives the "a dependency is unavailable" paths.
func Failing(err error) *Source {
	s := New()
	s.err = err
	return s
}

// Dependencies wires the fake into every port at once, with a fixed clock so a
// recorded timestamp is assertable.
func (s *Source) Dependencies(now time.Time) agenthub.Dependencies {
	return agenthub.Dependencies{
		Live:        s,
		Collections: s,
		Plans:       s,
		Schedules:   s,
		Dismissals:  s,
		Now:         func() time.Time { return now },
	}
}

// WithRunning adds executions to the live listing, i.e. work the orchestrator is
// running right now.
func (s *Source) WithRunning(execs ...agenthub.Execution) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = append(s.running, execs...)
	return s
}

// WithRecorded adds executions to the durable record.
func (s *Source) WithRecorded(execs ...agenthub.Execution) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, execs...)
	return s
}

// WithPlan makes plan resolvable for the given fleet.
func (s *Source) WithPlan(fleetID string, plan agenthub.Plan) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[fleetID] = plan
	for i := range s.recorded {
		if s.recorded[i].WorkflowID == fleetID && s.recorded[i].Class == wfid.ClassFleet {
			s.recorded[i].PlanID = fleetID
		}
	}
	return s
}

// WithSchedules adds schedule states.
func (s *Source) WithSchedules(states ...agenthub.ScheduleState) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules = append(s.schedules, states...)
	return s
}

// WithDismissal marks an item as dismissed without going through the service, so
// a read test can start from a world where something is already hidden.
func (s *Source) WithDismissal(kind agenthub.ItemKind, itemID string) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := agenthub.Dismissal{Kind: kind, ItemID: itemID, DismissedAt: time.Unix(0, 0).UTC()}
	s.dismissals[d.ID()] = d
	return s
}

// RunningExecutions implements agenthub.ExecutionSource.
func (s *Source) RunningExecutions(_ context.Context, limit int) ([]agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := append([]agenthub.Execution(nil), s.running...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Execution implements agenthub.ExecutionSource: it answers from the live listing
// only, so an execution the record calls running but that is not running here is
// reported as unknown — the case a reader must not present as in progress.
func (s *Source) Execution(_ context.Context, workflowID string) (agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Execution{}, s.err
	}
	for _, e := range s.running {
		if e.WorkflowID == workflowID {
			return e, nil
		}
	}
	return agenthub.Execution{}, agenthub.ErrNoExecution
}

// RecordedExecutions implements agenthub.RecordSource, applying the same filters
// the durable record does: a class, a workflow together with its children, a
// schedule's runs, and a cap — newest first.
func (s *Source) RecordedExecutions(_ context.Context, q agenthub.RecordQuery) ([]agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	var out []agenthub.Execution
	for _, e := range s.recorded {
		switch {
		case q.Class != "" && e.Class != q.Class:
			continue
		case q.WorkflowID != "" && e.WorkflowID != q.WorkflowID && e.ParentWorkflowID != q.WorkflowID:
			continue
		case q.ScheduleID != "" && e.ScheduleID != q.ScheduleID:
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// RunChains implements agenthub.CollectionSource.
func (s *Source) RunChains(_ context.Context, query agenthub.ChainQuery) ([]agenthub.ExecutionChain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	excluded := stringSet(query.ExcludedWorkflowIDs)
	groups := map[string][]agenthub.Execution{}
	for _, e := range s.recorded {
		if query.WorkflowID != "" && e.WorkflowID != query.WorkflowID {
			continue
		}
		if excluded[e.WorkflowID] || e.ParentWorkflowID != "" || e.ScheduleID != "" || !runClass(e.Class) {
			continue
		}
		groups[e.WorkflowID] = append(groups[e.WorkflowID], e)
	}
	return limitedChains(groups, query.Limit), nil
}

// FleetTrees implements agenthub.CollectionSource.
func (s *Source) FleetTrees(_ context.Context, query agenthub.ChainQuery) ([]agenthub.FleetTree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	excluded := stringSet(query.ExcludedWorkflowIDs)
	groups := map[string][]agenthub.Execution{}
	for _, e := range s.recorded {
		if e.Class != wfid.ClassFleet || excluded[e.WorkflowID] || query.WorkflowID != "" && e.WorkflowID != query.WorkflowID {
			continue
		}
		groups[e.WorkflowID] = append(groups[e.WorkflowID], e)
	}
	chains := limitedChains(groups, query.Limit)
	trees := make([]agenthub.FleetTree, 0, len(chains))
	for _, chain := range chains {
		tree := agenthub.FleetTree{Chain: chain}
		for _, e := range s.recorded {
			if e.WorkflowID == chain.Latest.WorkflowID || e.ParentWorkflowID == chain.Latest.WorkflowID {
				tree.Executions = append(tree.Executions, e)
			}
		}
		trees = append(trees, tree)
	}
	return trees, nil
}

// ScheduleActions implements agenthub.CollectionSource.
func (s *Source) ScheduleActions(_ context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	wanted := stringSet(scheduleIDs)
	out := make(map[string][]agenthub.Execution, len(scheduleIDs))
	for _, e := range s.recorded {
		if wanted[e.ScheduleID] {
			out[e.ScheduleID] = append(out[e.ScheduleID], e)
		}
	}
	for id := range out {
		sort.SliceStable(out[id], func(i, j int) bool { return out[id][i].StartedAt.After(out[id][j].StartedAt) })
		if perScheduleLimit > 0 && len(out[id]) > perScheduleLimit {
			out[id] = out[id][:perScheduleLimit]
		}
	}
	return out, nil
}

// Plans implements agenthub.PlanSource.
func (s *Source) Plans(_ context.Context, refs []agenthub.PlanReference) (map[string]agenthub.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]agenthub.Plan, len(refs))
	for _, ref := range refs {
		if plan, ok := s.plans[ref.FleetID]; ok && ref.PlanID != "" {
			out[ref.FleetID] = plan
		}
	}
	return out, nil
}

// Schedules implements agenthub.ScheduleSource.
func (s *Source) Schedules(_ context.Context, limit int) ([]agenthub.ScheduleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := append([]agenthub.ScheduleState(nil), s.schedules...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Dismissals implements agenthub.DismissalStore.
func (s *Source) Dismissals(context.Context) ([]agenthub.Dismissal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]agenthub.Dismissal, 0, len(s.dismissals))
	for _, d := range s.dismissals {
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// Dismiss implements agenthub.DismissalStore, idempotently on the dismissal's
// identity exactly as the durable adapter must.
func (s *Source) Dismiss(_ context.Context, d agenthub.Dismissal) (agenthub.Dismissal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Dismissal{}, s.err
	}
	if existing, ok := s.dismissals[d.ID()]; ok {
		// Keep and return the original time: the item was already hidden, and a retry
		// must describe the resource that remains stored.
		return existing, nil
	}
	s.dismissals[d.ID()] = d
	return d, nil
}

// Undismiss implements agenthub.DismissalStore.
func (s *Source) Undismiss(_ context.Context, kind agenthub.ItemKind, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	id := agenthub.Dismissal{Kind: kind, ItemID: itemID}.ID()
	if _, ok := s.dismissals[id]; !ok {
		return agenthub.ErrNotFound
	}
	delete(s.dismissals, id)
	return nil
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func runClass(class wfid.Class) bool {
	switch class {
	case wfid.ClassRun, wfid.ClassDevelop, wfid.ClassReview, wfid.ClassPilot, wfid.ClassFleetPlan:
		return true
	default:
		return false
	}
}

func limitedChains(groups map[string][]agenthub.Execution, limit int) []agenthub.ExecutionChain {
	chains := make([]agenthub.ExecutionChain, 0, len(groups))
	for _, executions := range groups {
		chain := agenthub.ExecutionChain{Iterations: len(executions)}
		for _, e := range executions {
			chain.Tokens += e.Tokens
			if chain.StartedAt.IsZero() || e.StartedAt.Before(chain.StartedAt) {
				chain.StartedAt = e.StartedAt
			}
			if chain.Latest.WorkflowID == "" || e.StartedAt.After(chain.Latest.StartedAt) ||
				e.StartedAt.Equal(chain.Latest.StartedAt) && e.RunID > chain.Latest.RunID {
				chain.Latest = e
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

// Fleet builds a recorded fleet parent execution, for readable test setup.
func Fleet(id string, outcome agenthub.ExecutionOutcome, startedAt time.Time) agenthub.Execution {
	return agenthub.Execution{
		WorkflowID: id,
		RunID:      id + "-run",
		Class:      wfid.ClassFleet,
		Outcome:    outcome,
		StartedAt:  startedAt,
	}
}

// Node builds a recorded fleet-node execution for the given fleet and node.
func Node(fleetID, nodeID string, outcome agenthub.ExecutionOutcome, startedAt time.Time) agenthub.Execution {
	workflowID := wfid.FleetNodeWorkflowID(fleetID, nodeID)
	return agenthub.Execution{
		WorkflowID:       workflowID,
		RunID:            workflowID + "-run",
		Class:            wfid.ClassFleetNode,
		Outcome:          outcome,
		StartedAt:        startedAt,
		ParentWorkflowID: fleetID,
	}
}

// Run builds a recorded top-level run execution.
func Run(id, label string, outcome agenthub.ExecutionOutcome, startedAt time.Time) agenthub.Execution {
	return agenthub.Execution{
		WorkflowID: id,
		RunID:      id + "-run",
		Class:      wfid.Classify(id),
		Outcome:    outcome,
		Label:      label,
		StartedAt:  startedAt,
	}
}
