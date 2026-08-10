package pgmigrate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The rule these tests pin — what a database is at, and what is still missing from
// it — is the whole of what a startup verification decides on, so it is exercised
// without a database. Applying migrations is a different matter and is tested against
// a real Postgres by the adapters that own migrations.

func TestAFreshDatabaseIsAtNoVersionAndMissesEverything(t *testing.T) {
	state := newState("agenthub", []string{"0001_a.sql", "0002_b.sql"}, map[string]bool{})

	require.Equal(t, noVersion, state.Version())
	require.Equal(t, "0002_b.sql", state.RequiredVersion())
	require.Equal(t, []string{"0001_a.sql", "0002_b.sql"}, state.Missing)
	require.False(t, state.UpToDate())
}

func TestANamespaceIgnoresAnotherContextsMigrations(t *testing.T) {
	recorded := map[string]bool{"agenthub/0001_a.sql": true, "0001_executions.sql": true}

	namespaced := newState("agenthub", []string{"0001_a.sql"}, recorded)
	require.True(t, namespaced.UpToDate())
	require.Equal(t, "0001_a.sql", namespaced.Version())

	// The empty namespace owns exactly the un-namespaced rows, which is what keeps an
	// adapter that migrated before namespacing existed recognising its own history.
	bare := newState("", []string{"0001_executions.sql"}, recorded)
	require.True(t, bare.UpToDate())
	require.Equal(t, []string{"0001_executions.sql"}, bare.Applied)
}

func TestAPartiallyMigratedDatabaseNamesWhatIsMissing(t *testing.T) {
	state := newState("", []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"},
		map[string]bool{"0001_a.sql": true, "0002_b.sql": true})

	require.Equal(t, "0002_b.sql", state.Version())
	require.Equal(t, []string{"0003_c.sql"}, state.Missing)
	require.False(t, state.UpToDate())
}

func TestADatabaseAheadOfThisBinaryIsUpToDate(t *testing.T) {
	// A newer binary migrated it. This one can read everything it knows about, so it
	// must start rather than refuse and take the deployment down.
	state := newState("", []string{"0001_a.sql"}, map[string]bool{"0001_a.sql": true, "0002_b.sql": true})

	require.True(t, state.UpToDate())
	require.Equal(t, "0002_b.sql", state.Version())
	require.Equal(t, "0001_a.sql", state.RequiredVersion())
}
