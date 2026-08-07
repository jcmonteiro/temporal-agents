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

// ErrNotDismissible is returned when a dismissal is asked for an item that is
// still active. Dismissing is view state over *finished* work, so the rule is
// enforced here rather than trusted to the client that offers the affordance.
var ErrNotDismissible = errors.New("only a finished item can be dismissed")

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
	// recordScanLimit is how many durable executions an internal reconciliation can
	// inspect. It is larger than a collection page because one fleet can have many
	// children and one chain can have many iterations; the durable record applies the
	// same hard cap.
	recordScanLimit = 1000
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
	// Records is the durable execution record for detail reads.
	Records RecordSource
	// Collections selects and aggregates durable collection resources.
	Collections CollectionSource
	// Plans resolves fleets' approved plans in batches.
	Plans PlanSource
	// Schedules lists the configured schedules.
	Schedules ScheduleSource
	// Dismissals is the operator's view state.
	Dismissals DismissalStore
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
	case deps.Records == nil:
		return nil, errors.New("the execution record source is required")
	case deps.Collections == nil:
		return nil, errors.New("the execution collection source is required")
	case deps.Plans == nil:
		return nil, errors.New("the plan source is required")
	case deps.Schedules == nil:
		return nil, errors.New("the schedule source is required")
	case deps.Dismissals == nil:
		return nil, errors.New("the dismissal store is required")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}, nil
}

