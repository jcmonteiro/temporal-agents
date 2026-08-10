package agenthub_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
)

// Starting work is the first thing this API does that changes the world outside
// itself, so these tests are about the rules that protect it: the place is resolved
// by the server, one request is one run however often it is asked for, and work
// that would collide is refused by name.

// aRegisteredPlace gives the fake a directory it can inspect and registers it.
func aRegisteredPlace(t *testing.T, service *agenthub.Service, source *agenthubtest.Source, directory string) agenthub.Location {
	t.Helper()
	source.WithDirectory(directory, agenthub.RecordedPlace{Directory: directory})
	place, err := service.RegisterPlace(context.Background(), directory, "operator-1")
	require.NoError(t, err)
	return place.Location
}

func TestStartingADevelopPassSubmitsItInThePlaceTheServerResolved(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")

	started, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-1",
		Kind:      agenthub.StartDevelop,
		PlaceID:   place.ID(),
		Prompt:    "make the flaky test pass",
		StartedBy: "operator-1",
	})

	require.NoError(t, err)
	require.Equal(t, place.ID(), started.Location.ID())
	require.Equal(t, now, started.StartedAt)
	require.Equal(t, "operator-1", started.StartedBy, "a run must record who started it")

	submitted := source.Started()
	require.Len(t, submitted, 1)
	require.Equal(t, agenthub.StartDevelop, submitted[0].Kind)
	require.Equal(t, "/srv/repos/pricing", submitted[0].Directory,
		"the directory is the server's answer, never the caller's")
	require.Equal(t, "make the flaky test pass", submitted[0].Prompt)
	require.Equal(t, started.RunID, submitted[0].WorkflowID,
		"the run an operator is given must be the execution that was submitted")
}

func TestRepeatingOneRequestStartsOneRun(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	request := agenthub.StartRequest{
		RequestID: "request-1",
		Kind:      agenthub.StartDevelop,
		PlaceID:   place.ID(),
		Prompt:    "make the flaky test pass",
		StartedBy: "operator-1",
	}

	first, err := service.StartWork(context.Background(), request)
	require.NoError(t, err)
	// The same request again: an impatient second click, a retried fetch, a reload.
	again, err := service.StartWork(context.Background(), request)

	require.NoError(t, err)
	require.Equal(t, first, again, "a repeat must describe the run it already started")
	require.Len(t, source.Started(), 1, "a repeat must not submit a second execution")
}

func TestARepeatIsNotRefusedForCollidingWithTheRunItStarted(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	request := agenthub.StartRequest{
		RequestID: "request-1",
		Kind:      agenthub.StartDevelop,
		PlaceID:   place.ID(),
		Prompt:    "make the flaky test pass",
	}
	first, err := service.StartWork(context.Background(), request)
	require.NoError(t, err)

	// The work is now running in that place, which is exactly what a conflict looks
	// like from the outside.
	running := agenthubtest.Run(first.RunID, "make the flaky test pass",
		agenthub.OutcomeRunning, ago(time.Minute))
	running.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	source.WithRecorded(running).WithRunning(running)

	again, err := service.StartWork(context.Background(), request)

	require.NoError(t, err)
	require.Equal(t, first.RunID, again.RunID)
}

func TestASecondLoopInOneWorkingTreeIsRefusedByName(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	running := agenthubtest.Run("develop-"+uuidLike("80"), "the first pass",
		agenthub.OutcomeRunning, ago(time.Minute))
	running.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	source.WithRecorded(running).WithRunning(running)

	_, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-2",
		Kind:      agenthub.StartReview,
		PlaceID:   place.ID(),
	})

	require.ErrorIs(t, err, agenthub.ErrPlaceIsBusy)
	require.Contains(t, err.Error(), running.WorkflowID,
		"a refusal must name what it collides with")
	require.Empty(t, source.Started(), "nothing may be submitted into a busy working tree")
}

