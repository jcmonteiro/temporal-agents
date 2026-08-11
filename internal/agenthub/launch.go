package agenthub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"temporal-agents/internal/wfid"
)

// Starting work is the first thing this API does that *changes* the world outside
// itself: it runs an agent on the operator's machine, in a working tree, where it
// branches and commits.
//
// Three rules therefore live here, in the core, and not in the transport that
// happens to offer them today:
//
//   - A request never carries a path. It names a place the hub knows, and the
//     directory is resolved from that place. The registry is the allowlist: a caller
//     that could name a directory could name any directory on the machine.
//   - The same request identity always names the same execution. A double click, a
//     retried fetch and a reload are normal, and each must end with one run.
//   - Work that would collide is refused, naming what it collides with. Two loops in
//     one working tree stash and commit over each other; that is corruption, not
//     impoliteness.

// ErrPlaceIsBusy is returned when work is already running in the place a start
// names. It is a conflict rather than an invalid request: nothing about the request
// is wrong, and the same request will succeed once the running work settles.
var ErrPlaceIsBusy = errors.New("work is already running in this place")

// PlaceIsBusy is the refusal itself: it reads as a sentence and carries the
// identity of the work in the way, so a consumer can lead an operator to it rather
// than parse it out of a message written for a person.
type PlaceIsBusy struct {
	// RunID is the work already running there.
	RunID string
	// Place is what to call the place, for the sentence.
	Place string
}

// Error implements error.
func (e PlaceIsBusy) Error() string {
	return fmt.Sprintf("%s: %s is already running in %s", ErrPlaceIsBusy, e.RunID, e.Place)
}

// Is makes the refusal match the sentinel, so a caller that only wants to know
// "was it busy?" does not have to know this type exists.
func (e PlaceIsBusy) Is(target error) bool { return target == ErrPlaceIsBusy }

// StartKind is what may be started. It is a closed set, and deliberately smaller
// than what the CLI offers: a fleet needs its plan approved and a schedule needs a
// recurrence, and neither is a decision this surface can carry.
type StartKind string

const (
	// StartDevelop is one develop pass: the agent is told what to do, and it
	// branches, implements and reviews until the loop converges.
	StartDevelop StartKind = "develop"
	// StartReview is one review pass over what is already in the working tree.
	StartReview StartKind = "review"
)

// StartKinds lists what may be started, in a stable order, for validation and for
// the published contract.
func StartKinds() []StartKind { return []StartKind{StartDevelop, StartReview} }

// Bounds on what a start request may carry.
const (
	// maxRequestIDLength bounds the caller's own identity for the request.
	maxRequestIDLength = 200
	// maxPromptLength bounds what the agent is told to do. It is generous — a good
	// instruction is a paragraph, not a word — and finite, because an unbounded
	// prompt is an unbounded workflow input.
	maxPromptLength = 10000
)

// StartRequest is what a caller asks for: which work, where, and under whose
// request identity. There is deliberately no directory field anywhere in it.
type StartRequest struct {
	// RequestID is the caller's own identity for this request. It is what makes the
	// request repeatable: the same identity always names the same execution.
	RequestID string
	// Kind is what to start.
	Kind StartKind
	// PlaceID references the place to work in, as published in a location registry.
	PlaceID string
	// Prompt is what the agent is told to do. It is required for a develop pass and
	// refused for a review, which reviews what is already there.
	Prompt string
	// Worktree asks development to run in a fresh server-managed worktree.
	Worktree bool
	// StartedBy identifies the principal asking, empty where nobody is
	// authenticated. It is recorded for audit only.
	StartedBy string
}

// StartSpec is what the orchestrator is asked to run: the work, resolved. The
// directory appears here for the first time, because this is the first value that
// is no longer anybody's request.
type StartSpec struct {
	// WorkflowID is the execution's identity, already minted from the request
	// identity.
	WorkflowID string
	// Kind is what to run.
	Kind StartKind
	// Directory is the working tree to run in, resolved from the place.
	Directory string
	// Prompt is what the agent is told to do, empty for a review.
	Prompt string
	// Worktree selects a fresh server-managed worktree for development.
	Worktree bool
	// StartedBy is the initiating principal, used only to address human checkpoints.
	StartedBy string
}

