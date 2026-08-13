package agenthub

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"temporal-agents/internal/wfid"
)

// ErrUnavailable wraps every failure that comes from a port rather than from the
// request: the orchestrator is unreachable, the record store is down, the
// dismissal store cannot be read. The transport answers those as a retryable
// condition, so a consumer can tell "your request was wrong" apart from "come
// back in a moment" without parsing messages.
var ErrUnavailable = errors.New("a dependency of the read path is unavailable")

// ErrStateChanged is returned when the item no longer has the state revision the
// operator reviewed. The client-supplied revision is only a precondition: the
// service always calculates the current revision before it writes a dismissal.
var ErrStateChanged = errors.New("the item state changed before it was dismissed")

// Limits on how much one read returns. The API is an overview, not a history
// browser: it is capped so a single request can never turn into a full table
// scan, and the caps are part of the published contract.
const (
	// DefaultLimit is how many items a collection returns when the caller asks for
	// no limit.
	DefaultLimit = 25
	// MaxLimit is the largest limit a collection accepts. A larger one is refused
	// rather than silently reduced, so a consumer is never quietly served something
	// other than what it asked for (see ValidateLimit).
	MaxLimit = 200
	// scheduleActionSample is how many of a schedule's fired runs one read looks at
	// to recover its label and the outcome of its most recent completed action. A
	// handful is enough: the newest run carries the label, and the newest settled one
	// the outcome, even when the last few firings are still in flight.
	scheduleActionSample = 10
	// liveLimit is how many in-flight executions one read looks at. It is
	// independent of the caller's limit: the live listing is what decides whether a
	// recorded execution is still running, so it must cover more than the page.
	liveLimit = 1000
)

// ValidateLimit checks a caller-supplied limit and resolves the default. A
// negative or over-cap limit is an error rather than a clamp: the response would
// otherwise silently disagree with the request.
func ValidateLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 0:
		return 0, fmt.Errorf("%w: limit must be a positive number", ErrInvalid)
	case limit > MaxLimit:
		return 0, fmt.Errorf("%w: limit must be at most %d", ErrInvalid, MaxLimit)
	default:
		return limit, nil
	}
}

// Dependencies are the driven ports the service reads and writes through. Every
// one of them is required: a missing port would degrade the overview silently,
// which is exactly what an operator cannot afford in a view they trust to answer
// "what is happening right now".
type Dependencies struct {
	// Live is the orchestrator's current state.
	Live ExecutionSource
	// Collections selects and aggregates durable collection resources.
	Collections CollectionSource
	// Plans resolves fleets' approved plans in batches.
	Plans PlanSource
	// Schedules lists the configured schedules.
	Schedules ScheduleSource
	// Dismissals is the operator's view state.
	Dismissals DismissalStore
	// Places is the registry of places an operator asked the hub to know about.
	Places PlaceStore
	// Inspector answers what a directory an operator names actually is, so a
	// registration is checked against the machine rather than believed.
	Inspector PlaceInspector
	// Launcher submits work to the orchestrator.
	Launcher Launcher
	// Launches remembers what was started from the hub, and by whom.
	Launches LaunchStore
	// Now supplies the current time, and defaults to time.Now. It is injectable so
	// a test can assert on a written timestamp.
	Now func() time.Time
}

// Service is the application core of the read API: it joins the live
// orchestration state with the durable record, reconciles a fleet's plan against
// the executions of its nodes, derives every status, and applies the operator's
// dismissals.
type Service struct {
	deps Dependencies
}

// NewService validates the wiring and returns the service. It reports a missing
// port instead of accepting it, so a misconfigured process fails at startup rather
// than serving a half-answered overview.
func NewService(deps Dependencies) (*Service, error) {
	switch {
	case deps.Live == nil:
		return nil, errors.New("the live execution source is required")
	case deps.Collections == nil:
		return nil, errors.New("the execution collection source is required")
	case deps.Plans == nil:
		return nil, errors.New("the plan source is required")
	case deps.Schedules == nil:
		return nil, errors.New("the schedule source is required")
	case deps.Dismissals == nil:
		return nil, errors.New("the dismissal store is required")
	case deps.Places == nil:
		return nil, errors.New("the place store is required")
	case deps.Inspector == nil:
		return nil, errors.New("the place inspector is required")
	case deps.Launcher == nil:
		return nil, errors.New("the launcher is required")
	case deps.Launches == nil:
		return nil, errors.New("the launch store is required")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}, nil
}

// Fleets returns the visible fleet satellites, newest first. There is no time
// window: a fleet stays visible until its current state is dismissed.
func (s *Service) Fleets(ctx context.Context, limit int) ([]Fleet, error) {
	return s.FleetsFor(ctx, LocalViewerID, limit)
}

