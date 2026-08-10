package execpg

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
)

// The adapter is tested against a real Postgres: the database and its schema are
// an out-of-process dependency this project owns, so a mock would exercise none of
// what can actually go wrong here (the upsert's conflict handling, the jsonb
// round-trip, the filter SQL, the migrations).
//
// The database is brought up by pgtest, and therefore by testcontainers-go, so
// `go test ./...` runs the suite with no setup, no environment variable and no
// compose service. It needs a working Docker (or compatible) daemon; when there is
// none the suite fails rather than skips, because a suite that quietly skips itself
// reports green while exercising none of the above.

func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }

// newTestStore gives the calling test a database of its own on the shared
// container, with the schema applied, and closes it afterwards.
func newTestStore(t *testing.T) *Postgres {
	t.Helper()
	store := newUnmigratedTestStore(t)
	require.NoError(t, store.Migrate(context.Background()))
	return store
}

// newUnmigratedTestStore is newTestStore without the schema, for the tests that
// are about applying it (and for the read that must report an unmigrated database
// as one).
func newUnmigratedTestStore(t *testing.T) *Postgres {
	t.Helper()
	return openTestStore(t, pgtest.NewDatabase(t))
}

// openTestStore connects another store to an existing test database, which is how
// a test stands in for a second worker against the same schema.
func openTestStore(t *testing.T, dsn string) *Postgres {
	t.Helper()
	store, err := Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

// withDSNParam returns dsn with one pool setting added. It parses instead of
// concatenating ("dsn + &key=value"), which would depend on the test database's DSN
// always carrying a query string already.
func withDSNParam(t *testing.T, dsn, key, value string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}
