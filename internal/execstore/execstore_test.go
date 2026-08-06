package execstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These are pure tests of the record types and the query-building logic: no
// database, no mocks. The adapter's behavior against a real Postgres lives in
// postgres_integration_test.go.

func TestValidKind_AcceptsEveryRecordedKindAndNothingElse(t *testing.T) {
	for _, k := range Kinds() {
		require.True(t, ValidKind(k), "%s is a recorded kind", k)
	}
	require.False(t, ValidKind("schedule"), "a schedule is not a kind: a fired run is a run carrying its schedule ID")
	require.False(t, ValidKind("open-pr"), "open-pr is folded into the develop record, not a kind of its own")
	require.False(t, ValidKind(""))
}

func TestExecution_RunningUntilItSettles(t *testing.T) {
	started := time.Date(2026, time.August, 6, 9, 0, 0, 0, time.UTC)
	inFlight := Execution{Status: StatusRunning, StartedAt: started}
	require.True(t, inFlight.Running())
	require.Zero(t, inFlight.Duration(), "an execution with no end time has no duration yet")

	settled := Execution{Status: StatusSucceeded, StartedAt: started, EndedAt: started.Add(90 * time.Second)}
	require.False(t, settled.Running())
	require.Equal(t, 90*time.Second, settled.Duration())
}

func TestDetail_OmitsWhatAKindDidNotProduce(t *testing.T) {
	// The detail column is shared by every kind, so a record must serialize only
	// the fields its own kind produced.
	raw, err := json.Marshal(Detail{Branch: "feat/x"})

	require.NoError(t, err)
	require.JSONEq(t, `{"branch":"feat/x"}`, string(raw))
}

func TestDetail_ConvergedDistinguishesFalseFromAbsent(t *testing.T) {
	// A review that did not converge must be recorded as false, not omitted as if
	// the question never applied.
	notConverged := false
	raw, err := json.Marshal(Detail{Converged: &notConverged})
	require.NoError(t, err)
	require.JSONEq(t, `{"converged":false}`, string(raw))

	var back Detail
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NotNil(t, back.Converged)
	require.False(t, *back.Converged)
}

func TestDetail_RoundTripsAFleetsPerNodeBreakdown(t *testing.T) {
	detail := Detail{
		PlanID:    "plan-1234abcd",
		PlanNodes: 2,
		Nodes: []NodeOutcome{
			{ID: "core", Status: string(StatusSucceeded), Detail: "Developed branch …", Tokens: 900},
			{ID: "rest", Status: string(StatusSkipped), Detail: `dependency "core" did not succeed`},
		},
	}

	raw, err := json.Marshal(detail)
	require.NoError(t, err)
	var back Detail
	require.NoError(t, json.Unmarshal(raw, &back))

	require.Equal(t, detail, back)
}

func TestBuildFilter_EmptyFilterConstrainsNothing(t *testing.T) {
	where, args := buildFilter(Filter{})

	require.Empty(t, where)
	require.Empty(t, args)
}

func TestBuildFilter_NumbersPlaceholdersPerConstraint(t *testing.T) {
	where, args := buildFilter(Filter{Kind: KindRun, ScheduleID: "schedule-9"})

	require.Equal(t, " WHERE kind = $1 AND schedule_id = $2", where)
	require.Equal(t, []any{"run", "schedule-9"}, args)
}

func TestBuildFilter_WorkflowIDMatchesTheWholeTree(t *testing.T) {
	// One workflow ID must select the execution itself (every continue-as-new
	// iteration) and its children, so a run's whole tree is shown.
	where, args := buildFilter(Filter{WorkflowID: "fleet-1"})

	require.Equal(t, " WHERE (workflow_id = $1 OR parent_workflow_id = $1)", where)
	require.Equal(t, []any{"fleet-1"}, args)
}

func TestNullMapping_AbsentValuesBecomeNull(t *testing.T) {
	// Empty strings and the zero time are "absent", and must reach Postgres as
	// NULL rather than as an empty string or a year-zero timestamp.
	require.Nil(t, nullString(""))
	require.Nil(t, nullTime(time.Time{}))

	s := nullString("schedule-9")
	require.NotNil(t, s)
	require.Equal(t, "schedule-9", *s)

	now := time.Now()
	ts := nullTime(now)
	require.NotNil(t, ts)
	require.Equal(t, now, *ts)
}

func TestMigrationNames_AreOrderedAndPresent(t *testing.T) {
	names, err := migrationNames()

	require.NoError(t, err)
	require.NotEmpty(t, names, "the schema must ship as embedded migrations")
	// Filenames are numbered, so lexical order is apply order.
	require.IsIncreasing(t, names)
}
