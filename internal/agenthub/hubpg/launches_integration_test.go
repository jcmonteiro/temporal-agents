package hubpg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/pgtest"
)

// What the hub started is what makes a repeated request one run, so the upsert that
// keeps the first launch — and the durability a hub that restarts mid-click relies
// on — are tested against a real Postgres.

func TestARequestThatStartedWorkIsRememberedWithItsProvenance(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	startedAt := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	_, err := store.Launch(ctx, agenthub.Launch{
		RequestID:  "request-1",
		WorkflowID: "develop-1",
		Kind:       agenthub.StartDevelop,
		Place:      agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		Prompt:     "make the flaky test pass",
		StartedAt:  startedAt,
		StartedBy:  "https://issuer.test|operator-1",
	})
	require.NoError(t, err)

	got, err := store.LaunchOf(ctx, "request-1")
	require.NoError(t, err)
	require.Equal(t, "develop-1", got.WorkflowID)
	require.Equal(t, agenthub.StartDevelop, got.Kind)
	require.Equal(t, "/srv/repos/pricing", got.Place.Directory)
	require.Equal(t, "make the flaky test pass", got.Prompt)
	require.True(t, got.StartedAt.Equal(startedAt))
	require.Equal(t, "https://issuer.test|operator-1", got.StartedBy)
}

func TestARequestThatStartedNothingIsReportedAsSuch(t *testing.T) {
	store := newTestStore(t)

	_, err := store.LaunchOf(context.Background(), "request-nothing")

	require.ErrorIs(t, err, agenthub.ErrNotFound,
		"a first attempt must be distinguishable from a repeat")
}

func TestRepeatingARequestKeepsTheRunItAlreadyStarted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	launch := agenthub.Launch{
		RequestID:  "request-1",
		WorkflowID: "develop-1",
		Kind:       agenthub.StartDevelop,
		Place:      agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		StartedAt:  first,
		StartedBy:  "operator-1",
	}
	_, err := store.Launch(ctx, launch)
	require.NoError(t, err)

	// The same request lands again — a retry that raced the first one home.
	repeat := launch
	repeat.WorkflowID = "develop-2"
	repeat.StartedAt = first.Add(time.Hour)
	repeat.StartedBy = "operator-2"
	stored, err := store.Launch(ctx, repeat)

	require.NoError(t, err)
	require.Equal(t, "develop-1", stored.WorkflowID,
		"a repeat must describe the run that is already there")
	require.True(t, stored.StartedAt.Equal(first))
	require.Equal(t, "operator-1", stored.StartedBy)
}

func TestALaunchIsThereForTheNextServer(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(ctx))
	_, err := first.Launch(ctx, agenthub.Launch{
		RequestID:  "request-1",
		WorkflowID: "develop-1",
		Kind:       agenthub.StartDevelop,
		Place:      agenthub.RecordedPlace{Directory: "/srv/repos/pricing"},
		StartedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)

	// The hub restarts while the operator's browser retries.
	second := openTestStore(t, dsn)
	require.NoError(t, second.Migrate(ctx))

	got, err := second.LaunchOf(ctx, "request-1")
	require.NoError(t, err)
	require.Equal(t, "develop-1", got.WorkflowID)
}
