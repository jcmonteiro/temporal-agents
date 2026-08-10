package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
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
		// A forgotten value must not be filled in from the next flag: without this,
		// "--workflow-id --kind" quietly searches for the workflow "--kind".
		"flag as a value": {"--workflow-id", "--kind", "run"},
		// The listing is read into memory and printed as one table, so the cap is a
		// refusal rather than an attempt.
		"limit above the cap": {"--limit", "1001"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseHistoryFlags(args)
			require.Error(t, err)
		})
	}
}

func TestParseHistoryFlags_AcceptsTheLimitCapItself(t *testing.T) {
	f, err := parseHistoryFlags([]string{"--limit", "1000"})

	require.NoError(t, err)
	require.Equal(t, execstore.MaxListLimit, f.Limit)
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
	// A settled row reports how long it took, which is what an operator reads it for.
	require.Contains(t, out, "11m0s")
	require.Contains(t, out, "1,234,567")
	require.Contains(t, out, "1 execution(s) · 1,234,567 tokens")
}

func TestFormatHistory_RunningExecutionHasNoDurationYet(t *testing.T) {
	out := formatHistory([]execstore.Execution{{
		WorkflowID: "run-abc",
		Kind:       execstore.KindRun,
		Status:     execstore.StatusRunning,
		StartedAt:  time.Date(2026, time.August, 6, 9, 30, 0, 0, time.UTC),
	}})

	// An in-flight execution is listed and distinguished by its status; it reports no
	// duration rather than one that grows every time the table is printed.
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

func TestHistoryRows_FailedPipelineStillPointsAtItsPR(t *testing.T) {
	// A develop pipeline that failed *after* the PR was opened is exactly the row an
	// operator follows to the PR, so the failure must not drop the link.
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "develop-1", Kind: execstore.KindDevelop, Status: execstore.StatusFailed,
		Detail: execstore.Detail{Error: "pilot exploded", PRURL: "https://github.com/o/r/pull/7"},
	}})

	require.Contains(t, rows[0].Note, "pilot exploded")
	require.Contains(t, rows[0].Note, "https://github.com/o/r/pull/7")
}

// "Which instruction produced this?" has to be answerable from the history an
// operator already reads, so a row that ran under stored instructions says which
// key, from where, in which version, and enough of the hash to tell two apart.
func TestHistoryRows_NoteSaysWhichInstructionTheExecutionRanUnder(t *testing.T) {
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "review-1", Kind: execstore.KindReview,
		Detail: execstore.Detail{Instructions: []execstore.InstructionUse{
			{Key: "review.perform", Scope: "directory:/src/agents", Version: 3, Hash: "abcdef0123456789"},
			{Key: "review.implement", Scope: "factory", Version: 1, Hash: "0123456789abcdef"},
		}},
	}})

	require.Contains(t, rows[0].Note, "review.perform directory v3 abcdef01")
	require.Contains(t, rows[0].Note, "review.implement factory v1 01234567")
	// The scope carries an absolute path, which is the server's directory layout and
	// not what the line is read for.
	require.NotContains(t, rows[0].Note, "/src/agents")
}

// An execution that ran under no stored instruction says nothing about them: an
// empty label in every row would be noise in the column that carries the reason a
// row is worth reading.
func TestHistoryRows_NoteSaysNothingAboutInstructionsWhenNoneWereUsed(t *testing.T) {
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "run-1", Kind: execstore.KindRun, Prompt: "add a rate limiter",
	}})

	require.Equal(t, "add a rate limiter", rows[0].Note)
}

func TestFormatHistory_CountsRecordedExecutionsAndExpandedNodesApart(t *testing.T) {
	// The table prints more lines than there are records: a skipped node has no
	// record of its own. The summary line reports both, so it cannot contradict the
	// table it closes.
	out := formatHistory([]execstore.Execution{{
		WorkflowID: "fleet-1", Kind: execstore.KindFleet, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Nodes: []execstore.NodeOutcome{
			{ID: "rest", Status: string(execstore.StatusSkipped), Detail: "dependency did not succeed"},
		}},
	}})

	require.Contains(t, out, "1 execution(s) · 1 skipped node(s)")
}

