package execpg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// These are pure tests of the adapter's query building and null mapping: no
// database. Its behavior against a real Postgres lives in
// execpg_integration_test.go.

func TestBuildFilter_EmptyFilterConstrainsNothing(t *testing.T) {
	where, args := buildFilter(execstore.Filter{})

	require.Empty(t, where)
	require.Empty(t, args)
}

func TestBuildFilter_NumbersPlaceholdersPerConstraint(t *testing.T) {
	where, args := buildFilter(execstore.Filter{Kind: execstore.KindRun, ScheduleID: "schedule-9"})

	require.Equal(t, " WHERE kind = $1 AND schedule_id = $2", where)
	require.Equal(t, []any{"run", "schedule-9"}, args)
}

func TestBuildFilter_WorkflowIDMatchesTheWholeTree(t *testing.T) {
	// One workflow ID must select the execution itself (every continue-as-new
	// iteration) and its children, so a run's whole tree is shown.
	where, args := buildFilter(execstore.Filter{WorkflowID: "fleet-1"})

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
