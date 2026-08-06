package execstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These are pure tests of the record types: no database, no mocks. The adapter's
// SQL, and its behavior against a real Postgres, are tested in the execpg package
// next door.

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

func TestEffectiveLimit_AppliesTheDefaultAndTheCap(t *testing.T) {
	// The rule belongs to the port, so both the CLI and the adapter resolve a limit
	// through it: a consumer that forgets the cap still cannot ask for more.
	require.Equal(t, DefaultHistoryLimit, EffectiveLimit(0, DefaultHistoryLimit))
	require.Equal(t, DefaultPlanLimit, EffectiveLimit(-5, DefaultPlanLimit))
	require.Equal(t, 50, EffectiveLimit(50, DefaultHistoryLimit))
	require.Equal(t, MaxListLimit, EffectiveLimit(MaxListLimit, DefaultHistoryLimit))
	require.Equal(t, MaxListLimit, EffectiveLimit(MaxListLimit+1, DefaultHistoryLimit))
	// A default above the cap would still be served capped: the cap is the last word.
	require.Equal(t, MaxListLimit, EffectiveLimit(0, MaxListLimit*2))
}
