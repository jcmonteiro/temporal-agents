package steeringpg

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
)

// The adapter is tested against a real Postgres, because everything it exists for is
// the database's behaviour and not Go's: the single statement that lets the first
// decision win when two tabs race, the per-session lock that keeps a conversation's
// sequence dense, the foreign key that refuses a turn of a conversation nobody
// started, and the migration itself. A mock would exercise none of it.
//
// The database is brought up by pgtest, and therefore by testcontainers-go, so
// `go test ./...` runs the suite with no setup and no compose service. It needs a
// working Docker (or compatible) daemon; when there is none the suite fails rather
// than skips, because a suite that quietly skips itself reports green while proving
// nothing.

func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }

// newTestStore gives the calling test a database of its own on the shared container,
// with the steering schema applied, and closes it afterwards.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store := openTestStore(t, pgtest.NewDatabase(t))
	require.NoError(t, store.Migrate(context.Background()))
	return store
}

// openTestStore connects a store to an existing test database, which is how a test
// stands in for a second process against the same schema.
func openTestStore(t *testing.T, dsn string) *Store {
	t.Helper()
	store, err := Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}
