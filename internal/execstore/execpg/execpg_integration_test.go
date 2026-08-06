package execpg

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// The adapter is tested against a real Postgres: the database and its schema are
// an out-of-process dependency this project owns, so a mock would exercise none
// of what can actually go wrong here (the upsert's conflict handling, the jsonb
// round-trip, the filter SQL).
//
// Set TEST_DATABASE_URL to a throwaway database to run these; the compose
// Postgres is fine (see `make test-integration`). The suite truncates the tables
// it uses, so it refuses any database whose name does not end in the suffix below
// — a destructive suite must not be able to reach real history by mistake.
const testDatabaseURLEnv = "TEST_DATABASE_URL"

// testDatabaseSuffix is the name ending a database must have before this suite
// will truncate anything in it. It makes the safety rule mechanical rather than a
// warning in a comment: pointing the suite at the working database (whose name is
// plain "temporal_agents") fails the run instead of deleting the recorded history
// and the stored fleet plans.
const testDatabaseSuffix = "_test"

// newTestStore opens the test database, applies the schema, and empties the
// executions table so each test starts from a known state.
func newTestStore(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv(testDatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping the real-Postgres adapter suite", testDatabaseURLEnv)
	}
	requireThrowawayDatabase(t, dsn)
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	require.NoError(t, store.Migrate(ctx))
	_, err = store.pool.Exec(ctx, "TRUNCATE executions")
	require.NoError(t, err)
	return store
}

// requireThrowawayDatabase fails the test unless the DSN names a database whose
// name ends in testDatabaseSuffix. Only the database name is inspected, and the
// DSN itself is never reported: it commonly carries credentials.
func requireThrowawayDatabase(t *testing.T, dsn string) {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoErrorf(t, err, "%s is not a valid connection string", testDatabaseURLEnv)
	name := strings.TrimPrefix(u.Path, "/")
	require.Truef(t, strings.HasSuffix(name, testDatabaseSuffix),
		"%s points at database %q, which does not end in %q; this suite truncates the tables it uses, "+
			"so it only runs against a throwaway database (see 'make test-integration')",
		testDatabaseURLEnv, name, testDatabaseSuffix)
}

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

func TestOpen_RejectsAnEmptyDSN(t *testing.T) {
	// DATABASE_URL is required and has no default, so an empty value must fail
	// fast instead of connecting to something implicit.
	_, err := Open(context.Background(), "   ")

	require.Error(t, err)
}
