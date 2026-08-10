package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// These pin the command's contract without a database. What migrating actually does
// to one is pinned by the container suite in migrate_integration_test.go.

func TestMigrateHelpExplainsTheOperationalOrder(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, migrateCmd([]string{"--help"}, &out))

	require.Contains(t, out.String(), "before starting 'worker' or 'serve'")
	require.Contains(t, out.String(), devAutoMigrateEnv)
}

func TestMigrateRefusesArgumentsItDoesNotUnderstand(t *testing.T) {
	// A mistyped argument must not be ignored: silently migrating everything when the
	// operator asked for something narrower is the wrong kind of helpful.
	err := migrateCmd([]string{"execution-store"}, &bytes.Buffer{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "execution-store")
}

func TestTheDevelopmentAutoMigrateModeIsOffUnlessItIsSwitchedOn(t *testing.T) {
	for _, value := range []string{"", "  ", "0", "false", "no", "off", "OFF"} {
		t.Setenv(devAutoMigrateEnv, value)
		require.False(t, devAutoMigrateEnabled(), "%q switched startup DDL on", value)
	}
	for _, value := range []string{"1", "true", "yes", "please"} {
		t.Setenv(devAutoMigrateEnv, value)
		require.True(t, devAutoMigrateEnabled(), "%q did not switch startup DDL on", value)
	}
}

func TestEveryContextAProcessUsesIsListedForIt(t *testing.T) {
	// The worker writes execution records; the API server reads them and owns the
	// dismissals. Both lists must be subsets of the set `migrate` applies, or a
	// process could verify a schema nothing ever applies.
	all := map[string]bool{}
	for _, target := range allSchemaContexts() {
		all[target.name] = true
	}
	for _, contexts := range [][]schemaContext{workerSchemaContexts(), serveSchemaContexts()} {
		for _, target := range contexts {
			require.True(t, all[target.name], "%s is verified but never migrated", target.name)
		}
	}
	require.Len(t, workerSchemaContexts(), 1)
	require.Len(t, serveSchemaContexts(), len(allSchemaContexts()))
}