// FleetsFor returns one viewer's fleet satellites. A dismissal applies only while
// the fleet still has the exact state that viewer acknowledged.
func (s *Service) FleetsFor(ctx context.Context, viewer ViewerID, limit int) ([]Fleet, error) {
	limit = resolveLimit(limit)
	dismissed, err := s.dismissedIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	required := make([]string, 0, len(live))
	for workflowID, execution := range live {
		if isFleet(execution) {
			required = append(required, workflowID)
		}
	}

	fleets := make([]Fleet, 0, limit)
	seen := make(map[string]bool)
	visible := make(map[string]bool)
	var cursor []byte
	for {
		page, err := s.deps.Collections.FleetTreePage(ctx, ChainQuery{
			RequiredWorkflowIDs: required,
			Limit:               collectionPageLimit(limit, dismissed.count(KindFleet)),
			Cursor:              cursor,
		})
		if err != nil {
			return nil, unavailable("read the recorded fleets", err)
		}
		itemIDs := make(map[string]bool, len(page.Items))
		trees := append(append([]FleetTree(nil), page.Items...), page.Required...)
		chains := make([]ExecutionChain, 0, len(trees))
		treesByID := make(map[string][]Execution, len(trees))
		var recorded []Execution
		for _, tree := range page.Items {
			itemIDs[tree.Chain.Latest.WorkflowID] = true
		}
		for _, tree := range trees {
			chains = append(chains, tree.Chain)
			treesByID[tree.Chain.Latest.WorkflowID] = tree.Executions
			recorded = append(recorded, tree.Chain.Latest)
			recorded = append(recorded, tree.Executions...)
		}
		live, err = s.addExecutionStates(ctx, live, recorded)
		if err != nil {
			return nil, err
		}
		candidateLive := boundedLiveCandidates(live, chainWorkflowIDs(chains), limit, isFleet)
		parents, err := s.resolveExecutionChains(ctx, chains, candidateLive, isFleet)
		if err != nil {
			return nil, err
		}
		unseen := parents[:0]
		for _, parent := range parents {
			if !seen[parent.WorkflowID] {
				seen[parent.WorkflowID] = true
				unseen = append(unseen, parent)
			}
		}
		plans, err := s.plansFor(ctx, unseen)
		if err != nil {
			return nil, err
		}
		for _, parent := range unseen {
			fleet, err := s.buildFleet(ctx, parent, treesByID[parent.WorkflowID], live, plans[parent.WorkflowID])
			if err != nil {
				return nil, err
			}
			if dismissed.has(KindFleet, fleet.ID, fleet.StateRevision()) {
				continue
			}
			fleets = append(fleets, fleet)
			visible[fleet.ID] = true
		}
		visibleItems := 0
		for id := range itemIDs {
			if visible[id] {
				visibleItems++
			}
		}
		if visibleItems >= limit || len(page.Next) == 0 {
			sort.SliceStable(fleets, func(i, j int) bool {
				if fleets[i].StartedAt.Equal(fleets[j].StartedAt) {
					return fleets[i].ID < fleets[j].ID
				}
				return fleets[i].StartedAt.After(fleets[j].StartedAt)
			})
			if len(fleets) > limit {
				fleets = fleets[:limit]
			}
			return fleets, nil
		}
		cursor = page.Next
		required = nil
	}
}

const (
	activeWorkCursorVersion   byte = 1
	activeWorkExecutionsPhase byte = 'e'
	activeWorkSchedulesPhase  byte = 's'
)

// ActiveWork returns one bounded source-native page of top-level active work.
// Running executions are read before schedules. A page can be empty when one
// Temporal page contains only child or schedule-fired executions; Next still lets
// the caller continue without this request scanning another source page.
func (s *Service) ActiveWork(ctx context.Context, query PageQuery) (Page[ActiveWorkItem], error) {
	limit := resolveLimit(query.Limit)
	phase, sourceCursor, err := decodeActiveWorkCursor(query.Cursor)
	if err != nil {
		return Page[ActiveWorkItem]{}, err
	}
	if phase == activeWorkSchedulesPhase {
		return s.activeSchedulePage(ctx, limit, sourceCursor)
	}

	sourcePage, err := s.deps.Live.RunningPage(ctx, ExecutionPageQuery{Limit: limit, Cursor: sourceCursor})
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return Page[ActiveWorkItem]{}, err
		}
		return Page[ActiveWorkItem]{}, unavailable("list a page of running executions", err)
	}
	items := make([]ActiveWorkItem, 0, len(sourcePage.Items))
	for _, execution := range sourcePage.Items {
		if !execution.Running() {
			continue
		}
		switch {
		case isFleet(execution):
			items = append(items, ActiveWorkItem{
				ID: execution.WorkflowID, Type: ActiveWorkFleet,
				Status: execution.Outcome.WorkStatus(), Running: true,
			})
		case isRunSatellite(execution):
			items = append(items, ActiveWorkItem{
				ID: execution.WorkflowID, Type: activeWorkType(execution.Class),
				Status: execution.Outcome.WorkStatus(), Running: true,
			})
		}
	}
	next := encodeActiveWorkCursor(activeWorkSchedulesPhase, nil)
	if len(sourcePage.Next) > 0 {
		next = encodeActiveWorkCursor(activeWorkExecutionsPhase, sourcePage.Next)
	}
	return Page[ActiveWorkItem]{Items: items, Next: next}, nil
}

func (s *Service) activeSchedulePage(ctx context.Context, limit int, cursor []byte) (Page[ActiveWorkItem], error) {
	sourcePage, err := s.deps.Schedules.SchedulePage(ctx, SchedulePageQuery{Limit: limit, Cursor: cursor})
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return Page[ActiveWorkItem]{}, err
		}
		return Page[ActiveWorkItem]{}, unavailable("list a page of schedules", err)
	}
	items := make([]ActiveWorkItem, 0, len(sourcePage.Items))
	for _, state := range sourcePage.Items {
		items = append(items, ActiveWorkItem{
			ID: state.ID, Type: ActiveWorkSchedule,
			Status: ScheduleStatus(state.Paused, 0, state.LastOutcome),
		})
	}
	var next []byte
	if len(sourcePage.Next) > 0 {
		next = encodeActiveWorkCursor(activeWorkSchedulesPhase, sourcePage.Next)
	}
	return Page[ActiveWorkItem]{Items: items, Next: next}, nil
}

func decodeActiveWorkCursor(cursor []byte) (byte, []byte, error) {
	if len(cursor) == 0 {
		return activeWorkExecutionsPhase, nil, nil
	}
	if len(cursor) < 2 || cursor[0] != activeWorkCursorVersion ||
		(cursor[1] != activeWorkExecutionsPhase && cursor[1] != activeWorkSchedulesPhase) {
		return 0, nil, fmt.Errorf("%w: cursor is invalid", ErrInvalid)
	}
	return cursor[1], append([]byte(nil), cursor[2:]...), nil
}

func encodeActiveWorkCursor(phase byte, sourceCursor []byte) []byte {
	cursor := make([]byte, 2, 2+len(sourceCursor))
	cursor[0], cursor[1] = activeWorkCursorVersion, phase
	return append(cursor, sourceCursor...)
}

func activeWorkType(class wfid.Class) ActiveWorkType {
	switch class {
	case wfid.ClassDevelop:
		return ActiveWorkDevelop
	case wfid.ClassReview:
		return ActiveWorkReview
	case wfid.ClassPilot:
		return ActiveWorkPilot
	case wfid.ClassFleetPlan:
		return ActiveWorkFleetPlan
	default:
		return ActiveWorkRun
	}
}

