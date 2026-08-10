package hubpg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/pgtest"
)

// The place registry is the hub's memory of where it may work. It is tested against a
// real Postgres for the same reason the dismissals are: the upsert that makes a
// repeat registration a no-op, and the durability an operator relies on when the hub
// restarts, are properties of the database and of nothing a fake could stand in for.

func TestARegisteredPlaceSurvivesAndComesBackWithItsProvenance(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	registeredAt := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	_, err := store.Register(ctx, agenthub.PlaceRegistration{
		Place: agenthub.RecordedPlace{
			Directory:  "/srv/worktrees/pricing-fix",
			Repository: "/srv/repos/pricing",
		},
		RegisteredAt: registeredAt,
		RegisteredBy: "https://issuer.test|operator-1",
	})
	require.NoError(t, err)

	got, err := store.Registrations(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "/srv/worktrees/pricing-fix", got[0].Place.Directory)
	require.Equal(t, "/srv/repos/pricing", got[0].Place.Repository,
		"the repository the probe named must survive, or the place loses its parent")
	require.True(t, got[0].RegisteredAt.Equal(registeredAt))
	require.Equal(t, "https://issuer.test|operator-1", got[0].RegisteredBy)
}

func TestRegisteringAPlaceTwiceKeepsTheFirstRegistration(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	_, err := store.Register(ctx, agenthub.PlaceRegistration{
		Place:        agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		RegisteredAt: first,
		RegisteredBy: "operator-1",
	})
	require.NoError(t, err)
	stored, err := store.Register(ctx, agenthub.PlaceRegistration{
		Place:        agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		RegisteredAt: first.Add(time.Hour),
		RegisteredBy: "operator-2",
	})
	require.NoError(t, err)

	require.True(t, stored.RegisteredAt.Equal(first), "want %v, got %v", first, stored.RegisteredAt)
	require.Equal(t, "operator-1", stored.RegisteredBy,
		"a repeat must describe the registration that is already there")
	got, err := store.Registrations(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestARegisteredPlaceIsThereForTheNextServer(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(ctx))
	_, err := first.Register(ctx, agenthub.PlaceRegistration{
		Place:        agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		RegisteredAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	// The hub restarts: a new process, the same database.
	second := openTestStore(t, dsn)
	require.NoError(t, second.Migrate(ctx))

	got, err := second.Registrations(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "/srv/repos/pricing", got[0].Place.Directory)
}
