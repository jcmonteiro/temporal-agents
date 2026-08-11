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
	"encoding/binary"
	"fmt"
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
	// running holds the executions returned by the orchestrator's running listing.
	running []agenthub.Execution
	// executionStates holds explicit latest states returned by describe operations.
	// It is separate from running so a test can represent an execution that settles
	// between the list and describe calls.
	executionStates map[string]agenthub.Execution
	// recorded holds the durable execution records.
	recorded []agenthub.Execution
	// plans holds the plans by fleet ID.
	plans map[string]agenthub.Plan
	// schedules holds the schedule states.
	schedules []agenthub.ScheduleState
	// dismissals holds the dismissals by their identifier.
	dismissals map[string]agenthub.Dismissal
	// registrations holds the registered places by their probed directory.
	registrations map[string]agenthub.PlaceRegistration
	// directories holds what the inspector answers for a directory an operator
	// names. A directory that is not in it does not exist as far as the fake is
	// concerned, which is how a test drives the refusals.
	directories map[string]agenthub.RecordedPlace
	// notRepositories holds directories that exist but that no repository holds.
	notRepositories map[string]bool
	// launches holds what was started from the hub, by request identity.
	launches map[string]agenthub.Launch
	// started holds every submitted specification, in order, so a test can assert
	// what the orchestrator was asked to run.
	started []agenthub.StartSpec
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
	_ agenthub.PlaceStore       = (*Source)(nil)
	_ agenthub.PlaceInspector   = (*Source)(nil)
	_ agenthub.LaunchStore      = (*Source)(nil)
	_ agenthub.Launcher         = (*Source)(nil)
)

// New returns an empty source.
func New() *Source {
	return &Source{
		executionStates: map[string]agenthub.Execution{},
		plans:           map[string]agenthub.Plan{},
		dismissals:      map[string]agenthub.Dismissal{},
		registrations:   map[string]agenthub.PlaceRegistration{},
		directories:     map[string]agenthub.RecordedPlace{},
		notRepositories: map[string]bool{},
		launches:        map[string]agenthub.Launch{},
	}
}

// Failing returns a source whose every operation fails with err, which is how a
// test drives the "a dependency is unavailable" paths.
func Failing(err error) *Source {
	s := New()
	s.err = err
	return s
}

// Fail makes every later operation fail with err, standing in for a dependency
// that goes down while the world is already set up.
func (s *Source) Fail(err error) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		Places:      s,
		Inspector:   s,
		Launcher:    s,
		Launches:    s,
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

// ReplaceRunning replaces the live listing without changing its source paging
// identity. It lets a test represent current-run facts changing between requests.
func (s *Source) ReplaceRunning(execs ...agenthub.Execution) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = append([]agenthub.Execution(nil), execs...)
	return s
}

// WithExecutionState configures the latest state returned by a describe operation.
// The state need not be in the running listing, which represents an execution that
// settled between list and describe.
func (s *Source) WithExecutionState(execs ...agenthub.Execution) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executionStates == nil {
		s.executionStates = map[string]agenthub.Execution{}
	}
	for _, execution := range execs {
		s.executionStates[execution.WorkflowID] = execution
	}
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

// RunningPage implements the source-native paging half of ExecutionSource.
func (s *Source) RunningPage(_ context.Context, query agenthub.ExecutionPageQuery) (agenthub.ExecutionPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.ExecutionPage{}, s.err
	}
	offset, err := pageOffset(query.Cursor, len(s.running))
	if err != nil {
		return agenthub.ExecutionPage{}, err
	}
	end := min(offset+query.Limit, len(s.running))
	page := agenthub.ExecutionPage{Items: append([]agenthub.Execution(nil), s.running[offset:end]...)}
	if end < len(s.running) {
		page.Next = pageToken(end)
	}
	return page, nil
}

// Execution implements agenthub.ExecutionSource. An explicit describe state wins;
// otherwise an item in the running listing is still returned as running. An item in
// neither view is unknown.
func (s *Source) Execution(_ context.Context, workflowID string) (agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Execution{}, s.err
	}
	if execution, ok := s.executionStates[workflowID]; ok {
		return execution, nil
	}
	for _, e := range s.running {
		if e.WorkflowID == workflowID {
			return e, nil
		}
	}
	return agenthub.Execution{}, agenthub.ErrNoExecution
}