// Fleet returns one fleet with its whole node graph, or ErrNotFound. It is the
// read behind a fleet's own view, and the one place the plan's edges are exposed.
func (s *Service) Fleet(ctx context.Context, id string) (Fleet, error) {
	if err := ValidateItemID(id); err != nil {
		return Fleet{}, err
	}
	// The collection adapter selects this identity first and then returns its whole
	// tree, so a large execution history cannot truncate the graph.
	page, err := s.deps.Collections.FleetTreePage(ctx, ChainQuery{WorkflowID: id, Limit: 1})
	if err != nil {
		return Fleet{}, unavailable("read the fleet's records", err)
	}
	trees := page.Items
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return Fleet{}, err
	}
	var tree []Execution
	var chains []ExecutionChain
	if len(trees) > 0 {
		tree = trees[0].Executions
		chains = append(chains, trees[0].Chain)
		recorded := append([]Execution{trees[0].Chain.Latest}, tree...)
		live, err = s.addExecutionStates(ctx, live, recorded)
		if err != nil {
			return Fleet{}, err
		}
	}
	parents, err := s.resolveExecutionChains(ctx, chains, live, func(e Execution) bool {
		return e.WorkflowID == id && isFleet(e)
	})
	if err != nil {
		return Fleet{}, err
	}
	if len(parents) == 0 {
		// Nothing recorded under that ID: it may still be a fleet the orchestrator
		// knows (an execution older than the durable record), so ask before giving up.
		parent, lerr := s.deps.Live.Execution(ctx, id)
		if errors.Is(lerr, ErrNoExecution) {
			return Fleet{}, ErrNotFound
		}
		if lerr != nil {
			return Fleet{}, unavailable("read the fleet's live state", lerr)
		}
		if parent.Class != wfid.ClassFleet {
			return Fleet{}, ErrNotFound
		}
		parents = []resolvedChain{{Execution: parent, Iterations: 1}}
	}
	plans, err := s.plansFor(ctx, parents)
	if err != nil {
		return Fleet{}, err
	}
	return s.buildFleet(ctx, parents[0], tree, live, plans[id])
}

// Runs returns the visible independently represented run satellites, newest
// first. A dismissed chain appears again when its state changes.
//
// A chain that has continued as new any number of times is one satellite, keyed by
// its workflow ID and showing its latest iteration's status. A run fired by a
// schedule is not listed: its schedule represents it, so the overview shows one
// satellite for the schedule rather than one per firing. A fleet's node is not
// listed either: it belongs to its fleet.
func (s *Service) Runs(ctx context.Context, limit int) ([]Run, error) {
	return s.RunsFor(ctx, LocalViewerID, limit)
}

// RunsFor returns one viewer's run satellites. A changed run is not hidden by a
// dismissal of an earlier state, even though the workflow identity is unchanged.
func (s *Service) RunsFor(ctx context.Context, viewer ViewerID, limit int) ([]Run, error) {
	limit = resolveLimit(limit)
	dismissed, err := s.dismissedIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	required := make([]string, 0, len(live))
	for workflowID, execution := range live {
		if isRunSatellite(execution) {
			required = append(required, workflowID)
		}
	}

	runs := make([]Run, 0, limit)
	seen := make(map[string]bool)
	visible := make(map[string]bool)
	var cursor []byte
	for {
		page, err := s.deps.Collections.RunChainPage(ctx, ChainQuery{
			RequiredWorkflowIDs: required,
			Limit:               collectionPageLimit(limit, dismissed.count(KindRun)),
			Cursor:              cursor,
		})
		if err != nil {
			return nil, unavailable("read the recorded runs", err)
		}
		itemIDs := make(map[string]bool, len(page.Items))
		chains := append(append([]ExecutionChain(nil), page.Items...), page.Required...)
		recordedExecutions := make([]Execution, 0, len(chains))
		for _, chain := range page.Items {
			itemIDs[chain.Latest.WorkflowID] = true
		}
		for _, chain := range chains {
			recordedExecutions = append(recordedExecutions, chain.Latest)
		}
		live, err = s.addExecutionStates(ctx, live, recordedExecutions)
		if err != nil {
			return nil, err
		}
		candidateLive := boundedLiveCandidates(live, chainWorkflowIDs(chains), limit, isRunSatellite)
		resolved, err := s.resolveExecutionChains(ctx, chains, candidateLive, isRunSatellite)
		if err != nil {
			return nil, err
		}
		for _, chain := range resolved {
			if seen[chain.WorkflowID] {
				continue
			}
			seen[chain.WorkflowID] = true
			run, err := runFrom(chain)
			if err != nil {
				return nil, err
			}
			if dismissed.has(KindRun, run.ID, run.StateRevision()) {
				continue
			}
			runs = append(runs, run)
			visible[run.ID] = true
		}
		visibleItems := 0
		for id := range itemIDs {
			if visible[id] {
				visibleItems++
			}
		}
		if visibleItems >= limit || len(page.Next) == 0 {
			sort.SliceStable(runs, func(i, j int) bool {
				if runs[i].StartedAt.Equal(runs[j].StartedAt) {
					return runs[i].ID < runs[j].ID
				}
				return runs[i].StartedAt.After(runs[j].StartedAt)
			})
			if len(runs) > limit {
				runs = runs[:limit]
			}
			return runs, nil
		}
		cursor = page.Next
		required = nil
	}
}