func TestFormatDuration(t *testing.T) {
	require.Equal(t, "-", formatDuration(0), "nothing to report reads as absent, not as 0s")
	require.Equal(t, "<1s", formatDuration(400*time.Millisecond))
	require.Equal(t, "11m0s", formatDuration(11*time.Minute))
}

// The history command reads through the execstore port, so the whole command —
// flag parsing, the store failure, and the release of the connection — is reachable
// with the in-memory fake in the adapter's place.

func TestHistoryCmd_ReadsThroughThePortAndReleasesIt(t *testing.T) {
	reader := &filterCapturingReader{}
	released := false

	err := historyCmd([]string{"--kind", "develop", "--limit", "5"}, io.Discard,
		func(context.Context) (execstore.ExecutionReader, func(), error) {
			return reader, func() { released = true }, nil
		})

	require.NoError(t, err)
	require.Equal(t, execstore.Filter{Kind: execstore.KindDevelop, Limit: 5}, reader.filter,
		"the parsed filter is what the port is queried with")
	require.True(t, released, "the store is released even on the success path")
}

func TestHistoryCmd_ReportsAStoreThatCannotBeRead(t *testing.T) {
	err := historyCmd(nil, io.Discard, func(context.Context) (execstore.ExecutionReader, func(), error) {
		return execstoretest.Failing(errors.New("postgres is down")), func() {}, nil
	})

	require.ErrorContains(t, err, "could not read the execution history")
	require.ErrorContains(t, err, "postgres is down")
}

func TestHistoryCmd_ReportsAStoreThatCannotBeOpened(t *testing.T) {
	// The "DATABASE_URL is unset" contract reaches the operator through this path.
	err := historyCmd(nil, io.Discard, func(context.Context) (execstore.ExecutionReader, func(), error) {
		return nil, nil, errors.New("DATABASE_URL is not set")
	})

	require.ErrorContains(t, err, "DATABASE_URL is not set")
}

func TestHistoryCmd_RejectsABadFlagBeforeTouchingTheStore(t *testing.T) {
	opened := false

	err := historyCmd([]string{"--kind", "developp"}, io.Discard,
		func(context.Context) (execstore.ExecutionReader, func(), error) {
			opened = true
			return execstoretest.New(), func() {}, nil
		})

	require.Error(t, err)
	require.False(t, opened, "an unusable filter is refused before a connection is made")
}

// filterCapturingReader is a stub of the read port that records the filter it was
// queried with. The shared fake deliberately ignores the filter (filtering is SQL,
// covered by the adapter's own suite), so capturing it here is what pins that the
// command's parsed filter reaches the port unchanged.
type filterCapturingReader struct {
	filter execstore.Filter
}

func (r *filterCapturingReader) ListExecutions(_ context.Context, f execstore.Filter) ([]execstore.Execution, error) {
	r.filter = f
	return nil, nil
}

func TestHistoryRows_FailedScheduledRunKeepsBothItsScheduleAndItsReason(t *testing.T) {
	// A schedule that keeps failing is only visible if the note says both which
	// schedule fired the run and why the run failed.
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "run-1", Kind: execstore.KindRun, ScheduleID: "schedule-9",
		Status: execstore.StatusFailed, Detail: execstore.Detail{Error: "pi crashed"},
	}})

	require.Equal(t, "schedule schedule-9: pi crashed", rows[0].Note)
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

func TestHistoryRows_NoteShowsWhatEachKindRecorded(t *testing.T) {
	// A recorded field nothing prints is weight, not memory: the tri-states, the pass
	// number and the plan correlation all have to reach the operator's row.
	converged, addressed := true, false
	rows := historyRows([]execstore.Execution{
		{WorkflowID: "review-1", Kind: execstore.KindReview,
			Detail: execstore.Detail{Pass: 3, Converged: &converged}},
		{WorkflowID: "pilot-2", Kind: execstore.KindPilot,
			Detail: execstore.Detail{Pass: 1, Addressed: &addressed}},
		{WorkflowID: "fleet-3", Kind: execstore.KindFleet,
			Detail: execstore.Detail{PlanID: "plan-1a2b3c4d5e6f7890", PlanNodes: 4}},
	})

	require.Equal(t, "pass 3 · converged", rows[0].Note)
	require.Equal(t, "pass 1 · no comments addressed", rows[1].Note)
	require.Equal(t, "plan plan-1a2b3c4d5e6f7890 (4 node(s))", rows[2].Note)
}