// Fleets returns the fleet satellites, newest first: every running fleet plus
// every finished fleet the operator has not dismissed. There is no time window —
// a finished fleet stays until it is dismissed, so nothing disappears from the
// overview on its own.
func (s *Service) Fleets(ctx context.Context, limit int) ([]Fleet, error) {
	limit = resolveLimit(limit)
	dismissed, err := s.dismissedIDs(ctx)
	if err != nil {
		return nil, err
	}
	trees, err := s.deps.Collections.FleetTrees(ctx, ChainQuery{
		ExcludedWorkflowIDs: dismissed.ids(KindFleet),
		Limit:               limit,
	})
	if err != nil {
		return nil, unavailable("read the recorded fleets", err)
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	chains := make([]ExecutionChain, 0, len(trees))
	treesByID := make(map[string][]Execution, len(trees))
	for _, tree := range trees {
		chains = append(chains, tree.Chain)
		treesByID[tree.Chain.Latest.WorkflowID] = tree.Executions
	}
	parents, err := s.resolveExecutionChains(ctx, chains, live, func(e Execution) bool {
		return isFleet(e) && !dismissed.has(KindFleet, e.WorkflowID)
	})
	if err != nil {
		return nil, err
	}
	if len(parents) > limit {
		parents = parents[:limit]
	}
	plans, err := s.plansFor(ctx, parents)
	if err != nil {
		return nil, err
	}

	fleets := make([]Fleet, 0, len(parents))
	for _, parent := range parents {
		fleet, err := s.buildFleet(ctx, parent, treesByID[parent.WorkflowID], live, plans[parent.WorkflowID])
		if err != nil {
			return nil, err
		}
		fleets = append(fleets, fleet)
	}
	return fleets, nil
}

// Fleet returns one fleet with its whole node graph, or ErrNotFound. It is the
// read behind a fleet's own view, and the one place the plan's edges are exposed.
func (s *Service) Fleet(ctx context.Context, id string) (Fleet, error) {
	if err := ValidateItemID(id); err != nil {
		return Fleet{}, err
	}
	// The collection adapter selects this identity first and then returns its whole
	// tree, so a large execution history cannot truncate the graph.
	trees, err := s.deps.Collections.FleetTrees(ctx, ChainQuery{WorkflowID: id, Limit: 1})
	if err != nil {
		return Fleet{}, unavailable("read the fleet's records", err)
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return Fleet{}, err
	}
	var tree []Execution
	var chains []ExecutionChain
	if len(trees) > 0 {
		tree = trees[0].Executions
		chains = append(chains, trees[0].Chain)
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

// Runs returns the standalone run satellites, newest first: every running chain
// plus every finished chain the operator has not dismissed.
//
// A chain that has continued as new any number of times is one satellite, keyed by
// its workflow ID and showing its latest iteration's status. A run fired by a
// schedule is not listed: its schedule represents it, so the overview shows one
// satellite for the schedule rather than one per firing. A fleet's node is not
// listed either: it belongs to its fleet.
func (s *Service) Runs(ctx context.Context, limit int) ([]Run, error) {
	limit = resolveLimit(limit)
	dismissed, err := s.dismissedIDs(ctx)
	if err != nil {
		return nil, err
	}
	recorded, err := s.deps.Collections.RunChains(ctx, ChainQuery{
		ExcludedWorkflowIDs: dismissed.ids(KindRun),
		Limit:               limit,
	})
	if err != nil {
		return nil, unavailable("read the recorded runs", err)
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	chains, err := s.resolveExecutionChains(ctx, recorded, live, func(e Execution) bool {
		return isRunSatellite(e) && !dismissed.has(KindRun, e.WorkflowID)
	})
	if err != nil {
		return nil, err
	}
	if len(chains) > limit {
		chains = chains[:limit]
	}

	runs := make([]Run, 0, len(chains))
	for _, chain := range chains {
		runs = append(runs, runFrom(chain))
	}
	return runs, nil
}

// Run returns one run chain, or ErrNotFound.
func (s *Service) Run(ctx context.Context, id string) (Run, error) {
	if err := ValidateItemID(id); err != nil {
		return Run{}, err
	}
	recorded, err := s.deps.Collections.RunChains(ctx, ChainQuery{WorkflowID: id, Limit: 1})
	if err != nil {
		return Run{}, unavailable("read the run's records", err)
	}
	live, err := s.liveByWorkflowID(ctx)
	if err != nil {
		return Run{}, err
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
	return runFrom(chains[0]), nil
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
	actions, err := s.deps.Collections.ScheduleActions(ctx, scheduleIDs, scheduleActionSample)
	if err != nil {
		return nil, unavailable("read the schedules' runs", err)
	}

	schedules := make([]Schedule, 0, len(states))
	for _, state := range states {
		// A schedule adapter may already know the running count. The live execution
		// count is the same fact from another source, so use the larger observation
		// rather than adding them and counting one action twice.
		runningActions := max(state.RunningActions, running[state.ID])
		schedules = append(schedules, scheduleFrom(state, actions[state.ID], runningActions))
		if len(schedules) == limit {
			break
		}
	}
	return schedules, nil
}

// scheduleFrom assembles one schedule satellite from its state, the runs it fired
// (newest first) and how many of its actions are in flight.
func scheduleFrom(state ScheduleState, fired []Execution, runningActions int) Schedule {
	schedule := Schedule{
		ID:             state.ID,
		Spec:           state.Spec,
		Paused:         state.Paused,
		RunningActions: runningActions,
		LastRunAt:      state.LastRunAt,
		NextRunAt:      state.NextRunAt,
	}
	outcome := state.LastOutcome
	for _, run := range fired {
		if schedule.Label == "" {
			schedule.Label = run.Label
		}
		if schedule.LastRunAt.IsZero() {
			schedule.LastRunAt = run.StartedAt
		}
		// The status describes the most recent *completed* action, so an in-flight
		// firing is skipped here; that it is running is said by runningActions.
		if outcome == "" && !run.Running() {
			outcome = run.Outcome
		}
	}
	schedule.Status = ScheduleStatus(state.Paused, runningActions, outcome)
	return schedule
}

// Dismissals returns every dismissal in force, newest first. It is what lets a
// consumer show — and undo — what has been hidden.
func (s *Service) Dismissals(ctx context.Context) ([]Dismissal, error) {
	dismissals, err := s.deps.Dismissals.Dismissals(ctx)
	if err != nil {
		return nil, unavailable("read the dismissals", err)
	}
	sort.SliceStable(dismissals, func(i, j int) bool {
		return dismissals[i].DismissedAt.After(dismissals[j].DismissedAt)
	})
	return dismissals, nil
}

// Dismiss hides a finished item from the overview and returns the dismissal it
// recorded. It refuses an unknown item (ErrNotFound) and an item that is still
// active (ErrNotDismissible): the rule that only finished work can be hidden is
// the server's to enforce, not the client's to remember.
//
// Dismissing an already-dismissed item succeeds and reports the dismissal, so a
// client that retries a lost response is not punished for it.
func (s *Service) Dismiss(ctx context.Context, kind ItemKind, itemID string) (Dismissal, error) {
	if err := ValidateDismissalTarget(kind, itemID); err != nil {
		return Dismissal{}, err
	}
	dismissible, err := s.dismissible(ctx, kind, itemID)
	if err != nil {
		return Dismissal{}, err
	}
	if !dismissible {
		return Dismissal{}, ErrNotDismissible
	}
	dismissal := Dismissal{Kind: kind, ItemID: itemID, DismissedAt: s.deps.Now().UTC()}
	stored, err := s.deps.Dismissals.Dismiss(ctx, dismissal)
	if err != nil {
		return Dismissal{}, unavailable("record the dismissal", err)
	}
	return stored, nil
}

// Undismiss brings a dismissed item back, and reports ErrNotFound when it was not
// dismissed.
func (s *Service) Undismiss(ctx context.Context, kind ItemKind, itemID string) error {
	if err := ValidateDismissalTarget(kind, itemID); err != nil {
		return err
	}
	err := s.deps.Dismissals.Undismiss(ctx, kind, itemID)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case err != nil:
		return unavailable("remove the dismissal", err)
	}
	return nil
}

// dismissible reports whether the item exists and has finished. A dismissed item
// is still dismissible: the check reads the item's own status, which dismissal
// does not change.
func (s *Service) dismissible(ctx context.Context, kind ItemKind, itemID string) (bool, error) {
	switch kind {
	case KindFleet:
		fleet, err := s.Fleet(ctx, itemID)
		if err != nil {
			return false, err
		}
		return fleet.Dismissible(), nil
	case KindRun:
		run, err := s.Run(ctx, itemID)
		if err != nil {
			return false, err
		}
		return run.Dismissible(), nil
	default:
		// ValidateDismissalTarget has already refused every other kind.
		return false, ErrNotDismissible
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
	if tree == nil {
		var err error
		tree, err = s.deps.Records.RecordedExecutions(ctx, RecordQuery{WorkflowID: parent.WorkflowID, Limit: recordScanLimit})
		if err != nil {
			return Fleet{}, unavailable("read the fleet's nodes", err)
		}
	}

	fleet := Fleet{
		ID:        parent.WorkflowID,
		Goal:      parent.Label,
		PlanID:    parent.PlanID,
		StartedAt: parent.StartedAt,
		EndedAt:   parent.EndedAt,
	}

	if len(plan.Nodes) == 0 && plan.Goal == "" {
		fleet.Status = parent.Outcome.WorkStatus()
		return fleet, nil
	}
	if plan.Goal != "" {
		fleet.Goal = plan.Goal
	}

	executions, outcomes, err := s.nodeExecutions(ctx, parent, tree, live)
	if err != nil {
		return Fleet{}, err
	}
	statuses := DeriveNodeStatuses(plan, outcomes)
	fleet.Nodes = make([]FleetNode, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		fleet.Nodes = append(fleet.Nodes, FleetNode{
			ID:        n.ID,
			Prompt:    n.Prompt,
			DependsOn: n.DependsOn,
			Status:    statuses[n.ID],
			Execution: executions[n.ID],
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
// workflow-ID convention, and works out each node's outcome.
//
// A node's own child execution is the primary source. The fleet parent's recorded
// per-node breakdown is layered on top for settled nodes, because it is the only
// source for the two outcomes a child execution cannot express: a node that was
// skipped (it has no execution at all) and a node that stopped in a recoverable
// way and needs a human. A node whose child is running keeps "running": a
// breakdown entry then belongs to an earlier attempt.
func (s *Service) nodeExecutions(ctx context.Context, parent resolvedChain, tree []Execution, live map[string]Execution) (map[string]*NodeExecution, map[string]ExecutionOutcome, error) {
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
	for nodeID, execs := range byNode {
		nodeWorkflowID := wfid.FleetNodeWorkflowID(parent.WorkflowID, nodeID)
		chains, err := s.resolveChains(ctx, execs, live, func(e Execution) bool {
			return e.WorkflowID == nodeWorkflowID
		})
		if err != nil {
			return nil, nil, err
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
	}
	for _, recorded := range parent.NodeOutcomes {
		if outcomes[recorded.NodeID] == OutcomeRunning {
			continue
		}
		outcomes[recorded.NodeID] = recorded.Outcome
	}
	return executions, outcomes, nil
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
	if len(newest.NodeOutcomes) == 0 {
		newest.NodeOutcomes = older.NodeOutcomes
	}
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
func runFrom(chain resolvedChain) Run {
	return Run{
		ID:         chain.WorkflowID,
		Type:       runType(chain.Class),
		Label:      chain.Label,
		Status:     chain.Outcome.WorkStatus(),
		StartedAt:  chain.StartedAt,
		EndedAt:    chain.EndedAt,
		Iterations: max(chain.Iterations, 1),
		Tokens:     chain.Tokens,
	}
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
// A child execution belongs to whatever started it (a fleet's node to its fleet, a
// review to its develop run) and a schedule-fired run is represented by its
// schedule, so neither is a satellite: listing them would show the same work
// twice.
func isRunSatellite(e Execution) bool {
	if e.ParentWorkflowID != "" || e.ScheduleID != "" {
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
	running, err := s.deps.Live.RunningExecutions(ctx, liveLimit)
	if err != nil {
		return nil, unavailable("list the running executions", err)
	}
	live := make(map[string]Execution, len(running))
	for _, e := range running {
		live[e.WorkflowID] = e
	}
	return live, nil
}

// dismissalSet is the operator's dismissals indexed for lookup.
type dismissalSet map[string]struct{}

// has reports whether the item is dismissed.
func (d dismissalSet) has(kind ItemKind, itemID string) bool {
	_, ok := d[Dismissal{Kind: kind, ItemID: itemID}.ID()]
	return ok
}

// ids returns the dismissed item IDs of one kind for adapter-side exclusion.
func (d dismissalSet) ids(kind ItemKind) []string {
	prefix := string(kind) + ":"
	ids := make([]string, 0)
	for id := range d {
		if len(id) > len(prefix) && id[:len(prefix)] == prefix {
			ids = append(ids, id[len(prefix):])
		}
	}
	sort.Strings(ids)
	return ids
}

// dismissedIDs reads the dismissals. A failure is reported rather than ignored: a
// silently empty set would put every dismissed item back on the overview, which
// looks exactly like work reappearing.
func (s *Service) dismissedIDs(ctx context.Context) (dismissalSet, error) {
	dismissals, err := s.deps.Dismissals.Dismissals(ctx)
	if err != nil {
		return nil, unavailable("read the dismissals", err)
	}
	set := make(dismissalSet, len(dismissals))
	for _, d := range dismissals {
		set[d.ID()] = struct{}{}
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
