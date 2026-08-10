package hubpg

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
)

// The adapter is tested against a real Postgres, for the same reason the execution
// store's is: the database and its schema are an out-of-process dependency this
// project owns, and a mock would exercise none of what can actually go wrong here
// (the upsert that makes dismissing idempotent, the delete that must report having
// deleted nothing, the migration).
//
// The database is brought up by pgtest, and therefore by testcontainers-go, so
// `go test ./...` runs the suite with no setup, no environment variable and no compose
// service. It needs a working Docker (or compatible) daemon; when there is none the
// suite fails rather than skips, because a suite that quietly skips itself reports
// green while exercising none of the above.

func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }

// newTestStore gives the calling test a database of its own on the shared container,
// with the schema applied, and closes it afterwards.
func newTestStore(t *testing.T) *Dismissals {
	t.Helper()
	store := openTestStore(t, pgtest.NewDatabase(t))
	require.NoError(t, store.Migrate(context.Background()))
	return store
}

// openTestStore connects a store to an existing test database, which is how a test
// stands in for a second server against the same schema.
func openTestStore(t *testing.T, dsn string) *Dismissals {
	t.Helper()
	store, err := Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}
