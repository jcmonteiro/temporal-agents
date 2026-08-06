package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
)

// The persist activity is the one place where PromptWorkflow's own state type is
// mapped onto the shared execution record, so it is tested directly against a
// fake at the port: the assertion is the record that reached the store, not which
// method was called.

func TestPersistRunWorkflowState_MapsARunOntoTheSharedRecord(t *testing.T) {
	store := execstoretest.New()
	a := &Activities{Store: store}
	started := time.Date(2026, time.August, 6, 9, 30, 0, 0, time.UTC)

	err := a.PersistRunWorkflowState(context.Background(), RunState{
		WorkflowID:       "run-1",
		RunID:            "run-1-a",
		ParentWorkflowID: "fleet-1",
		Prompt:           "summarize the README",
		ScheduleID:       "schedule-9",
		StartedAt:        started,
		EndedAt:          started.Add(time.Minute),
		Status:           execstore.StatusFailed,
		Tokens:           1200,
		Error:            "pi crashed",
	})

	require.NoError(t, err)
	got := store.Last(t)
	// A run is recorded as KindRun; a schedule is not a kind of its own, so a fired
	// run stays a run that carries its schedule ID.
	require.Equal(t, execstore.KindRun, got.Kind)
	require.Equal(t, "schedule-9", got.ScheduleID)
	require.Equal(t, "run-1", got.WorkflowID)
	require.Equal(t, "run-1-a", got.RunID)
	require.Equal(t, "fleet-1", got.ParentWorkflowID)
	require.Equal(t, "summarize the README", got.Prompt)
	require.Equal(t, started, got.StartedAt)
	require.Equal(t, started.Add(time.Minute), got.EndedAt)
	require.Equal(t, execstore.StatusFailed, got.Status)
	require.Equal(t, 1200, got.Tokens)
	// The failure text is the only detail a run produces, so the record says why it
	// failed rather than merely that it did.
	require.Equal(t, execstore.Detail{Error: "pi crashed"}, got.Detail)
}

func TestPersistRunWorkflowState_WithoutAStoreFailsInsteadOfPanicking(t *testing.T) {
	// Recording is a hard dependency, so a worker started without the port wired in
	// must turn into a clear activity failure rather than a nil-pointer panic.
	var a Activities

	err := a.PersistRunWorkflowState(context.Background(), RunState{WorkflowID: "run-1", RunID: "run-1-a"})

	require.ErrorIs(t, err, execstore.ErrNotConfigured)
}
