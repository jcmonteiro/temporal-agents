package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/schema"
)

// A state is the value both processes decide on: whether to start, and what to tell
// an operator who has to act. The decisions are asserted here, once, so an adapter's
// own tests are about reading the record and nothing else.

func TestAVersionIsWhatTheRecordSaysAndNotWhatTheBinaryCarries(t *testing.T) {
	// A database migrated by a newer binary reports that newer version. Reading the
	// embedded files instead would make an old binary report a version nothing applied.
	state := schema.State{
		Applied:  []string{"0001_executions.sql", "0002_plans.sql", "0003_tokens.sql"},
		Required: []string{"0001_executions.sql", "0002_plans.sql"},
	}

	require.Equal(t, "0003_tokens.sql", state.Version())
	require.Equal(t, "0002_plans.sql", state.RequiredVersion())
}

func TestADatabaseNothingHasMigratedIsReportedAsBeingAtNoVersion(t *testing.T) {
	// "none" rather than an empty string: a startup failure that names no version at
	// all leaves an operator guessing whether the read failed.
	var never schema.State

	require.Equal(t, schema.NoVersion, never.Version())
	require.Equal(t, schema.NoVersion, never.RequiredVersion())
	require.True(t, never.UpToDate(), "a binary carrying no migrations requires nothing")
}

func TestOnlyAMissingRequiredMigrationMakesASchemaStale(t *testing.T) {
	for name, expectation := range map[string]struct {
		state schema.State
		want  bool
	}{
		"exactly at the required set": {
			state: schema.State{
				Applied:  []string{"0001_executions.sql"},
				Required: []string{"0001_executions.sql"},
			},
			want: true,
		},
		"ahead of this binary": {
			state: schema.State{
				Applied:  []string{"0001_executions.sql", "0002_plans.sql"},
				Required: []string{"0001_executions.sql"},
			},
			// A rollback must not take the deployment down: this binary can read
			// everything it knows about.
			want: true,
		},
		"behind this binary": {
			state: schema.State{
				Applied:  []string{"0001_executions.sql"},
				Required: []string{"0001_executions.sql", "0002_plans.sql"},
				Missing:  []string{"0002_plans.sql"},
			},
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, expectation.want, expectation.state.UpToDate())
		})
	}
}
