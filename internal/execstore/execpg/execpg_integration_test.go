package execpg

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// The behaviour of the adapter against a real Postgres. The container and the
// per-test database it runs on are set up in suite_test.go.

// stamp is a fixed, microsecond-precision instant: Postgres stores timestamps to
// the microsecond, so a nanosecond-precision value would not compare equal after
// a round-trip.
var stamp = time.Date(2026, time.August, 6, 9, 30, 0, 123456000, time.UTC)

func TestPostgres_MigrateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// The worker applies migrations on every start, so re-applying an up-to-date
	// schema must be a no-op rather than an error.
	require.NoError(t, store.Migrate(ctx))
	require.NoError(t, store.Migrate(ctx))
}

func TestPostgres_MigrateNeedsOnlyOneConnection(t *testing.T) {
	// Migrating pins one connection for the advisory lock and runs every migration on
	// that same connection. Taking a second one from the pool would deadlock a worker
	// whose DSN caps the pool at one — silently, at startup, with no message.
	dsn := newTestDatabase(t) + "&pool_max_conns=1"
	store := openTestStore(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	require.NoError(t, store.Migrate(ctx))
}

func TestPostgres_ConcurrentMigrateSucceedsForEveryWorker(t *testing.T) {
	// Every worker migrates at startup and treats a failure as fatal, so two workers
	// starting together must both succeed. Without the advisory lock in Migrate the
	// unguarded CREATE TABLE IF NOT EXISTS lets one of them lose on a catalog
	// duplicate-key error and refuse to start.
	dsn := newTestDatabase(t)
	const workers = 4
	stores := make([]*Postgres, 0, workers)
	for range workers {
		stores = append(stores, openTestStore(t, dsn))
	}

	errs := make(chan error, workers)
	var start sync.WaitGroup
	start.Add(1)
	for _, store := range stores {
		go func(store *Postgres) {
			start.Wait()
			errs <- store.Migrate(context.Background())
		}(store)
	}
	start.Done()
	for range workers {
		require.NoError(t, <-errs)
	}

	// Each migration was applied exactly once, so no worker skipped or repeated one.
	names, err := migrationNames()
	require.NoError(t, err)
	var applied int
	require.NoError(t, stores[0].pool.QueryRow(context.Background(),
		"SELECT count(*) FROM schema_migrations").Scan(&applied))
	require.Equal(t, len(names), applied)
}

func TestPostgres_RoundTripsAnExecutionIncludingItsDetail(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	converged := true
	want := execstore.Execution{
		WorkflowID:       "develop-1",
		RunID:            "run-1",
		Kind:             execstore.KindDevelop,
		Prompt:           "add a rate limiter",
		StartedAt:        stamp,
		EndedAt:          stamp.Add(time.Minute),
		Status:           execstore.StatusSucceeded,
		Tokens:           4321,
		ScheduleID:       "schedule-1",
		ParentWorkflowID: "fleet-1",
		Detail: execstore.Detail{
			Branch:    "feat/rate-limit",
			PRURL:     "https://github.com/o/r/pull/7",
			Converged: &converged,
			Nodes:     []execstore.NodeOutcome{{ID: "core", Status: "succeeded", Tokens: 12}},
			PlanID:    "plan-abcd1234",
			PlanNodes: 3,
			Error:     "",
		},
	}

	require.NoError(t, store.SaveExecution(ctx, want))
	got, err := store.ListExecutions(ctx, execstore.Filter{})

	require.NoError(t, err)
	require.Len(t, got, 1)
	got[0].StartedAt = got[0].StartedAt.UTC()
	got[0].EndedAt = got[0].EndedAt.UTC()
	require.Equal(t, want, got[0])
}

func TestPostgres_StillRunningExecutionHasNoEndTime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.SaveExecution(ctx, execstore.Execution{
		WorkflowID: "run-1", RunID: "run-1", Kind: execstore.KindRun,
		StartedAt: stamp, Status: execstore.StatusRunning,
	}))
	got, err := store.ListExecutions(ctx, execstore.Filter{})

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].Running())
	require.True(t, got[0].EndedAt.IsZero(), "an unset end time round-trips as NULL, not year zero")
	require.Empty(t, got[0].ScheduleID, "an absent schedule round-trips as empty, not NULL-as-error")
	require.Empty(t, got[0].ParentWorkflowID)
}

func TestPostgres_SaveExecutionUpsertsOnRunID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	start := execstore.Execution{
		WorkflowID: "run-1", RunID: "run-a", Kind: execstore.KindRun,
		Prompt: "summarize", StartedAt: stamp, Status: execstore.StatusRunning,
	}
	require.NoError(t, store.SaveExecution(ctx, start))

	// A retried start write must not duplicate the row.
	require.NoError(t, store.SaveExecution(ctx, start))

	terminal := start
	terminal.Status = execstore.StatusSucceeded
	terminal.EndedAt = stamp.Add(time.Minute)
	terminal.Tokens = 99
	require.NoError(t, store.SaveExecution(ctx, terminal))
	// A retried terminal write must not duplicate it either.
	require.NoError(t, store.SaveExecution(ctx, terminal))

	got, err := store.ListExecutions(ctx, execstore.Filter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "every write for one run ID lands in a single row")
	require.Equal(t, execstore.StatusSucceeded, got[0].Status)
	require.Equal(t, 99, got[0].Tokens)
}

