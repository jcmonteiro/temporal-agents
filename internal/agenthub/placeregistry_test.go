package agenthub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
)

// Registering a place is how an operator says "the hub may work here" before any
// work has ever run there. These tests drive the service, because what matters is
// what comes back out of the registry — not which port was called.

func TestAPlaceWithNoWorkInItIsKnownOnceItIsRegistered(t *testing.T) {
	source := agenthubtest.New().
		WithDirectory("/srv/repos/pricing", agenthub.RecordedPlace{Directory: "/srv/repos/pricing"})
	service := newService(t, source)

	registered, err := service.RegisterPlace(context.Background(), "/srv/repos/pricing", "operator-1")

	require.NoError(t, err)
	require.Equal(t, "pricing", registered.Location.Label())
	require.Equal(t, now, registered.RegisteredAt)
	require.Equal(t, "operator-1", registered.RegisteredBy,
		"a place must record who let the hub work there")

	places, err := service.RegisteredPlaces(context.Background())
	require.NoError(t, err)
	require.Len(t, places, 1)
	require.Equal(t, registered.Location.ID(), places[0].Location.ID(),
		"a registered place must be known even though nothing has ever run in it")
}

func TestKnownPlacesIncludeAPlaceObservedFromRecordedWork(t *testing.T) {
	recorded := agenthubtest.Run("develop-observed", "earlier work", agenthub.OutcomeSucceeded, now.Add(-time.Hour))
	recorded.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	service := newService(t, agenthubtest.New().WithRecorded(recorded))

	places, err := service.KnownPlaces(context.Background())

	require.NoError(t, err)
	require.Len(t, places, 1)
	directory, hasDirectory := places[0].Location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/repos/pricing", directory)
	require.True(t, places[0].RegisteredAt.IsZero(), "an observed place must not claim it was registered")
}

func TestRegisteringTheSamePlaceAgainChangesNothing(t *testing.T) {
	source := agenthubtest.New().
		WithDirectory("/srv/repos/pricing", agenthub.RecordedPlace{Directory: "/srv/repos/pricing"})
	service := newService(t, source)
	first, err := service.RegisterPlace(context.Background(), "/srv/repos/pricing", "operator-1")
	require.NoError(t, err)

	// A retried request, a second click, or another operator doing the same thing.
	again, err := service.RegisterPlace(context.Background(), "/srv/repos/pricing", "operator-2")

	require.NoError(t, err)
	require.Equal(t, first, again, "a repeat registration must describe the place that is already there")
	places, err := service.RegisteredPlaces(context.Background())
	require.NoError(t, err)
	require.Len(t, places, 1)
}

func TestTheDirectoryTheProbeNamesIsTheOneRegistered(t *testing.T) {
	// An operator names a directory deep inside a checkout. The place is the working
	// tree that holds it, so naming it either way registers one place.
	source := agenthubtest.New().
		WithDirectory("/srv/repos/pricing/internal/api",
			agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}).
		WithDirectory("/srv/repos/pricing", agenthub.RecordedPlace{Directory: "/srv/repos/pricing"})
	service := newService(t, source)

	deep, err := service.RegisterPlace(context.Background(), "/srv/repos/pricing/internal/api", "operator-1")
	require.NoError(t, err)
	root, err := service.RegisterPlace(context.Background(), "/srv/repos/pricing", "operator-1")
	require.NoError(t, err)

	require.Equal(t, root.Location.ID(), deep.Location.ID())
	directory, hasDirectory := deep.Location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/repos/pricing", directory,
		"what is registered must be what the probe established, never what was typed")
	places, err := service.RegisteredPlaces(context.Background())
	require.NoError(t, err)
	require.Len(t, places, 1)
}

func TestARegistrationCannotStateAHierarchyTheProbeDoesNotSee(t *testing.T) {
	// A worktree hangs under the repository the probe names, and under nothing else:
	// the operator supplies a directory and has no way to assert a parent at all.
	source := agenthubtest.New().
		WithDirectory("/srv/worktrees/pricing-fix", agenthub.RecordedPlace{
			Directory:  "/srv/worktrees/pricing-fix",
			Repository: "/srv/repos/pricing",
		})
	service := newService(t, source)

	registered, err := service.RegisterPlace(context.Background(), "/srv/worktrees/pricing-fix", "operator-1")

	require.NoError(t, err)
	parent, hasParent := registered.Location.Parent()
	require.True(t, hasParent)
	require.Equal(t, "pricing", parent.Label())
}

func TestADirectoryThatCannotBeWorkedInIsRefusedForTheRightReason(t *testing.T) {
	source := agenthubtest.New().
		WithUnversionedDirectory("/srv/notes")
	service := newService(t, source)

	cases := map[string]struct {
		directory string
		want      error
	}{
		"nothing is there":            {directory: "/srv/gone", want: agenthub.ErrNoSuchDirectory},
		"no repository holds it":      {directory: "/srv/notes", want: agenthub.ErrNotARepository},
		"the path is relative":        {directory: "srv/repos/pricing", want: agenthub.ErrInvalid},
		"the path is not written out": {directory: "/srv/repos/../repos/pricing", want: agenthub.ErrInvalid},
		"the path is empty":           {directory: "", want: agenthub.ErrInvalid},
		"the path is padded":          {directory: " /srv/repos/pricing ", want: agenthub.ErrInvalid},
		"the path hides a newline":    {directory: "/srv/repos/pricing\n", want: agenthub.ErrInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.RegisterPlace(context.Background(), tc.directory, "operator-1")

			require.Error(t, err)
			require.True(t, errors.Is(err, tc.want), "error %v, want %v", err, tc.want)
			places, listErr := service.RegisteredPlaces(context.Background())
			require.NoError(t, listErr)
			require.Empty(t, places, "a refused registration must leave the registry alone")
		})
	}
}

func TestAnUnreachableRegistryIsReportedAsSuch(t *testing.T) {
	service := newService(t, agenthubtest.Failing(errors.New("the database is restarting")))

	_, listErr := service.RegisteredPlaces(context.Background())
	_, registerErr := service.RegisterPlace(context.Background(), "/srv/repos/pricing", "operator-1")

	require.ErrorIs(t, listErr, agenthub.ErrUnavailable)
	require.ErrorIs(t, registerErr, agenthub.ErrUnavailable,
		"a store that is down is not the operator's mistake")
}
