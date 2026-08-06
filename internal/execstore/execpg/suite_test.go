package execpg

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The adapter is tested against a real Postgres: the database and its schema are
// an out-of-process dependency this project owns, so a mock would exercise none of
// what can actually go wrong here (the upsert's conflict handling, the jsonb
// round-trip, the filter SQL, the migrations).
//
// The database is brought up by testcontainers-go, so `go test ./...` runs the
// suite with no setup, no environment variable and no compose service. It needs a
// working Docker (or compatible) daemon; when there is none the suite fails rather
// than skips, because a suite that quietly skips itself reports green while
// exercising none of the above.

// postgresImage pins the same Postgres, by the same digest, that the compose stack
// runs, so the suite tests the database the tool actually stores its history in.
const postgresImage = "postgres:17@sha256:7958605b474b3d264a969cb3a123d6aa00ad1e1fe9da8a69984dabb704d93317"

// adminDatabase is the container's initial database. Tests never use it for data:
// it is only where CREATE DATABASE is issued from.
const adminDatabase = "postgres"

// container is the throwaway Postgres the whole package's suite shares, started on
// first use by sharedContainer.
//
// One container per test would also be correct, but a cold Postgres costs seconds
// and this package has a suite's worth of tests. Instead each test gets a database
// of its own inside the one container (see newTestStore), which isolates it just as
// completely for a few milliseconds — and, unlike truncating shared tables, it
// cannot leak state in either direction.
//
// It is started lazily rather than in TestMain so the package's pure tests (the
// buildFilter ones) need no Docker daemon at all: only a test that asks for a
// database pays for one.
var (
	container     *postgres.PostgresContainer
	containerOnce sync.Once
)

func TestMain(m *testing.M) {
	code := m.Run()
	// os.Exit skips deferred calls, so the container is stopped explicitly — when a
	// test asked for one at all. The testcontainers reaper would collect it anyway,
	// but only after a delay.
	if container != nil {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("could not terminate the throwaway Postgres: %v", err)
		}
	}
	os.Exit(code)
}

// sharedContainer returns the package's Postgres, starting it on first use. A
// failure to start is fatal rather than a skip: a suite that quietly skips itself
// reports green while exercising none of the SQL it exists for.
func sharedContainer(t *testing.T) *postgres.PostgresContainer {
	t.Helper()
	containerOnce.Do(func() {
		ctr, err := postgres.Run(context.Background(), postgresImage,
			postgres.WithDatabase(adminDatabase),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			postgres.BasicWaitStrategies())
		if err != nil {
			log.Fatalf("could not start the throwaway Postgres for the execstore adapter suite "+
				"(is a Docker daemon running?): %v", err)
		}
		container = ctr
	})
	return container
}

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
	return openTestStore(t, newTestDatabase(t))
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

// dbSeq numbers the per-test databases. A counter keeps the names short, unique
// and always valid, which a test name (which may contain slashes, spaces and
// capitals) would not be.
var dbSeq atomic.Int64

// newTestDatabase creates an empty database on the shared container and returns
// its DSN.
func newTestDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	adminDSN, err := sharedContainer(t).ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	admin, err := pgx.Connect(ctx, adminDSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("test_%d", dbSeq.Add(1))
	// The name is generated here, not taken from input, so quoting it is enough.
	_, err = admin.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err)

	u, err := url.Parse(adminDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}