// Run returns one run chain, or ErrNotFound.
func (s *Service) Run(ctx context.Context, id string) (Run, error) {
	if err := ValidateItemID(id); err != nil {
		return Run{}, err
	}
	page, err := s.deps.Collections.RunChainPage(ctx, ChainQuery{WorkflowID: id, Limit: 1})
	if err != nil {
		return Run{}, unavailable("read the run's records", err)
	}
	recorded := page.Items
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return Run{}, err
	}
	if len(recorded) > 0 {
		live, err = s.addExecutionStates(ctx, live, []Execution{recorded[0].Latest})
		if err != nil {
			return Run{}, err
		}
	}
	chains, err := s.resolveExecutionChains(ctx, recorded, live, func(e Execution) bool {
		return e.WorkflowID == id && isRunSatellite(e)
	})
	if err != nil {
		return Run{}, err
	}
	if len(chains) == 0 {
		live, lerr := s.deps.Live.Execution(ctx, id)
		if errors.Is(lerr, ErrNoExecution) {
			return Run{}, ErrNotFound
		}
		if lerr != nil {
			return Run{}, unavailable("read the run's live state", lerr)
		}
		chain := live
		if !isRunSatellite(chain) {
			return Run{}, ErrNotFound
		}
		chains = []resolvedChain{{Execution: chain, Iterations: 1}}
	}
	run, err := runFrom(chains[0])
	if err != nil {
		return Run{}, err
	}
	// Who started it is asked for one run and never for a collection: it is a fact
	// a person reads on a run's own page, and one query per listed run would make an
	// overview pay for it.
	run.StartedBy = s.startedBy(ctx, run.ID)
	return run, nil
}

// Schedules returns the schedule satellites: one per schedule, whatever its runs
// are doing. A schedule is recurring, so it carries no progress — there is no
// finite amount of work to be a fraction of — and it is never dismissible.
//
// A schedule has no prompt and no outcome of its own: what it asks of the agent,
// and how its latest firing went, are only visible in the runs it fired. So the
// label and the latest outcome come from the durable record, and "an action is
// running right now" comes from the live listing, where every scheduled execution
// is attributed to the schedule that started it.
func (s *Service) Schedules(ctx context.Context, limit int) ([]Schedule, error) {
	limit = resolveLimit(limit)
	states, err := s.deps.Schedules.Schedules(ctx, limit)
	if err != nil {
		return nil, unavailable("list the schedules", err)
	}
	return s.schedulesFromStates(ctx, states)
}

func (s *Service) schedulesFromStates(ctx context.Context, states []ScheduleState) ([]Schedule, error) {
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	running := map[string]int{}
	for _, e := range live {
		if e.ScheduleID != "" && e.Running() {
			running[e.ScheduleID]++
		}
	}

	scheduleIDs := make([]string, 0, len(states))
	for _, state := range states {
		scheduleIDs = append(scheduleIDs, state.ID)
	}
	actions, err := s.deps.Collections.ScheduleActionChains(ctx, scheduleIDs, scheduleActionSample)
	if err != nil {
		return nil, unavailable("read the schedules' runs", err)
	}
	var recordedActions []Execution
	for _, chains := range actions {
		for _, chain := range chains {
			recordedActions = append(recordedActions, chain.Latest)
		}
	}
	current, err := s.addExecutionStates(ctx, live, recordedActions)
	if err != nil {
		return nil, err
	}
	for scheduleID, chains := range actions {
		for i := range chains {
			if state, ok := current[chains[i].Latest.WorkflowID]; ok && sameExecutionChain(chains[i].Latest, state) {
				chains[i].Latest.Outcome = state.Outcome
				chains[i].Latest.RunID = state.RunID
				chains[i].Latest.EndedAt = state.EndedAt
			}
		}
		actions[scheduleID] = chains
	}

	schedules := make([]Schedule, 0, len(states))
	for _, state := range states {
		// A source page can carry eventually consistent action observations. The
		// established schedule resource derives current action state from the live
		// and durable execution sources reconciled above.
		state.RunningActions = 0
		state.LastOutcome = ""
		schedule, err := scheduleFrom(state, actions[state.ID], running[state.ID])
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, nil
}

func sameExecutionChain(recorded, live Execution) bool {
	if recorded.FirstRunID != "" && live.FirstRunID != "" {
		return recorded.FirstRunID == live.FirstRunID
	}
	return recorded.RunID != "" && recorded.RunID == live.RunID
}

// scheduleFrom assembles one schedule satellite from its state, the runs it fired
// (newest first) and how many of its actions are in flight.
//
// A schedule has no place of its own: it is configuration, and it runs nothing. The
// place it reports is the one its runs report, taken from the most recent firing
// that recorded one, so a schedule sits with the work it produces instead of in the
// unknown place.
func scheduleFrom(state ScheduleState, fired []ExecutionChain, runningActions int) (Schedule, error) {
	schedule := Schedule{
		ID:             state.ID,
		Spec:           state.Spec,
		Paused:         state.Paused,
		RunningActions: runningActions,
		LastRunAt:      state.LastRunAt,
		NextRunAt:      state.NextRunAt,
	}
	outcome := state.LastOutcome
	var place RecordedPlace
	for _, chain := range fired {
		action := chain.Latest
		if schedule.Label == "" {
			schedule.Label = action.Label
		}
		if !place.Recorded() {
			place = action.Place
		}
		if schedule.LastRunAt.IsZero() {
			schedule.LastRunAt = chain.StartedAt
		}
		// The status describes the most recent *completed* action, so an in-flight
		// firing is skipped here; that it is running is said by runningActions.
		if outcome == "" && !action.Running() {
			outcome = action.Outcome
		}
	}
	location, err := place.Location()
	if err != nil {
		return Schedule{}, err
	}
	schedule.Location = location
	schedule.Status = ScheduleStatus(state.Paused, runningActions, outcome)
	return schedule, nil
}

// Dismissals returns every dismissal in force, newest first. It is what lets a
// consumer show — and undo — what has been hidden.
func (s *Service) Dismissals(ctx context.Context) ([]Dismissal, error) {
	return s.DismissalsFor(ctx, LocalViewerID)
}

// DismissalsFor returns only one viewer's dismissals.
func (s *Service) DismissalsFor(ctx context.Context, viewer ViewerID) ([]Dismissal, error) {
	dismissals, err := s.deps.Dismissals.Dismissals(ctx, viewer)
	if err != nil {
		return nil, unavailable("read the dismissals", err)
	}
	sort.SliceStable(dismissals, func(i, j int) bool {
		return dismissals[i].DismissedAt.After(dismissals[j].DismissedAt)
	})
	return dismissals, nil
}

// Dismiss hides one exact fleet or run state from the overview and returns the
// dismissal it recorded. It refuses unknown work and stale state revisions.
// Dismissing an already-dismissed state succeeds and reports the dismissal, so a
// client that retries a lost response is not punished for it.
func (s *Service) Dismiss(ctx context.Context, kind ItemKind, itemID, expectedRevision string) (Dismissal, error) {
	return s.DismissFor(ctx, LocalViewerID, kind, itemID, expectedRevision)
}

// DismissFor hides the exact state one viewer reviewed.
func (s *Service) DismissFor(ctx context.Context, viewer ViewerID, kind ItemKind, itemID, expectedRevision string) (Dismissal, error) {
	if err := ValidateDismissalTarget(kind, itemID); err != nil {
		return Dismissal{}, err
	}
	if expectedRevision == "" {
		return Dismissal{}, fmt.Errorf("%w: stateRevision is required", ErrInvalid)
	}
	revision, err := s.currentStateRevision(ctx, kind, itemID)
	if err != nil {
		return Dismissal{}, err
	}
	if revision != expectedRevision {
		return Dismissal{}, ErrStateChanged
	}
	dismissal := Dismissal{
		Viewer: viewer, Kind: kind, ItemID: itemID, StateRevision: revision,
		DismissedAt: s.deps.Now().UTC(),
	}
	stored, err := s.deps.Dismissals.Dismiss(ctx, dismissal)
	if err != nil {
		return Dismissal{}, unavailable("record the dismissal", err)
	}
	return stored, nil
}

// Undismiss brings a dismissed item back, and reports ErrNotFound when it was not
// dismissed.
func (s *Service) Undismiss(ctx context.Context, kind ItemKind, itemID string) error {
	return s.UndismissFor(ctx, LocalViewerID, kind, itemID)
}

// UndismissFor removes only one viewer's dismissal.
func (s *Service) UndismissFor(ctx context.Context, viewer ViewerID, kind ItemKind, itemID string) error {
	if err := ValidateDismissalTarget(kind, itemID); err != nil {
		return err
	}
	err := s.deps.Dismissals.Undismiss(ctx, viewer, kind, itemID)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case err != nil:
		return unavailable("remove the dismissal", err)
	}
	return nil
}

