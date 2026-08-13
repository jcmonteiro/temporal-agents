package hubrecords

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
	"temporal-agents/internal/agenthub/hubpg"
	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execpg"
	"temporal-agents/internal/pgtest"
)

// The whole recorded path against a real Postgres: a workflow records where it ran,
// the row goes through the jsonb detail and the chain aggregation, and what comes
// out the other side is the registry a response publishes. The in-memory tests in
// hubrecords_test.go cover the translation; only this one covers the store.
//
// The database is brought up by pgtest (testcontainers-go), so it needs a Docker
// daemon and no setup. It fails rather than skips without one: a suite that skips
// itself reports green while exercising none of the SQL it exists for.

func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }

var recordedAt = time.Date(2026, time.August, 6, 9, 30, 0, 0, time.UTC)

func TestRecordedRunsProjectIntoARegistryClosedUnderAncestry(t *testing.T) {
	ctx := context.Background()
	store, err := execpg.Open(ctx, pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	require.NoError(t, store.Migrate(ctx))

	// Two nodes developing in worktrees of one repository, a run in the repository
	// itself, and a run whose probe established nothing.
	for _, execution := range []execstore.Execution{{
		WorkflowID: "develop-api", RunID: "api-1", Kind: execstore.KindDevelop,
		StartedAt: recordedAt, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Directory: "/srv/worktrees/api", Repository: "/srv/repos/pricing"},
	}, {
		WorkflowID: "develop-web", RunID: "web-1", Kind: execstore.KindDevelop,
		StartedAt: recordedAt, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Directory: "/srv/worktrees/web", Repository: "/srv/repos/pricing"},
	}, {
		WorkflowID: "run-sweep", RunID: "sweep-1", Kind: execstore.KindRun,
		StartedAt: recordedAt, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Directory: "/srv/repos/pricing"},
	}, {
		WorkflowID: "run-elsewhere", RunID: "elsewhere-1", Kind: execstore.KindRun,
		StartedAt: recordedAt, Status: execstore.StatusSucceeded,
	}} {
		require.NoError(t, store.SaveExecution(ctx, execution))
	}
	records, err := New(store, store)
	require.NoError(t, err)

	chains, err := records.RunChains(ctx, agenthub.ChainQuery{})
	require.NoError(t, err)

	require.Len(t, chains, 4)
	locations := make([]agenthub.Location, 0, len(chains))
	byWorkflow := map[string]agenthub.Location{}
	for _, chain := range chains {
		location, err := chain.Latest.Place.Location()
		require.NoError(t, err)
		locations = append(locations, location)
		byWorkflow[chain.Latest.WorkflowID] = location
	}
	registry := agenthub.NewLocationRegistry(locations...)

	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	require.True(t, registry.Contains(repository.ID()),
		"the repository the worktrees hang under must be published, though no run is in it")
	require.Equal(t, 4, registry.Len(), "unknown, the repository and the two worktrees")
	require.Equal(t, repository.ID(), byWorkflow["run-sweep"].ID(),
		"a run in the repository and a worktree's parent are the same place")
	require.Equal(t, agenthub.LocationUnknown, byWorkflow["run-elsewhere"].Kind())
	for _, location := range registry.Locations() {
		parent, hasParent := location.Parent()
		if !hasParent {
			continue
		}
		require.True(t, registry.Contains(parent.ID()),
			"%q hangs under a place the registry does not publish", location.ID())
	}
}

func TestMoreThanOneThousandDismissalsDoNotHideOlderVisibleWork(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.NewDatabase(t)
	executions, err := execpg.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(executions.Close)
	require.NoError(t, executions.Migrate(ctx))
	dismissals, err := hubpg.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(dismissals.Close)
	require.NoError(t, dismissals.Migrate(ctx))

	const dismissedCount = 1000
	for i := 0; i < dismissedCount; i++ {
		workflowID := fmt.Sprintf("run-dismissed-%04d", i)
		startedAt := recordedAt.Add(-time.Duration(i) * time.Second)
		endedAt := startedAt.Add(time.Second)
		require.NoError(t, executions.SaveExecution(ctx, execstore.Execution{
			WorkflowID: workflowID, RunID: workflowID + "-iteration", Kind: execstore.KindRun,
			Prompt: "already reviewed", StartedAt: startedAt, EndedAt: endedAt,
			Status: execstore.StatusSucceeded,
		}))
		run := agenthub.Run{
			ID: workflowID, Type: agenthub.RunTypePrompt, Label: "already reviewed",
			Status: agenthub.StatusDone, StartedAt: startedAt, EndedAt: endedAt, Iterations: 1,
		}
		_, err := dismissals.Dismiss(ctx, agenthub.Dismissal{
			Viewer: agenthub.LocalViewerID, Kind: agenthub.KindRun, ItemID: workflowID,
			StateRevision: run.StateRevision(), DismissedAt: recordedAt,
		})
		require.NoError(t, err)
	}
	visibleID := "run-visible"
	require.NoError(t, executions.SaveExecution(ctx, execstore.Execution{
		WorkflowID: visibleID, RunID: visibleID + "-iteration", Kind: execstore.KindRun,
		Prompt: "still visible", StartedAt: recordedAt.Add(-2 * time.Hour),
		EndedAt: recordedAt.Add(-2*time.Hour + time.Second), Status: execstore.StatusSucceeded,
	}))

	records, err := New(executions, executions)
	require.NoError(t, err)
	otherPorts := agenthubtest.New()
	deps := otherPorts.Dependencies(recordedAt)
	deps.Collections = records
	deps.Dismissals = dismissals
	service, err := agenthub.NewService(deps)
	require.NoError(t, err)

	runs, err := service.Runs(ctx, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, visibleID, runs[0].ID)
}

func TestAnIterationThatRecordedNoPlaceDoesNotEraseTheChainsPlace(t *testing.T) {
	// A chain loops through continue-as-new, and the iteration in flight when the
	// probe was switched on records no place. The chain is one satellite in one
	// place, so the aggregation must keep the fact an earlier row established.
	ctx := context.Background()
	store, err := execpg.Open(ctx, pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	require.NoError(t, store.Migrate(ctx))

	require.NoError(t, store.SaveExecution(ctx, execstore.Execution{
		WorkflowID: "run-chain", RunID: "iteration-1", Kind: execstore.KindRun,
		StartedAt: recordedAt, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Directory: "/srv/worktrees/api", Repository: "/srv/repos/pricing"},
	}))
	require.NoError(t, store.SaveExecution(ctx, execstore.Execution{
		WorkflowID: "run-chain", RunID: "iteration-2", Kind: execstore.KindRun,
		StartedAt: recordedAt.Add(time.Minute), Status: execstore.StatusSucceeded,
	}))
	records, err := New(store, store)
	require.NoError(t, err)

	chains, err := records.RunChains(ctx, agenthub.ChainQuery{})
	require.NoError(t, err)

	require.Len(t, chains, 1)
	require.Equal(t, agenthub.RecordedPlace{
		Directory: "/srv/worktrees/api", Repository: "/srv/repos/pricing",
	}, chains[0].Latest.Place)
}
