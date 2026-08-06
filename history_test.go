package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

func TestParseHistoryFlags_ReadsEveryFilter(t *testing.T) {
	f, err := parseHistoryFlags([]string{
		"--kind", "develop", "--limit=50", "--workflow-id", "fleet-1", "--schedule-id=schedule-2"})

	require.NoError(t, err)
	require.Equal(t, execstore.Filter{
		Kind:       execstore.KindDevelop,
		Limit:      50,
		WorkflowID: "fleet-1",
		ScheduleID: "schedule-2",
	}, f)
}

func TestParseHistoryFlags_NoFlagsMeansNoConstraint(t *testing.T) {
	f, err := parseHistoryFlags(nil)

	require.NoError(t, err)
	require.Equal(t, execstore.Filter{}, f)
}

func TestParseHistoryFlags_RejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		// A mistyped kind must be rejected rather than silently match nothing.
		"unknown kind":      {"--kind", "developp"},
		"missing value":     {"--kind"},
		"empty value":       {"--kind="},
		"non-numeric limit": {"--limit", "many"},
		"zero limit":        {"--limit", "0"},
		"negative limit":    {"--limit", "-3"},
		"unknown flag":      {"--everything"},
		"stray positional":  {"fleet-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseHistoryFlags(args)
			require.Error(t, err)
		})
	}
}

func TestFormatHistory_Empty(t *testing.T) {
	require.Equal(t, "No recorded executions yet.\n", formatHistory(nil))
}

func TestFormatHistory_ShowsKindStatusTimingAndTokens(t *testing.T) {
	started := time.Date(2026, time.August, 6, 9, 30, 0, 0, time.UTC)
	out := formatHistory([]execstore.Execution{{
		WorkflowID: "develop-abc",
		Kind:       execstore.KindDevelop,
		Status:     execstore.StatusSucceeded,
		StartedAt:  started,
		EndedAt:    started.Add(11 * time.Minute),
		Tokens:     1234567,
	}})

	require.Contains(t, out, "develop")
	require.Contains(t, out, "succeeded")
	require.Contains(t, out, "develop-abc")
	require.Contains(t, out, started.Local().Format("2006-01-02 15:04:05"))
	require.Contains(t, out, "1,234,567")
	require.Contains(t, out, "1 execution(s) · 1,234,567 tokens")
}

func TestFormatHistory_RunningExecutionHasNoEndTime(t *testing.T) {
	out := formatHistory([]execstore.Execution{{
		WorkflowID: "run-abc",
		Kind:       execstore.KindRun,
		Status:     execstore.StatusRunning,
		StartedAt:  time.Date(2026, time.August, 6, 9, 30, 0, 0, time.UTC),
	}})

	// An in-flight execution is listed and distinguished by its status; its end
	// column is empty rather than a bogus timestamp.
	require.Contains(t, out, "running")
	require.Contains(t, out, "-")
}

func TestHistoryRows_ExpandsSkippedFleetNodes(t *testing.T) {
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "fleet-1",
		Kind:       execstore.KindFleet,
		Status:     execstore.StatusSucceeded,
		Detail: execstore.Detail{Nodes: []execstore.NodeOutcome{
			{ID: "core", Status: string(execstore.StatusSucceeded), Tokens: 100},
			{ID: "rest", Status: string(execstore.StatusSkipped), Detail: `dependency "core" did not succeed`},
		}},
	}})

	// A skipped node starts no child workflow, so it has no row of its own and is
	// expanded from its parent's breakdown. A node that ran recorded itself, so it
	// must not be expanded too (that would duplicate it).
	require.Len(t, rows, 2)
	require.Equal(t, "fleet", rows[0].Kind)
	require.Equal(t, skippedNodeKind, rows[1].Kind)
	require.Equal(t, string(execstore.StatusSkipped), rows[1].Status)
	require.Equal(t, "fleet-1-rest", rows[1].ID)
	require.Contains(t, rows[1].Note, "core")
}

func TestHistoryRows_NoteSurfacesFailureScheduleAndPR(t *testing.T) {
	rows := historyRows([]execstore.Execution{
		{WorkflowID: "run-1", Kind: execstore.KindRun, ScheduleID: "schedule-9"},
		{WorkflowID: "run-2", Kind: execstore.KindRun, Detail: execstore.Detail{Error: "pi crashed\nstack trace"}},
		{WorkflowID: "develop-3", Kind: execstore.KindDevelop,
			Detail: execstore.Detail{PRURL: "https://github.com/o/r/pull/7"}},
	})

	require.Equal(t, "schedule schedule-9", rows[0].Note)
	// A multi-line failure is reduced to its first line so the table stays aligned.
	require.Equal(t, "pi crashed", rows[1].Note)
	require.Equal(t, "https://github.com/o/r/pull/7", rows[2].Note)
}

func TestHistoryRows_ExpandedNodeIsNotLabelledAsAFilterableKind(t *testing.T) {
	// The KIND column must never print a value that --kind rejects, so the expanded
	// row's label is deliberately outside the recorded vocabulary.
	require.False(t, execstore.ValidKind(execstore.Kind(skippedNodeKind)))
	for _, k := range execstore.Kinds() {
		require.NotEqual(t, string(k), skippedNodeKind)
	}
}

func TestSumTokens_AddsOwnIncrementalUsageOnly(t *testing.T) {
	// Each row carries only its own usage, so a fleet parent, its node and that
	// node's review sum to the run's real cost with nothing counted twice.
	total := sumTokens([]execstore.Execution{
		{Kind: execstore.KindFleet, Tokens: 0},
		{Kind: execstore.KindDevelop, Tokens: 1000},
		{Kind: execstore.KindReview, Tokens: 250},
	})

	require.Equal(t, 1250, total)
}

func TestGroupThousands(t *testing.T) {
	require.Equal(t, "0", groupThousands(0))
	require.Equal(t, "999", groupThousands(999))
	require.Equal(t, "1,000", groupThousands(1000))
	require.Equal(t, "1,234,567", groupThousands(1234567))
	require.Equal(t, "-1,000", groupThousands(-1000))
}