// currentStateRevision resolves the exact observable state a dismissal will hide.
func (s *Service) currentStateRevision(ctx context.Context, kind ItemKind, itemID string) (string, error) {
	switch kind {
	case KindFleet:
		fleet, err := s.Fleet(ctx, itemID)
		if err != nil {
			return "", err
		}
		return fleet.StateRevision(), nil
	case KindRun:
		run, err := s.Run(ctx, itemID)
		if err != nil {
			return "", err
		}
		return run.StateRevision(), nil
	default:
		// ValidateDismissalTarget has already refused every other kind.
		return "", fmt.Errorf("%w: item kind %q cannot be dismissed", ErrInvalid, kind)
	}
}

// plansFor resolves all plans needed by a fleet page in one adapter call.
func (s *Service) plansFor(ctx context.Context, parents []resolvedChain) (map[string]Plan, error) {
	refs := make([]PlanReference, 0, len(parents))
	for _, parent := range parents {
		if parent.PlanID != "" {
			refs = append(refs, PlanReference{FleetID: parent.WorkflowID, PlanID: parent.PlanID})
		}
	}
	if len(refs) == 0 {
		return map[string]Plan{}, nil
	}
	plans, err := s.deps.Plans.Plans(ctx, refs)
	if err != nil {
		return nil, unavailable("resolve the fleets' plans", err)
	}
	return plans, nil
}

// buildFleet reconciles one fleet: its plan's nodes against the executions of
// those nodes, then the aggregated status and progress over the result.
//
// tree, when non-nil, is a previously read snapshot of the fleet and its children
// (see Fleet); otherwise the children are read here. A fleet whose plan cannot be
// resolved is still returned — with no nodes and its own execution's status — so a
// plan the store has lost hides the graph rather than the fleet.
func (s *Service) buildFleet(ctx context.Context, parent resolvedChain, tree []Execution, live map[string]Execution, plan Plan) (Fleet, error) {
	location, err := parent.Place.Location()
	if err != nil {
		return Fleet{}, err
	}
	fleet := Fleet{
		ID:        parent.WorkflowID,
		Running:   parent.Running(),
		Goal:      parent.Label,
		PlanID:    parent.PlanID,
		StartedAt: parent.StartedAt,
		EndedAt:   parent.EndedAt,
		Location:  location,
	}

	if len(plan.Nodes) == 0 && plan.Goal == "" {
		fleet.Status = parent.Outcome.WorkStatus()
		return fleet, nil
	}
	if plan.Goal != "" {
		fleet.Goal = plan.Goal
	}

	executions, outcomes, places, err := s.nodeExecutions(ctx, parent, tree, live)
	if err != nil {
		return Fleet{}, err
	}
	statuses := DeriveNodeStatuses(plan, outcomes)
	fleet.Nodes = make([]FleetNode, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		// A node develops in a worktree of its own, so its place is its own execution's,
		// never its fleet's. A node that never started has none, which is the unknown
		// place.
		nodeLocation, err := places[n.ID].Location()
		if err != nil {
			return Fleet{}, err
		}
		fleet.Nodes = append(fleet.Nodes, FleetNode{
			ID:        n.ID,
			Prompt:    n.Prompt,
			DependsOn: n.DependsOn,
			Status:    statuses[n.ID],
			Execution: executions[n.ID],
			Location:  nodeLocation,
		})
	}
	fleet.Progress = NodeProgress(fleet.Nodes)
	fleet.Status = fleetStatus(parent, fleet.Nodes)
	return fleet, nil
}

// fleetStatus aggregates the fleet's nodes and then reconciles the result with
// what the fleet's own execution did. The aggregation is the published precedence
// (see AggregateStatus); the two guards on top of it exist because the parent
// execution is a fact of its own:
//
//   - a fleet whose own execution failed is failed, even when its nodes look
//     fine: an orchestration that could not run (an invalid plan, a missing
//     worktrees directory) has nodes that never started, and reporting that as
//     "todo" would hide the failure.
//   - a fleet whose own execution is still running is never reported done: the
//     orchestration still has work to do (its summary, its notifications) after the
//     last node settled.
func fleetStatus(parent resolvedChain, nodes []FleetNode) WorkStatus {
	status := AggregateStatus(nodes)
	switch {
	case parent.Outcome == OutcomeFailed:
		return StatusFailed
	case parent.Running() && status == StatusDone:
		return StatusInProgress
	default:
		return status
	}
}