func TestPostgres_ARetriedStartWriteCannotUnsettleARecord(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	start := execstore.Execution{
		WorkflowID: "run-1", RunID: "run-a", Kind: execstore.KindRun,
		Prompt: "summarize", StartedAt: stamp, Status: execstore.StatusRunning,
	}
	terminal := start
	terminal.Status = execstore.StatusSucceeded
	terminal.EndedAt = stamp.Add(time.Minute)
	terminal.Tokens = 99

	require.NoError(t, store.SaveExecution(ctx, start))
	require.NoError(t, store.SaveExecution(ctx, terminal))
	// Temporal may re-run an activity whose result was lost, so a start write can
	// land after the terminal one. It must not drag a settled record back to
	// running or discard its outcome.
	require.NoError(t, store.SaveExecution(ctx, start))

	got, err := store.ListExecutions(ctx, execstore.Filter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, execstore.StatusSucceeded, got[0].Status)
	require.Equal(t, 99, got[0].Tokens)
}

func TestPostgres_ChainedIterationsAreSeparateRowsUnderOneWorkflowID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for i, runID := range []string{"run-a", "run-b"} {
		require.NoError(t, store.SaveExecution(ctx, execstore.Execution{
			WorkflowID: "run-1", RunID: runID, Kind: execstore.KindRun,
			StartedAt: stamp.Add(time.Duration(i) * time.Minute),
			Status:    execstore.StatusSucceeded, Tokens: 100,
		}))
	}

	got, err := store.ListExecutions(ctx, execstore.Filter{WorkflowID: "run-1"})

	require.NoError(t, err)
	require.Len(t, got, 2, "a chained run's iterations are rows of their own")
	// Newest first.
	require.Equal(t, "run-b", got[0].RunID)
}

func TestPostgres_ListExecutionsFilters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	save := func(e execstore.Execution) {
		t.Helper()
		require.NoError(t, store.SaveExecution(ctx, e))
	}
	save(execstore.Execution{WorkflowID: "run-1", RunID: "r1", Kind: execstore.KindRun,
		StartedAt: stamp, Status: execstore.StatusSucceeded, ScheduleID: "schedule-9"})
	save(execstore.Execution{WorkflowID: "run-2", RunID: "r2", Kind: execstore.KindRun,
		StartedAt: stamp.Add(time.Minute), Status: execstore.StatusSucceeded})
	save(execstore.Execution{WorkflowID: "fleet-1", RunID: "f1", Kind: execstore.KindFleet,
		StartedAt: stamp.Add(2 * time.Minute), Status: execstore.StatusSucceeded})
	save(execstore.Execution{WorkflowID: "fleet-1-core", RunID: "f1c", Kind: execstore.KindDevelop,
		StartedAt: stamp.Add(3 * time.Minute), Status: execstore.StatusSucceeded, ParentWorkflowID: "fleet-1"})
	save(execstore.Execution{WorkflowID: "review-fleet-1-core", RunID: "f1cr", Kind: execstore.KindReview,
		StartedAt: stamp.Add(4 * time.Minute), Status: execstore.StatusSucceeded, ParentWorkflowID: "fleet-1-core"})

	t.Run("newest first, capped by limit", func(t *testing.T) {
		got, err := store.ListExecutions(ctx, execstore.Filter{Limit: 2})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, "f1cr", got[0].RunID)
		require.Equal(t, "f1c", got[1].RunID)
	})

	t.Run("by kind", func(t *testing.T) {
		got, err := store.ListExecutions(ctx, execstore.Filter{Kind: execstore.KindRun})
		require.NoError(t, err)
		require.Len(t, got, 2)
		for _, e := range got {
			require.Equal(t, execstore.KindRun, e.Kind)
		}
	})

	t.Run("by workflow ID, including its children", func(t *testing.T) {
		got, err := store.ListExecutions(ctx, execstore.Filter{WorkflowID: "fleet-1"})
		require.NoError(t, err)
		// The fleet parent and its node child; the node's own review child hangs off
		// the node, one level further down.
		require.Len(t, got, 2)
		require.Equal(t, []string{"f1c", "f1"}, []string{got[0].RunID, got[1].RunID})
	})

	t.Run("by schedule ID", func(t *testing.T) {
		got, err := store.ListExecutions(ctx, execstore.Filter{ScheduleID: "schedule-9"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, "r1", got[0].RunID)
	})

	t.Run("combined constraints narrow further", func(t *testing.T) {
		got, err := store.ListExecutions(ctx, execstore.Filter{Kind: execstore.KindReview, WorkflowID: "fleet-1"})
		require.NoError(t, err)
		require.Empty(t, got, "the review is a child of the node, not of the fleet")
	})
}

func TestPostgres_ReadBeforeAnyWorkerMigrated(t *testing.T) {
	// Only the worker applies migrations, so `history` can legitimately be the first
	// thing to touch a fresh database. That must read as "start the worker once"
	// rather than as Postgres's own "relation does not exist".
	store := newUnmigratedTestStore(t)

	_, err := store.ListExecutions(context.Background(), execstore.Filter{})

	require.ErrorIs(t, err, execstore.ErrNotMigrated)
}

func TestOpen_RejectsAnEmptyDSN(t *testing.T) {
	// DATABASE_URL is required and has no default, so an empty value must fail
	// fast instead of connecting to something implicit.
	_, err := Open(context.Background(), "   ")

	require.Error(t, err)
}