// Executions implements the batch half of agenthub.ExecutionSource.
func (s *Source) Executions(_ context.Context, workflowIDs []string) (map[string]agenthub.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	wanted := stringSet(workflowIDs)
	out := make(map[string]agenthub.Execution, len(workflowIDs))
	for workflowID, execution := range s.executionStates {
		if wanted[workflowID] {
			out[workflowID] = execution
		}
	}
	for _, execution := range s.running {
		if wanted[execution.WorkflowID] {
			if _, explicit := out[execution.WorkflowID]; !explicit {
				out[execution.WorkflowID] = execution
			}
		}
	}
	return out, nil
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
		if excluded[e.WorkflowID] || (e.ParentWorkflowID != "" && !e.Detached) || e.ScheduleID != "" || !runClass(e.Class) {
			continue
		}
		groups[e.WorkflowID] = append(groups[e.WorkflowID], e)
	}
	return requiredChains(groups, query.Limit, query.RequiredWorkflowIDs), nil
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

// SchedulePage implements source-native schedule paging.
func (s *Source) SchedulePage(_ context.Context, query agenthub.SchedulePageQuery) (agenthub.ScheduleStatePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.ScheduleStatePage{}, s.err
	}
	offset, err := pageOffset(query.Cursor, len(s.schedules))
	if err != nil {
		return agenthub.ScheduleStatePage{}, err
	}
	end := min(offset+query.Limit, len(s.schedules))
	page := agenthub.ScheduleStatePage{Items: append([]agenthub.ScheduleState(nil), s.schedules[offset:end]...)}
	if end < len(s.schedules) {
		page.Next = pageToken(end)
	}
	return page, nil
}

// ScheduleActionChains implements agenthub.CollectionSource.
func (s *Source) ScheduleActionChains(_ context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]agenthub.ExecutionChain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	wanted := stringSet(scheduleIDs)
	groups := make(map[string]map[string][]agenthub.Execution, len(scheduleIDs))
	for _, execution := range s.recorded {
		if !wanted[execution.ScheduleID] {
			continue
		}
		if groups[execution.ScheduleID] == nil {
			groups[execution.ScheduleID] = map[string][]agenthub.Execution{}
		}
		actionID := execution.FirstRunID
		if actionID == "" {
			actionID = execution.RunID
		}
		groups[execution.ScheduleID][actionID] = append(
			groups[execution.ScheduleID][actionID], execution)
	}
	out := make(map[string][]agenthub.ExecutionChain, len(scheduleIDs))
	for scheduleID, actions := range groups {
		out[scheduleID] = limitedChains(actions, perScheduleLimit)
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

func pageOffset(cursor []byte, size int) (int, error) {
	if len(cursor) == 0 {
		return 0, nil
	}
	if len(cursor) != 8 {
		return 0, fmt.Errorf("%w: invalid source cursor", agenthub.ErrInvalid)
	}
	offset := int(binary.BigEndian.Uint64(cursor))
	if offset < 0 || offset > size {
		return 0, fmt.Errorf("%w: invalid source cursor", agenthub.ErrInvalid)
	}
	return offset, nil
}

func pageToken(offset int) []byte {
	token := make([]byte, 8)
	binary.BigEndian.PutUint64(token, uint64(offset))
	return token
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

func requiredChains(groups map[string][]agenthub.Execution, limit int, requiredIDs []string) []agenthub.ExecutionChain {
	chains := limitedChains(groups, 0)
	required := stringSet(requiredIDs)
	if limit <= 0 || len(chains) <= limit {
		return chains
	}
	selected := append([]agenthub.ExecutionChain(nil), chains[:limit]...)
	known := make(map[string]bool, len(selected))
	for _, chain := range selected {
		known[chain.Latest.WorkflowID] = true
	}
	for _, chain := range chains[limit:] {
		workflowID := chain.Latest.WorkflowID
		if required[workflowID] && !known[workflowID] {
			selected = append(selected, chain)
		}
	}
	return selected
}

func limitedChains(groups map[string][]agenthub.Execution, limit int) []agenthub.ExecutionChain {
	chains := make([]agenthub.ExecutionChain, 0, len(groups))
	for _, executions := range groups {
		chain := agenthub.ExecutionChain{Iterations: len(executions)}
		var place agenthub.RecordedPlace
		var instructions []agenthub.InstructionUse
		for _, e := range executions {
			chain.Tokens += e.Tokens
			if chain.StartedAt.IsZero() || e.StartedAt.Before(chain.StartedAt) {
				chain.StartedAt = e.StartedAt
			}
			if !place.Recorded() {
				place = e.Place
			}
			if len(instructions) == 0 {
				instructions = e.Instructions
			}
			if chain.Latest.WorkflowID == "" || e.StartedAt.After(chain.Latest.StartedAt) ||
				e.StartedAt.Equal(chain.Latest.StartedAt) && e.RunID > chain.Latest.RunID {
				chain.Latest = e
			}
		}
		chain.Latest.StartedAt = chain.StartedAt
		chain.Latest.Tokens = chain.Tokens
		// A place is a fact about the chain: an iteration that recorded none must not
		// hide the one an earlier iteration established. The instructions are the same
		// kind of fact, resolved once per unit of work.
		if !chain.Latest.Place.Recorded() {
			chain.Latest.Place = place
		}
		if len(chain.Latest.Instructions) == 0 {
			chain.Latest.Instructions = instructions
		}
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
		FirstRunID: id + "-run",
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
		FirstRunID:       workflowID + "-run",
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
		FirstRunID: id + "-run",
		Class:      wfid.Classify(id),
		Outcome:    outcome,
		Label:      label,
		StartedAt:  startedAt,
	}
}

// WithDirectory makes directory answerable by the inspector, with the facts the
// probe would establish for it. A directory nothing was said about does not exist.
func (s *Source) WithDirectory(directory string, facts agenthub.RecordedPlace) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directories[directory] = facts
	return s
}

// WithUnversionedDirectory makes directory exist with no repository holding it.
func (s *Source) WithUnversionedDirectory(directory string) *Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notRepositories[directory] = true
	return s
}