// nodeExecutions matches a fleet's child executions to its plan nodes by the
// workflow-ID convention, and works out each node's outcome and where it ran.
//
// A node's own child execution is the primary source. The fleet parent's recorded
// per-node breakdown is layered on top for settled nodes, because it is the only
// source for the two outcomes a child execution cannot express: a node that was
// skipped (it has no execution at all) and a node that stopped in a recoverable
// way and needs a human. A node whose child is running keeps "running": a
// breakdown entry then belongs to an earlier attempt.
func (s *Service) nodeExecutions(ctx context.Context, parent resolvedChain, tree []Execution, live map[string]Execution) (map[string]*NodeExecution, map[string]ExecutionOutcome, map[string]RecordedPlace, error) {
	byNode := make(map[string][]Execution)
	for _, e := range tree {
		nodeID, ok := wfid.FleetNodeID(parent.WorkflowID, e.WorkflowID)
		if !ok {
			continue
		}
		byNode[nodeID] = append(byNode[nodeID], e)
	}
	// A running child that the record has not caught up with yet still belongs to
	// its node, so the live listing is merged in as well.
	for _, e := range live {
		if nodeID, ok := wfid.FleetNodeID(parent.WorkflowID, e.WorkflowID); ok {
			byNode[nodeID] = append(byNode[nodeID], e)
		}
	}

	executions := make(map[string]*NodeExecution, len(byNode))
	outcomes := make(map[string]ExecutionOutcome, len(byNode))
	places := make(map[string]RecordedPlace, len(byNode))
	for nodeID, execs := range byNode {
		nodeWorkflowID := wfid.FleetNodeWorkflowID(parent.WorkflowID, nodeID)
		chains, err := s.resolveChains(ctx, execs, live, func(e Execution) bool {
			return e.WorkflowID == nodeWorkflowID
		})
		if err != nil {
			return nil, nil, nil, err
		}
		if len(chains) == 0 {
			continue
		}
		chain := chains[0]
		executions[nodeID] = &NodeExecution{
			WorkflowID: chain.WorkflowID,
			RunID:      chain.RunID,
			StartedAt:  chain.StartedAt,
			EndedAt:    chain.EndedAt,
			Tokens:     chain.Tokens,
		}
		outcomes[nodeID] = chain.Outcome
		places[nodeID] = chain.Place
	}
	for _, recorded := range parent.NodeOutcomes {
		if outcomes[recorded.NodeID] == OutcomeRunning {
			continue
		}
		outcomes[recorded.NodeID] = recorded.Outcome
	}
	return executions, outcomes, places, nil
}

func chainWorkflowIDs(chains []ExecutionChain) map[string]bool {
	ids := make(map[string]bool, len(chains))
	for _, chain := range chains {
		ids[chain.Latest.WorkflowID] = true
	}
	return ids
}

// boundedLiveCandidates keeps the live identities already selected by the record
// page and the newest limit identities from the live source. Full live state
// remains available to fleet-node reconciliation; this bounded map controls which
// parent resources and plans can enter one collection result.
func boundedLiveCandidates(live map[string]Execution, selected map[string]bool, limit int, keep func(Execution) bool) map[string]Execution {
	candidates := make(map[string]Execution, len(selected)+limit)
	for id := range selected {
		if execution, ok := live[id]; ok {
			candidates[id] = execution
		}
	}
	ordered := make([]Execution, 0, len(live))
	for _, execution := range live {
		if keep(execution) {
			ordered = append(ordered, execution)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartedAt.Equal(ordered[j].StartedAt) {
			return ordered[i].WorkflowID < ordered[j].WorkflowID
		}
		return ordered[i].StartedAt.After(ordered[j].StartedAt)
	})
	for i, execution := range ordered {
		if i == limit {
			break
		}
		candidates[execution.WorkflowID] = execution
	}
	return candidates
}

// resolvedChain is one execution chain after collapsing: the chain's latest
// iteration, with the facts that only the whole chain has (how many iterations it
// has looped through). It is internal to the read path — a source never fills it —
// which is why it is not part of the port's Execution type.
type resolvedChain struct {
	Execution
	// Iterations is how many continue-as-new iterations of the chain are known.
	Iterations int
}

// resolveExecutionChains reconciles already-aggregated durable chains with live
// state. Collection limits have already been applied to chain identities, never to
// their rows.
func (s *Service) resolveExecutionChains(ctx context.Context, recorded []ExecutionChain, live map[string]Execution, keep func(Execution) bool) ([]resolvedChain, error) {
	chains := make(map[string]resolvedChain, len(recorded))
	for _, chain := range recorded {
		if !keep(chain.Latest) {
			continue
		}
		latest := chain.Latest
		latest.StartedAt = chain.StartedAt
		latest.Tokens = chain.Tokens
		chains[latest.WorkflowID] = resolvedChain{Execution: latest, Iterations: chain.Iterations}
	}
	for _, execution := range live {
		if !keep(execution) {
			continue
		}
		if _, known := chains[execution.WorkflowID]; !known {
			chains[execution.WorkflowID] = resolvedChain{Execution: execution, Iterations: 1}
		}
	}

	resolved := make([]resolvedChain, 0, len(chains))
	for id, chain := range chains {
		latest := chain.Execution
		if current, running := live[id]; running {
			if current.RunID != "" && latest.RunID != "" && current.RunID != latest.RunID {
				chain.Iterations++
			}
			latest.Outcome = current.Outcome
			latest.EndedAt = current.EndedAt
			if current.RunID != "" {
				latest.RunID = current.RunID
			}
		} else if latest.Running() {
			settled, err := s.settle(ctx, latest)
			if err != nil {
				return nil, err
			}
			latest = settled
		}
		chain.Execution = latest
		resolved = append(resolved, chain)
	}
	sort.SliceStable(resolved, func(i, j int) bool {
		if !resolved[i].StartedAt.Equal(resolved[j].StartedAt) {
			return resolved[i].StartedAt.After(resolved[j].StartedAt)
		}
		return resolved[i].WorkflowID < resolved[j].WorkflowID
	})
	return resolved, nil
}