// StartedWork is one started run, as the core reports it.
//
// It is not the run read model: a start returns before the orchestrator's state is
// observable, so reading the run back here would answer "no such run" for work that
// is starting perfectly well. What it carries is what the server knows for certain,
// which is enough to address the run and to show it.
type StartedWork struct {
	// RunID is the run's identity in the read model — the workflow ID of the chain.
	RunID string
	// Kind is what was started.
	Kind StartKind
	// Location is where it runs.
	Location Location
	// Prompt is what the agent was told to do.
	Prompt string
	// StartedAt is when the work was first started under this request identity. A
	// repeat does not move it.
	StartedAt time.Time
	// StartedBy is the principal who started it.
	StartedBy string
}

// LaunchStore is the driven port that remembers what was started from the hub: the
// request identity, the execution it named, and who asked.
//
// It is what makes a repeat honest without asking the orchestrator to keep a
// history of requests, and it is where attribution lives: the workflow's input is
// what the work needs to run, and stuffing an operator's identity into it would put
// an audit fact into a replayable execution history for no benefit to the work.
type LaunchStore interface {
	// Launch records a start and returns the stored record. It must be idempotent on
	// the request identity, returning the original record rather than overwriting it,
	// so a repeat describes the run that is already there.
	Launch(ctx context.Context, launch Launch) (Launch, error)
	// LaunchOf returns the launch one request identity produced, or ErrNotFound.
	LaunchOf(ctx context.Context, requestID string) (Launch, error)
	// LaunchOfRun returns the launch that started one execution, or ErrNotFound for
	// work the hub did not start. It is how a run answers "who started this" without
	// the answer having to travel in the workflow's own input.
	LaunchOfRun(ctx context.Context, workflowID string) (Launch, error)
}

// Launch is one recorded start.
type Launch struct {
	// RequestID is the caller's identity for the request.
	RequestID string
	// WorkflowID is the execution it started.
	WorkflowID string
	// Kind is what was started.
	Kind StartKind
	// Place is where it runs, as the recorded fact the location is derived from.
	Place RecordedPlace
	// Prompt is what the agent was told to do.
	Prompt string
	// StartedAt is when it was started.
	StartedAt time.Time
	// StartedBy is the principal who started it.
	StartedBy string
}

// Launcher is the driven port that submits work to the orchestrator. The core says
// what to run and where; how a workflow is submitted stays at the edge, and no
// workflow logic is duplicated on this side of it.
type Launcher interface {
	// Start submits the work described by spec. It must be idempotent on the
	// specified workflow ID: an execution that is already running under it is the
	// work, not a failure.
	Start(ctx context.Context, spec StartSpec) error
}

