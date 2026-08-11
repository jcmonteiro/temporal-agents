package execstoretest

import (
	"context"
	"testing"
	"time"

	"temporal-agents/internal/execstore"
)

func TestExecutionChainsIncludeDetachedChildrenButNotSupervisedChildren(t *testing.T) {
	store := New()
	started := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for _, execution := range []execstore.Execution{
		{
			WorkflowID: "review-detached", RunID: "detached-1", Kind: execstore.KindReview,
			ParentWorkflowID: "develop-1", StartedAt: started, Status: execstore.StatusRunning,
			Detail: execstore.Detail{Detached: true},
		},
		{
			WorkflowID: "review-supervised", RunID: "supervised-1", Kind: execstore.KindReview,
			ParentWorkflowID: "develop-2", StartedAt: started, Status: execstore.StatusRunning,
		},
	} {
		if err := store.SaveExecution(context.Background(), execution); err != nil {
			t.Fatalf("save execution: %v", err)
		}
	}

	chains, err := store.ListExecutionChains(context.Background(), execstore.ChainFilter{
		Kinds: []execstore.Kind{execstore.KindReview},
	})
	if err != nil {
		t.Fatalf("ListExecutionChains: %v", err)
	}
	if len(chains) != 1 || chains[0].Latest.WorkflowID != "review-detached" {
		t.Fatalf("chains = %+v, want only the detached review", chains)
	}
}

// TestStoreKeepsWritesAndReadsAsSeparateViews pins the fake's two responsibilities:
// workflow tests can inspect each attempted write, while read-port consumers see the
// current row that PostgreSQL upserts by run ID.
func TestStoreKeepsWritesAndReadsAsSeparateViews(t *testing.T) {
	store := New()
	started := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	start := execstore.Execution{
		WorkflowID: "run-1",
		RunID:      "iteration-1",
		Kind:       execstore.KindRun,
		StartedAt:  started,
		Status:     execstore.StatusRunning,
	}
	terminal := start
	terminal.Status = execstore.StatusSucceeded
	terminal.EndedAt = started.Add(time.Minute)
	terminal.Tokens = 42

	if err := store.SaveExecution(context.Background(), start); err != nil {
		t.Fatalf("save start: %v", err)
	}
	if err := store.SaveExecution(context.Background(), terminal); err != nil {
		t.Fatalf("save terminal: %v", err)
	}

	if writes := store.Records(); len(writes) != 2 {
		t.Fatalf("write journal has %d records, want both writes", len(writes))
	}
	chains, err := store.ListExecutionChains(context.Background(), execstore.ChainFilter{
		Kinds: []execstore.Kind{execstore.KindRun},
	})
	if err != nil {
		t.Fatalf("ListExecutionChains: %v", err)
	}
	if len(chains) != 1 || chains[0].Iterations != 1 || chains[0].Tokens != 42 || chains[0].Latest.Status != execstore.StatusSucceeded {
		t.Fatalf("chains = %+v, want one current successful iteration", chains)
	}
}
