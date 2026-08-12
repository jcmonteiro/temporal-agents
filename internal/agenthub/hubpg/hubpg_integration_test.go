package hubpg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/pgtest"
)

// TestOpenRejectsAnEmptyDSN pins the fail-fast contract: the server must not start
// with a dismissal store it cannot reach, because the first thing an operator would
// learn about it is a click that silently did nothing.
func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	_, err := Open(context.Background(), "   ")
	require.Error(t, err)
}

// TestMigrateIsIdempotent pins that a restart is free: the server applies the schema
// at startup, so re-running it must do nothing.
func TestMigrateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.Migrate(context.Background()))
	require.NoError(t, store.Migrate(context.Background()))
}

// TestDismissalsRoundTripNewestFirst pins the read the whole overview depends on: a
// dismissal survives, comes back with its kind, item and time, and the listing is
// newest first.
func TestDismissalsRoundTripNewestFirst(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	older := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)

	dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", DismissedAt: older,
	})
	dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindFleet, ItemID: "fleet-1", DismissedAt: newer,
	})

	got, err := store.Dismissals(ctx, agenthub.LocalViewerID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "fleet:fleet-1", got[0].ID())
	require.Equal(t, "run:run-1", got[1].ID())
	require.True(t, got[0].DismissedAt.Equal(newer), "want %v, got %v", newer, got[0].DismissedAt)
}

// TestDismissIsIdempotentAndKeepsTheOriginalTime pins the write contract a client
// that retries a lost response depends on: the same item dismissed twice is one
// dismissal, and the moment it was first hidden is not rewritten.
func TestDismissIsIdempotentAndKeepsTheOriginalTime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	storedFirst := dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", DismissedAt: first,
	})
	storedSecond := dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", DismissedAt: first.Add(time.Hour),
	})
	require.Equal(t, storedFirst, storedSecond)

	got, err := store.Dismissals(ctx, agenthub.LocalViewerID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].DismissedAt.Equal(first), "the original time must stand, got %v", got[0].DismissedAt)
}

// TestDismissalsAreIsolatedByViewer pins that the same item can be acknowledged by
// two operators without either write changing the other's private view state.
func TestDismissalsAreIsolatedByViewer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC()

	dismiss(t, store, ctx, agenthub.Dismissal{
		Viewer: "issuer|alice", Kind: agenthub.KindRun, ItemID: "run-1",
		StateRevision: "state-1", DismissedAt: at,
	})
	dismiss(t, store, ctx, agenthub.Dismissal{
		Viewer: "issuer|bob", Kind: agenthub.KindRun, ItemID: "run-1",
		StateRevision: "state-1", DismissedAt: at,
	})

	alice, err := store.Dismissals(ctx, "issuer|alice")
	require.NoError(t, err)
	require.Len(t, alice, 1)
	require.Equal(t, agenthub.ViewerID("issuer|alice"), alice[0].Viewer)
	bob, err := store.Dismissals(ctx, "issuer|bob")
	require.NoError(t, err)
	require.Len(t, bob, 1)
	require.Equal(t, agenthub.ViewerID("issuer|bob"), bob[0].Viewer)
}

// TestTheSameIDUnderTwoKindsAreTwoDismissals pins that the kind is part of the
// identity: a fleet and a run that happen to share an id are different items, and
// dismissing one must not hide the other.
func TestTheSameIDUnderTwoKindsAreTwoDismissals(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC()

	dismiss(t, store, ctx, agenthub.Dismissal{Kind: agenthub.KindRun, ItemID: "x", DismissedAt: at})
	dismiss(t, store, ctx, agenthub.Dismissal{Kind: agenthub.KindFleet, ItemID: "x", DismissedAt: at})

	got, err := store.Dismissals(ctx, agenthub.LocalViewerID)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestDismissingAChangedStateReplacesTheAcknowledgement pins the state-sensitive
// upsert: an exact retry keeps its time, while reviewing a later state records a new
// revision and a new time.
func TestDismissingAChangedStateReplacesTheAcknowledgement(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", StateRevision: "state-1", DismissedAt: first,
	})
	stored := dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", StateRevision: "state-2", DismissedAt: second,
	})

	require.Equal(t, "state-2", stored.StateRevision)
	require.True(t, stored.DismissedAt.Equal(second))
}

// TestUndismissReportsWhetherItRemovedAnything pins the delete contract: undismissing
// an item that was not dismissed is reported as missing, so the transport can answer
// 404 instead of pretending it undid something.
func TestUndismissReportsWhetherItRemovedAnything(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.ErrorIs(t, store.Undismiss(ctx, agenthub.LocalViewerID, agenthub.KindRun, "run-1"), agenthub.ErrNotFound)

	dismiss(t, store, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", DismissedAt: time.Now().UTC(),
	})
	require.NoError(t, store.Undismiss(ctx, agenthub.LocalViewerID, agenthub.KindRun, "run-1"))

	got, err := store.Dismissals(ctx, agenthub.LocalViewerID)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestTwoServersShareOneSchema pins that a second process against the same database
// migrates and reads without a conflict, which is what the shared advisory lock and
// tracking table exist for.
func TestTwoServersShareOneSchema(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(ctx))
	second := openTestStore(t, dsn)
	require.NoError(t, second.Migrate(ctx))

	dismiss(t, first, ctx, agenthub.Dismissal{
		Kind: agenthub.KindRun, ItemID: "run-1", DismissedAt: time.Now().UTC(),
	})
	got, err := second.Dismissals(ctx, agenthub.LocalViewerID)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// dismiss records one dismissal and fails the test when the adapter cannot return
// the stored resource.
func dismiss(t *testing.T, store *Store, ctx context.Context, d agenthub.Dismissal) agenthub.Dismissal {
	t.Helper()
	if d.Viewer == "" {
		d.Viewer = agenthub.LocalViewerID
	}
	if d.StateRevision == "" {
		d.StateRevision = "reviewed-state"
	}
	stored, err := store.Dismiss(ctx, d)
	require.NoError(t, err)
	return stored
}