// ValidateStartRequest checks what a caller asked for, before anything is
// resolved, started or recorded.
func ValidateStartRequest(request StartRequest) error {
	if err := validateRequestID(request.RequestID); err != nil {
		return err
	}
	switch request.Kind {
	case StartDevelop:
		if strings.TrimSpace(request.Prompt) == "" {
			return fmt.Errorf("%w: a develop pass needs a prompt saying what to do", ErrInvalid)
		}
	case StartReview:
		if request.Prompt != "" {
			return fmt.Errorf("%w: a review pass takes no prompt: it reviews what is already there", ErrInvalid)
		}
		if request.Worktree {
			return fmt.Errorf("%w: a review pass runs in the selected working tree", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: %q cannot be started here, only %v", ErrInvalid, request.Kind, StartKinds())
	}
	if utf8.RuneCountInString(request.Prompt) > maxPromptLength {
		return fmt.Errorf("%w: the prompt must be at most %d characters", ErrInvalid, maxPromptLength)
	}
	if !utf8.ValidString(request.Prompt) {
		return fmt.Errorf("%w: the prompt is not valid text", ErrInvalid)
	}
	if request.PlaceID == "" {
		return fmt.Errorf("%w: the place to work in is required", ErrInvalid)
	}
	return nil
}

// validateRequestID checks the caller's identity for the request.
func validateRequestID(requestID string) error {
	switch {
	case requestID == "":
		return fmt.Errorf("%w: a request id is required, so a repeat of this request is one run", ErrInvalid)
	case utf8.RuneCountInString(requestID) > maxRequestIDLength:
		return fmt.Errorf("%w: the request id must be at most %d characters", ErrInvalid, maxRequestIDLength)
	case strings.TrimSpace(requestID) != requestID:
		return fmt.Errorf("%w: the request id must not be padded with whitespace", ErrInvalid)
	case strings.IndexFunc(requestID, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: the request id contains control characters", ErrInvalid)
	default:
		return nil
	}
}

// StartWork starts one unit of agent work in a place the hub knows.
//
// The order is the order the rules must be applied in. The request is checked
// first, because a malformed one must not reach the machine. A request identity
// that has already started something answers with that work, before anything else
// is looked at: a repeat is not a new start and must not be judged as one — in
// particular it must not be refused for conflicting with the very run it started.
// Only then is the place resolved, the place checked for work already running in
// it, and the execution submitted.
func (s *Service) StartWork(ctx context.Context, request StartRequest) (StartedWork, error) {
	if err := ValidateStartRequest(request); err != nil {
		return StartedWork{}, err
	}
	if started, repeated, err := s.alreadyStarted(ctx, request.RequestID); err != nil || repeated {
		return started, err
	}
	known, err := s.knownWork(ctx)
	if err != nil {
		return StartedWork{}, err
	}
	place, err := s.placeToWorkIn(ctx, request.PlaceID, known)
	if err != nil {
		return StartedWork{}, err
	}
	directory, hasDirectory := place.Directory()
	if !hasDirectory {
		return StartedWork{}, fmt.Errorf(
			"%w: %s is not a working tree, so nothing can be started in it", ErrInvalid, place.Label())
	}
	if conflict, busy := known.runningIn(place.ID()); busy {
		return StartedWork{}, PlaceIsBusy{RunID: conflict, Place: place.Label()}
	}
	workflowID, minted := wfid.ForRequest(classOf(request.Kind), request.RequestID)
	if !minted {
		// Every kind this service accepts has a convention; a kind that does not is a
		// defect here, not a request to fix.
		return StartedWork{}, fmt.Errorf("no workflow identity convention for %q", request.Kind)
	}
	spec := StartSpec{
		WorkflowID: workflowID,
		Kind:       request.Kind,
		Directory:  directory,
		Prompt:     request.Prompt,
		Worktree:   request.Worktree,
		StartedBy:  request.StartedBy,
	}
	if err := s.deps.Launcher.Start(ctx, spec); err != nil {
		return StartedWork{}, unavailable("start the work", err)
	}
	// The launch is recorded after the work is submitted, so nothing is ever
	// recorded as started that was not. A record that fails to be written costs the
	// caller its repeat — the work runs, and a retry of the same request is refused
	// as a conflict with it, which names the run either way.
	stored, err := s.deps.Launches.Launch(ctx, Launch{
		RequestID:  request.RequestID,
		WorkflowID: workflowID,
		Kind:       request.Kind,
		Place:      RecordedPlace{Directory: directory},
		Prompt:     request.Prompt,
		StartedAt:  s.deps.Now().UTC(),
		StartedBy:  request.StartedBy,
	})
	if err != nil {
		return StartedWork{}, unavailable("record the start", err)
	}
	return startedFrom(stored, place)
}

// alreadyStarted answers what a request identity has already started, if anything.
func (s *Service) alreadyStarted(ctx context.Context, requestID string) (StartedWork, bool, error) {
	launch, err := s.deps.Launches.LaunchOf(ctx, requestID)
	switch {
	case errors.Is(err, ErrNotFound):
		return StartedWork{}, false, nil
	case err != nil:
		return StartedWork{}, false, unavailable("read what this request started", err)
	}
	location, err := launch.Place.Location()
	if err != nil {
		return StartedWork{}, false, err
	}
	started, err := startedFrom(launch, location)
	return started, true, err
}

// startedFrom projects a recorded launch onto what the core reports.
func startedFrom(launch Launch, place Location) (StartedWork, error) {
	return StartedWork{
		RunID:     launch.WorkflowID,
		Kind:      launch.Kind,
		Location:  place,
		Prompt:    launch.Prompt,
		StartedAt: launch.StartedAt,
		StartedBy: launch.StartedBy,
	}, nil
}

// placeToWorkIn resolves a place reference to the place itself.
//
// A place the hub has registered is one it may work in. A place it has only
// observed — because work ran there before — is one it demonstrably has worked in,
// so refusing it would tell an operator that the repository they are looking at is
// unknown. Both are resolved; a reference that is neither is refused, so no
// directory the hub cannot name a source for is ever reached.
func (s *Service) placeToWorkIn(ctx context.Context, placeID string, known workInPlaces) (Location, error) {
	registered, err := s.RegisteredPlaces(ctx)
	if err != nil {
		return Location{}, err
	}
	for _, place := range registered {
		if place.Location.ID() == placeID {
			return place.Location, nil
		}
	}
	if place, observed := known.places[placeID]; observed {
		return place, nil
	}
	return Location{}, fmt.Errorf("%w: this hub knows no place %q to work in", ErrInvalid, placeID)
}

// workInPlaces is what the hub currently knows about work and where it runs: every
// place it has watched work in, and what is running in each right now.
//
// Both answers come from one read, because they are one question asked twice: the
// same items say which places exist and which of them are busy, and reading them
// twice would let a start be judged against two different moments.
type workInPlaces struct {
	// places are every observed place and its ancestors, by id.
	places map[string]Location
	// running names one running item per place, which is all a refusal needs.
	running map[string]string
}

// runningIn reports what is running in a place, if anything.
//
// The rule is per place, and deliberately the whole place rather than the kind of
// work: a develop pass and a review pass in one working tree collide with each
// other exactly as two develop passes do, because what they collide over is the
// tree and not each other's purpose. A worktree of a repository is a place of its
// own, so work in it does not make the repository busy.
func (w workInPlaces) runningIn(placeID string) (string, bool) {
	item, busy := w.running[placeID]
	return item, busy
}

// knownWork reads the work the hub knows about, once.
func (s *Service) knownWork(ctx context.Context) (workInPlaces, error) {
	known := workInPlaces{places: map[string]Location{}, running: map[string]string{}}
	runs, err := s.Runs(ctx, MaxLimit)
	if err != nil {
		return workInPlaces{}, err
	}
	for _, run := range runs {
		known.remember(run.Location, run.ID, run.Running)
	}
	fleets, err := s.Fleets(ctx, MaxLimit)
	if err != nil {
		return workInPlaces{}, err
	}
	for _, fleet := range fleets {
		known.remember(fleet.Location, fleet.ID, fleet.Running)
		for _, node := range fleet.Nodes {
			known.remember(node.Location, fleet.ID+" ("+node.ID+")", node.Status == StatusInProgress)
		}
	}
	schedules, err := s.Schedules(ctx, MaxLimit)
	if err != nil {
		return workInPlaces{}, err
	}
	for _, schedule := range schedules {
		// A schedule's own firings are represented by the schedule, so a schedule with
		// an action in flight is work running in that place under the schedule's name.
		known.remember(schedule.Location, schedule.ID, schedule.RunningActions > 0)
	}
	return known, nil
}

// remember indexes a place and every place above it, so a repository is known even
// when only a worktree of it has been worked in. Running work marks the place it
// runs in, and only that place: an ancestor is a different working tree.
func (w workInPlaces) remember(place Location, item string, running bool) {
	if running {
		if _, already := w.running[place.ID()]; !already {
			w.running[place.ID()] = item
		}
	}
	for current, ok := place, true; ok; current, ok = current.Parent() {
		w.places[current.ID()] = current
	}
}

// classOf maps what may be started onto the workflow-ID convention it is minted
// under, so a started run is classified by every reader exactly as the same work
// started from the command line.
func classOf(kind StartKind) wfid.Class {
	switch kind {
	case StartDevelop:
		return wfid.ClassDevelop
	case StartReview:
		return wfid.ClassReview
	default:
		return ""
	}
}

// startedBy reports who started one run from the hub, and empty for a run the hub
// did not start — one begun from the command line, or fired by a schedule.
//
// A launch store that cannot answer is not allowed to hide the run: attribution is
// provenance about the run, and a run reported without it is still the truth about
// the work, while no run at all is not.
func (s *Service) startedBy(ctx context.Context, workflowID string) string {
	launch, err := s.deps.Launches.LaunchOfRun(ctx, workflowID)
	if err != nil {
		return ""
	}
	return launch.StartedBy
}