func TestWorkInAWorktreeDoesNotMakeItsRepositoryBusy(t *testing.T) {
	// A linked worktree is a working tree of its own: a loop in it commits nowhere
	// near the repository's own checkout.
	source := agenthubtest.New()
	service := newService(t, source)
	repository := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	running := agenthubtest.Run("develop-"+uuidLike("81"), "in the worktree",
		agenthub.OutcomeRunning, ago(time.Minute))
	running.Place = agenthub.RecordedPlace{
		Directory: "/srv/worktrees/pricing-fix", Repository: "/srv/repos/pricing",
	}
	source.WithRecorded(running).WithRunning(running)

	_, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-3",
		Kind:      agenthub.StartReview,
		PlaceID:   repository.ID(),
	})

	require.NoError(t, err)
}

func TestWorkThatHasFinishedInAPlaceDoesNotBlockTheNextRun(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	settled := agenthubtest.Run("develop-"+uuidLike("82"), "yesterday's pass",
		agenthub.OutcomeSucceeded, ago(time.Hour))
	settled.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	source.WithRecorded(settled)

	_, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-4",
		Kind:      agenthub.StartReview,
		PlaceID:   place.ID(),
	})

	require.NoError(t, err)
}

func TestWorkCanBeStartedInAPlaceTheHubHasOnlyWatchedWorkIn(t *testing.T) {
	// The place was never registered: it is known because a run recorded it. The hub
	// has demonstrably worked there, so refusing it would tell the operator that the
	// repository in front of them is unknown.
	source := agenthubtest.New()
	recorded := agenthubtest.Run("run-"+uuidLike("83"), "yesterday's work",
		agenthub.OutcomeSucceeded, ago(time.Hour))
	recorded.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	source.WithRecorded(recorded)
	service := newService(t, source)
	place, err := agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}.Location()
	require.NoError(t, err)

	started, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-5",
		Kind:      agenthub.StartReview,
		PlaceID:   place.ID(),
	})

	require.NoError(t, err)
	require.Equal(t, "/srv/repos/pricing", source.Started()[0].Directory)
	require.Equal(t, place.ID(), started.Location.ID())
}

func TestAStartTheHubCannotHonourIsRefusedAndStartsNothing(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	unknownPlace := agenthub.UnknownLocation()

	cases := map[string]agenthub.StartRequest{
		"no request identity": {
			Kind: agenthub.StartDevelop, PlaceID: place.ID(), Prompt: "do the thing",
		},
		"a kind this surface does not offer": {
			RequestID: "r", Kind: "fleet", PlaceID: place.ID(), Prompt: "do the thing",
		},
		"a develop pass with nothing to do": {
			RequestID: "r", Kind: agenthub.StartDevelop, PlaceID: place.ID(), Prompt: "   ",
		},
		"a review told what to do": {
			RequestID: "r", Kind: agenthub.StartReview, PlaceID: place.ID(), Prompt: "do the thing",
		},
		"a prompt beyond the bound": {
			RequestID: "r", Kind: agenthub.StartDevelop, PlaceID: place.ID(),
			Prompt: strings.Repeat("x", 10001),
		},
		"no place": {
			RequestID: "r", Kind: agenthub.StartDevelop, Prompt: "do the thing",
		},
		"a place the hub does not know": {
			RequestID: "r", Kind: agenthub.StartDevelop, PlaceID: "invented", Prompt: "do the thing",
		},
		"a place that is not a working tree": {
			RequestID: "r", Kind: agenthub.StartDevelop, PlaceID: unknownPlace.ID(), Prompt: "do the thing",
		},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.StartWork(context.Background(), request)

			require.ErrorIs(t, err, agenthub.ErrInvalid)
			require.Empty(t, source.Started(), "a refused request must start nothing")
		})
	}
}

func TestAnUnreachableOrchestratorIsReportedAsSuchAndRecordsNothing(t *testing.T) {
	source := agenthubtest.New()
	service := newService(t, source)
	place := aRegisteredPlace(t, service, source, "/srv/repos/pricing")
	source.Fail(errors.New("the orchestrator is unreachable"))

	_, err := service.StartWork(context.Background(), agenthub.StartRequest{
		RequestID: "request-6",
		Kind:      agenthub.StartReview,
		PlaceID:   place.ID(),
	})

	require.ErrorIs(t, err, agenthub.ErrUnavailable,
		"a dependency that is down is not the operator's mistake")
}