// Inspect implements agenthub.PlaceInspector over what the test said is there.
func (s *Source) Inspect(_ context.Context, directory string) (agenthub.RecordedPlace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.RecordedPlace{}, s.err
	}
	if facts, ok := s.directories[directory]; ok {
		return facts, nil
	}
	if s.notRepositories[directory] {
		return agenthub.RecordedPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNotARepository, directory)
	}
	return agenthub.RecordedPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNoSuchDirectory, directory)
}

// Registrations implements agenthub.PlaceStore.
func (s *Source) Registrations(context.Context) ([]agenthub.PlaceRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]agenthub.PlaceRegistration, 0, len(s.registrations))
	for _, registration := range s.registrations {
		out = append(out, registration)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Place.Directory < out[j].Place.Directory })
	return out, nil
}

// Register implements agenthub.PlaceStore, idempotently on the probed directory
// exactly as the durable adapter must: a repeat keeps the original registration.
func (s *Source) Register(_ context.Context, registration agenthub.PlaceRegistration) (agenthub.PlaceRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.PlaceRegistration{}, s.err
	}
	if existing, ok := s.registrations[registration.Place.Directory]; ok {
		return existing, nil
	}
	s.registrations[registration.Place.Directory] = registration
	return registration, nil
}

// Start implements agenthub.Launcher by remembering what was submitted.
func (s *Source) Start(_ context.Context, spec agenthub.StartSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.started = append(s.started, spec)
	return nil
}

// Started returns what the orchestrator was asked to run, in order.
func (s *Source) Started() []agenthub.StartSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agenthub.StartSpec(nil), s.started...)
}

// Launch implements agenthub.LaunchStore, idempotently on the request identity
// exactly as the durable adapter must.
func (s *Source) Launch(_ context.Context, launch agenthub.Launch) (agenthub.Launch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Launch{}, s.err
	}
	if existing, ok := s.launches[launch.RequestID]; ok {
		return existing, nil
	}
	s.launches[launch.RequestID] = launch
	return launch, nil
}

// LaunchOfRun implements agenthub.LaunchStore.
func (s *Source) LaunchOfRun(_ context.Context, workflowID string) (agenthub.Launch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Launch{}, s.err
	}
	for _, launch := range s.launches {
		if launch.WorkflowID == workflowID {
			return launch, nil
		}
	}
	return agenthub.Launch{}, agenthub.ErrNotFound
}

// LaunchOf implements agenthub.LaunchStore.
func (s *Source) LaunchOf(_ context.Context, requestID string) (agenthub.Launch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return agenthub.Launch{}, s.err
	}
	if launch, ok := s.launches[requestID]; ok {
		return launch, nil
	}
	return agenthub.Launch{}, agenthub.ErrNotFound
}