// resolveChains collapses executions into one entry per workflow ID and settles
// each one's current outcome against the live state, newest first.
//
// Collapsing is what keeps a chained run one satellite: every continue-as-new
// iteration shares the workflow ID, so the chain is the item and its latest
// iteration supplies the status. keep decides which executions belong in the
// caller's collection at all, and gates both sources, so an execution the record
// excludes cannot slip in through the live listing.
func (s *Service) resolveChains(ctx context.Context, recorded []Execution, live map[string]Execution, keep func(Execution) bool) ([]resolvedChain, error) {
	chains := map[string]Execution{}
	iterations := map[string]int{}
	for _, e := range recorded {
		if !keep(e) {
			continue
		}
		merge(chains, iterations, e)
	}
	// An execution the orchestrator is running but the record does not have (one
	// older than the record itself) is still real work, so it is added rather than
	// hidden.
	for _, e := range live {
		if !keep(e) {
			continue
		}
		if _, known := chains[e.WorkflowID]; !known {
			merge(chains, iterations, e)
		}
	}

	resolved := make([]resolvedChain, 0, len(chains))
	for id, latest := range chains {
		if current, running := live[id]; running {
			// The orchestrator is authoritative about what is happening now. Its latest
			// iteration can be newer than the durable record (the start write has not
			// landed yet), in which case it is one more known iteration of the chain.
			if current.RunID != "" && latest.RunID != "" && current.RunID != latest.RunID {
				iterations[id]++
			}
			latest.Outcome = current.Outcome
			latest.EndedAt = current.EndedAt
			if current.RunID != "" {
				latest.RunID = current.RunID
			}
		} else if latest.Running() {
			settled, err := s.settle(ctx, latest)
			if err != nil {
				return nil, err
			}
			latest = settled
		}
		resolved = append(resolved, resolvedChain{Execution: latest, Iterations: iterations[id]})
	}
	sort.SliceStable(resolved, func(i, j int) bool {
		if !resolved[i].StartedAt.Equal(resolved[j].StartedAt) {
			return resolved[i].StartedAt.After(resolved[j].StartedAt)
		}
		return resolved[i].WorkflowID < resolved[j].WorkflowID
	})
	return resolved, nil
}

// settle resolves an execution the record still calls running but that is not in
// the live listing. Asking the orchestrator directly is the only honest answer:
// it either confirms the execution (it is running, or it settled between the two
// reads) or it does not know it at all, which means the execution stopped without
// ever recording how — it was terminated, or its worker died — and reporting it as
// still in progress would be a claim nothing supports.
func (s *Service) settle(ctx context.Context, chain Execution) (Execution, error) {
	current, err := s.deps.Live.Execution(ctx, chain.WorkflowID)
	switch {
	case errors.Is(err, ErrNoExecution):
		chain.Outcome = OutcomeFailed
		return chain, nil
	case err != nil:
		return Execution{}, unavailable("read the execution's live state", err)
	}
	chain.Outcome = current.Outcome
	chain.EndedAt = current.EndedAt
	if current.RunID != "" {
		chain.RunID = current.RunID
	}
	return chain, nil
}

// merge folds one iteration into its chain: the newest iteration supplies the
// chain's state, the earliest its start time, and the tokens are summed because
// each iteration records only its own usage.
func merge(chains map[string]Execution, iterations map[string]int, e Execution) {
	iterations[e.WorkflowID]++
	existing, ok := chains[e.WorkflowID]
	if !ok {
		chains[e.WorkflowID] = e
		return
	}
	tokens := existing.Tokens + e.Tokens
	startedAt := existing.StartedAt
	if !e.StartedAt.IsZero() && (startedAt.IsZero() || e.StartedAt.Before(startedAt)) {
		startedAt = e.StartedAt
	}
	newest, older := existing, e
	if newer(e, existing) {
		newest, older = e, existing
	}
	newest.Tokens = tokens
	newest.StartedAt = startedAt
	// A later iteration carries the same prompt, plan and schedule as the first, but
	// a source that does not know them would drop them; keep whichever iteration has
	// the fact.
	newest.Label = firstNonEmpty(newest.Label, older.Label)
	newest.PlanID = firstNonEmpty(newest.PlanID, older.PlanID)
	newest.ScheduleID = firstNonEmpty(newest.ScheduleID, older.ScheduleID)
	newest.ParentWorkflowID = firstNonEmpty(newest.ParentWorkflowID, older.ParentWorkflowID)
	newest.Detached = newest.Detached || older.Detached
	// A place is a fact about the chain, not about one iteration: an iteration
	// recorded before the probe existed (or one whose probe failed) must not erase
	// the place a sibling iteration established.
	if !newest.Place.Recorded() {
		newest.Place = older.Place
	}
	if len(newest.NodeOutcomes) == 0 {
		newest.NodeOutcomes = older.NodeOutcomes
	}
	// The instructions are a fact about the chain too: they are resolved once per
	// unit of work and travel across continue-as-new, so an iteration that recorded
	// none must not erase what a sibling recorded.
	if len(newest.Instructions) == 0 {
		newest.Instructions = older.Instructions
	}
	// Waiting is deliberately *not* inherited from an older iteration: only the
	// iteration that is waiting is waiting, and a finished pass that once waited must
	// never make the chain look like it still does.
	chains[e.WorkflowID] = newest
}

// firstNonEmpty returns a when it carries a value, and b otherwise.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// newer reports whether candidate is a later iteration than current, by start
// time and then by run ID so the choice is deterministic when two iterations share
// a timestamp.
func newer(candidate, current Execution) bool {
	if !candidate.StartedAt.Equal(current.StartedAt) {
		return candidate.StartedAt.After(current.StartedAt)
	}
	return candidate.RunID > current.RunID
}

