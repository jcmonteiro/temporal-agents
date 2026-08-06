package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDatabaseURL_RequiresTheEnvironmentVariable(t *testing.T) {
	// The DSN has no default on purpose: guessing one could point the durable record
	// at the wrong database, or silently drop it. The contract is testable because
	// databaseURL reports instead of exiting.
	t.Setenv(databaseURLEnv, "   ")

	_, err := databaseURL()

	require.Error(t, err)
	require.Contains(t, err.Error(), databaseURLEnv, "the message names what to set")
}

func TestDatabaseURL_TrimsTheConfiguredValue(t *testing.T) {
	t.Setenv(databaseURLEnv, "  postgres://postgres:postgres@localhost:15432/temporal_agents  ")

	dsn, err := databaseURL()

	require.NoError(t, err)
	require.Equal(t, "postgres://postgres:postgres@localhost:15432/temporal_agents", dsn)
}