func TestHistoryRows_NoteDistinguishesBothTriStates(t *testing.T) {
	// "not converged" is the outcome an operator has to act on, so it must be said
	// rather than left absent — an absent label reads like a workflow that never
	// reviews.
	notConverged, addressed := false, true
	rows := historyRows([]execstore.Execution{
		{WorkflowID: "review-1", Kind: execstore.KindReview, Detail: execstore.Detail{Converged: &notConverged}},
		{WorkflowID: "pilot-2", Kind: execstore.KindPilot, Detail: execstore.Detail{Addressed: &addressed}},
		// A kind that neither reviews nor pilots records neither, and so says neither.
		{WorkflowID: "run-3", Kind: execstore.KindRun},
	})

	require.Equal(t, "not converged", rows[0].Note)
	require.Equal(t, "addressed comments", rows[1].Note)
	require.Empty(t, rows[2].Note)
}

func TestHistoryRows_NoteIdentifiesAPlainRunByItsPrompt(t *testing.T) {
	// A run row is otherwise identified by nothing but "run-<uuid>", so without the
	// prompt an operator cannot tell five runs apart. Anything more specific wins over
	// it: the prompt is long, and a failure is what the row is read for.
	rows := historyRows([]execstore.Execution{
		{WorkflowID: "run-1", Kind: execstore.KindRun, Prompt: "tidy the parser\nsecond line"},
		{WorkflowID: "run-2", Kind: execstore.KindRun, Prompt: "tidy the parser",
			Status: execstore.StatusFailed, Detail: execstore.Detail{Error: "pi crashed"}},
	})

	require.Equal(t, "tidy the parser", rows[0].Note)
	require.Equal(t, "pi crashed", rows[1].Note)
}

func TestHistoryRows_SkippedNodeNoteIsShortenedLikeEveryOther(t *testing.T) {
	// A node's detail is bounded only at 8 KiB, so it goes through the same first-line
	// and width treatment as every other note instead of breaking the table.
	rows := historyRows([]execstore.Execution{{
		WorkflowID: "fleet-1", Kind: execstore.KindFleet, Status: execstore.StatusSucceeded,
		Detail: execstore.Detail{Nodes: []execstore.NodeOutcome{{
			ID:     "rest",
			Status: string(execstore.StatusSkipped),
			Detail: strings.Repeat("x", 200) + "\nand a second line",
		}}},
	}})

	require.LessOrEqual(t, len(rows[1].Note), noteWidth+len("…"))
	require.NotContains(t, rows[1].Note, "\n")
}

func TestHistoryCmd_PrintsTheTableToItsWriter(t *testing.T) {
	// What the operator sees is asserted here rather than printed into the test log:
	// the command writes to the writer it is given.
	var out strings.Builder
	reader := execstoretest.New()
	require.NoError(t, reader.SaveExecution(context.Background(), execstore.Execution{
		RunID: "r1", WorkflowID: "run-abc", Kind: execstore.KindRun, Prompt: "tidy the parser",
		Status: execstore.StatusSucceeded, Tokens: 1200,
	}))

	err := historyCmd(nil, &out, func(context.Context) (execstore.ExecutionReader, func(), error) {
		return reader, func() {}, nil
	})

	require.NoError(t, err)
	require.Contains(t, out.String(), "run-abc")
	require.Contains(t, out.String(), "tidy the parser")
	require.Contains(t, out.String(), "1 execution(s) · 1,200 tokens")
}

func TestHistoryCmd_HelpGoesToItsWriterToo(t *testing.T) {
	var out strings.Builder

	err := historyCmd([]string{"--help"}, &out, func(context.Context) (execstore.ExecutionReader, func(), error) {
		t.Fatal("help must not touch the store")
		return nil, nil, nil
	})

	require.NoError(t, err)
	require.Contains(t, out.String(), "temporal-agents history")
}