// runFrom projects a resolved chain onto the run satellite the API publishes.
//
// It fails only when the chain's recorded place cannot be expressed as a location,
// which is a defect in what was recorded rather than something the run itself did
// (see RecordedPlace.Location).
func runFrom(chain resolvedChain) (Run, error) {
	location, err := chain.Place.Location()
	if err != nil {
		return Run{}, err
	}
	return Run{
		ID:           chain.WorkflowID,
		Running:      chain.Running(),
		Type:         runType(chain.Class),
		Label:        chain.Label,
		Status:       chain.Status(),
		StartedAt:    chain.StartedAt,
		EndedAt:      chain.EndedAt,
		Iterations:   max(chain.Iterations, 1),
		Tokens:       chain.Tokens,
		Location:     location,
		Instructions: chain.Instructions,
	}, nil
}

// runType names the command a run came from, defaulting to a plain run for an ID
// that matches no other convention.
func runType(class wfid.Class) RunType {
	switch class {
	case wfid.ClassDevelop:
		return RunTypeDevelop
	case wfid.ClassReview:
		return RunTypeReview
	case wfid.ClassPilot:
		return RunTypePilot
	case wfid.ClassFleetPlan:
		return RunTypeFleetPlan
	default:
		return RunTypePrompt
	}
}

// runClasses lists the execution classes a run satellite can be. A fleet parent
// and a fleet node are excluded (a fleet is its own kind of item, and a node
// belongs to its fleet), as is the open-PR stage, which only ever runs inside a
// develop pipeline.
func runClasses() []wfid.Class {
	return []wfid.Class{
		wfid.ClassRun, wfid.ClassDevelop, wfid.ClassReview,
		wfid.ClassPilot, wfid.ClassFleetPlan,
	}
}

// isRunSatellite reports whether an execution belongs on the overview as a run
// satellite of its own.
//
// A supervised child belongs to whatever started it (a fleet's node to its fleet,
// or a review to a supervising develop run). A detached child has independent
// lifecycle ownership after its parent closes, so it is a satellite despite the
// parent correlation. A schedule-fired run remains represented by its schedule.
func isRunSatellite(e Execution) bool {
	if (e.ParentWorkflowID != "" && !e.Detached) || e.ScheduleID != "" {
		return false
	}
	for _, c := range runClasses() {
		if c == e.Class {
			return true
		}
	}
	return false
}

// isFleet reports whether an execution is a fleet parent.
func isFleet(e Execution) bool { return e.Class == wfid.ClassFleet }

// liveByWorkflowID indexes the in-flight executions by workflow ID, which is how
// every read finds out whether a recorded execution is still running.
func (s *Service) liveByWorkflowID(ctx context.Context) (map[string]Execution, error) {
	return s.liveByWorkflowIDWithLimit(ctx, liveLimit)
}

func (s *Service) liveByWorkflowIDWithLimit(ctx context.Context, limit int) (map[string]Execution, error) {
	running, err := s.deps.Live.RunningExecutions(ctx, limit)
	if err != nil {
		return nil, unavailable("list the running executions", err)
	}
	live := make(map[string]Execution, len(running))
	for _, e := range running {
		live[e.WorkflowID] = e
	}
	return live, nil
}

// addExecutionStates batch-resolves durable rows that still say running but are
// absent from the open-execution listing. Unknown chains are marked failed, which
// is the same honest fallback as settle without one call per row.
func (s *Service) addExecutionStates(ctx context.Context, states map[string]Execution, recorded []Execution) (map[string]Execution, error) {
	latest := make(map[string]Execution)
	for _, execution := range recorded {
		current, known := latest[execution.WorkflowID]
		if !known || newer(execution, current) {
			latest[execution.WorkflowID] = execution
		}
	}
	stale := make(map[string]Execution)
	for id, execution := range latest {
		if execution.Running() {
			if _, current := states[id]; !current {
				stale[id] = execution
			}
		}
	}
	if len(stale) == 0 {
		return states, nil
	}
	ids := make([]string, 0, len(stale))
	for id := range stale {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	resolved, err := s.deps.Live.Executions(ctx, ids)
	if err != nil {
		return nil, unavailable("read the executions' live state", err)
	}
	for _, id := range ids {
		if execution, ok := resolved[id]; ok {
			states[id] = execution
			continue
		}
		execution := stale[id]
		execution.Outcome = OutcomeFailed
		states[id] = execution
	}
	return states, nil
}

// dismissalSet is one viewer's dismissals indexed by item identity. The stored
// value is the exact state that viewer acknowledged.
type dismissalSet map[string]string

// count reports how many dismissals can affect one collection kind. A run
// dismissal must not make a fleet page resolve more complete plan trees.
func (d dismissalSet) count(kind ItemKind) int {
	prefix := string(kind) + ":"
	var count int
	for id := range d {
		if len(id) >= len(prefix) && id[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}

// collectionPageLimit compensates for likely exact dismissals without allowing
// one source read to grow past the public collection bound. Further candidates
// are reached through the stable cursor.
func collectionPageLimit(limit, dismissed int) int {
	if dismissed >= MaxLimit-limit {
		return MaxLimit
	}
	return limit + dismissed
}

// has reports whether this exact item state is dismissed.
func (d dismissalSet) has(kind ItemKind, itemID, revision string) bool {
	dismissedRevision, ok := d[Dismissal{Kind: kind, ItemID: itemID}.ID()]
	return ok && dismissedRevision == revision
}

// dismissedIDs reads one viewer's dismissals. A failure is reported rather than
// ignored: a silently empty set would put hidden items back on the overview.
func (s *Service) dismissedIDs(ctx context.Context, viewer ViewerID) (dismissalSet, error) {
	dismissals, err := s.deps.Dismissals.Dismissals(ctx, viewer)
	if err != nil {
		return nil, unavailable("read the dismissals", err)
	}
	set := make(dismissalSet, len(dismissals))
	for _, d := range dismissals {
		set[d.ID()] = d.StateRevision
	}
	return set, nil
}

// resolveLimit applies the service's own bounds to a limit, so a caller that
// skipped ValidateLimit (or passed nothing at all) still cannot ask for an
// unbounded read.
func resolveLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

// unavailable marks a port failure as the retryable condition it is, keeping the
// original error in the chain for the log while giving the transport a sentinel to
// branch on.
func unavailable(what string, err error) error {
	return errors.Join(ErrUnavailable, fmt.Errorf("%s: %w", what, err))
}
