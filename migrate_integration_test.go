package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"temporal-agents/internal/pgmigrate"
)

// Schema application is an operational contract — one deliberate step, and two
// processes that refuse to run against a database older than they are — so it is
// tested against a real Postgres. A fake would confirm the code calls itself; only a
// database can show that a fresh one comes up, that a stale one is refused, that
// applying twice changes nothing, and that two invocations at once do not corrupt
// each other.
//
// The database is brought up by testcontainers-go, so `go test ./...` runs the suite
// with no setup and no compose service. It needs a working Docker (or compatible)
// daemon; when there is none the suite fails rather than skips, because a suite that
// quietly skips itself reports green while exercising none of the above.

// postgresImage pins the same Postgres, by the same digest, that the compose stack
// runs, so the suite migrates the database the tool actually uses.
const postgresImage = "postgres:17@sha256:7958605b474b3d264a969cb3a123d6aa00ad1e1fe9da8a69984dabb704d93317"

// adminDatabase is the container's initial database. It is only where CREATE
// DATABASE is issued from.
const adminDatabase = "postgres"

// container is the throwaway Postgres the suite shares, started on first use. Each
// test then gets a database of its own inside it, which isolates it completely for a
// few milliseconds and cannot leak state in either direction.
var (
	container     *postgres.PostgresContainer
	containerOnce sync.Once
)

func TestMain(m *testing.M) {
	code := m.Run()
	// os.Exit skips deferred calls, so the container is stopped explicitly — when a
	// test asked for one at all. The package's pure tests need no Docker daemon.
	if container != nil {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("could not terminate the throwaway Postgres: %v", err)
		}
	}
	os.Exit(code)
}

// sharedContainer returns the suite's Postgres, starting it on first use.
func sharedContainer(t *testing.T) *postgres.PostgresContainer {
	t.Helper()
	containerOnce.Do(func() {
		ctr, err := postgres.Run(context.Background(), postgresImage,
			postgres.WithDatabase(adminDatabase),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			postgres.BasicWaitStrategies())
		if err != nil {
			log.Fatalf("could not start the throwaway Postgres for the migration suite "+
				"(is a Docker daemon running?): %v", err)
		}
		container = ctr
	})
	return container
}

// dbSeq numbers the per-test databases. A counter keeps the names short, unique and
// always valid, which a test name would not be.
var dbSeq atomic.Int64

// newTestDatabase creates an empty database on the shared container and returns its
// DSN. An empty database is a genuinely stale one: it is at no version at all.
func newTestDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	adminDSN, err := sharedContainer(t).ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	admin, err := pgx.Connect(ctx, adminDSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("migrate_%d", dbSeq.Add(1))
	// The name is generated here, not taken from input, so quoting it is enough.
	_, err = admin.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err)

	u, err := url.Parse(adminDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// schemaStateOf reads one context's state through its own adapter, which is the same
// read the command and the startup verification use.
func schemaStateOf(t *testing.T, dsn string, target schemaContext) pgmigrate.State {
	t.Helper()
	schema, err := target.open(context.Background(), dsn)
	require.NoError(t, err)
	defer schema.Close()
	state, err := schema.SchemaState(context.Background())
	require.NoError(t, err)
	return state
}

// recordedMigrations reads the tracking table directly, so a test can see what the
// database really holds rather than what an adapter reports about it. A database
// without the table reports nothing, which is what "verification applied nothing"
// looks like on a fresh one.
func recordedMigrations(t *testing.T, dsn string) []string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	var exists bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists))
	if !exists {
		return nil
	}
	rows, err := conn.Query(ctx, `SELECT name FROM schema_migrations ORDER BY name`)
	require.NoError(t, err)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return names
}

// forgetMigration removes one migration's tracking row, which is how a test produces
// the state an older deployment is really in: a database that has not had this
// build's newest migration applied to it.
func forgetMigration(t *testing.T, dsn, name string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	tag, err := conn.Exec(ctx, `DELETE FROM schema_migrations WHERE name = $1`, name)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "the migration to forget must have been applied")
}

func TestMigrateBringsAFreshDatabaseUpAndReportsEveryContextsVersion(t *testing.T) {
	dsn := newTestDatabase(t)

	var out bytes.Buffer
	require.NoError(t, migrateSchemas(context.Background(), dsn, allSchemaContexts(), &out))

	report := out.String()
	for _, target := range allSchemaContexts() {
		state := schemaStateOf(t, dsn, target)
		require.True(t, state.UpToDate(), "%s is not up to date after migrating", target.name)
		require.Contains(t, report, target.name, "the report names every context")
		require.Contains(t, report, state.Version(), "the report names %s's resulting version", target.name)
	}
	require.Contains(t, report, "applied")

	// Each context's migrations stay its own: they are recorded under its own
	// namespace, so no context can be confused with, or blocked by, another's.
	recorded := recordedMigrations(t, dsn)
	require.Contains(t, recorded, "0001_executions.sql")
	require.Contains(t, recorded, "agenthub/0001_dismissals.sql")
}

func TestBothProcessesRefuseADatabaseWithNoSchemaAtAll(t *testing.T) {
	dsn := newTestDatabase(t)
	t.Setenv(databaseURLEnv, dsn)

	for name, contexts := range map[string][]schemaContext{
		"worker": workerSchemaContexts(),
		"serve":  serveSchemaContexts(),
	} {
		var out bytes.Buffer
		err := requireCurrentSchema(context.Background(), contexts, &out)

		require.Error(t, err, "%s started against an unmigrated database", name)
		require.ErrorIs(t, err, pgmigrate.ErrStale)
		require.Contains(t, err.Error(), "temporal-agents migrate", "%s's failure carries the remedy", name)
		require.Contains(t, err.Error(), "none", "%s's failure names the version the database is at", name)
		require.Empty(t, recordedMigrations(t, dsn), "%s applied DDL while verifying", name)
	}
}

func TestBothProcessesRefuseADatabaseMissingThisBuildsNewestMigration(t *testing.T) {
	dsn := newTestDatabase(t)
	t.Setenv(databaseURLEnv, dsn)
	require.NoError(t, migrateSchemas(context.Background(), dsn, allSchemaContexts(), &bytes.Buffer{}))

	newest := schemaStateOf(t, dsn, executionStoreSchema).RequiredVersion()
	forgetMigration(t, dsn, newest)

	for name, contexts := range map[string][]schemaContext{
		"worker": workerSchemaContexts(),
		"serve":  serveSchemaContexts(),
	} {
		err := requireCurrentSchema(context.Background(), contexts, &bytes.Buffer{})

		require.ErrorIs(t, err, pgmigrate.ErrStale, "%s started against a stale database", name)
		require.Contains(t, err.Error(), executionStoreSchema.name)
		require.Contains(t, err.Error(), newest, "%s's failure names the missing migration", name)
		require.Contains(t, err.Error(), "temporal-agents migrate")
	}
}

func TestAContextAProcessDoesNotUseIsNotThatProcessesDependency(t *testing.T) {
	dsn := newTestDatabase(t)
	t.Setenv(databaseURLEnv, dsn)
	require.NoError(t, migrateSchemas(context.Background(), dsn, allSchemaContexts(), &bytes.Buffer{}))

	// The hub's own schema goes stale. The worker never writes a dismissal, so it must
	// still start; the API server owns them, so it must not.
	forgetMigration(t, dsn, "agenthub/"+schemaStateOf(t, dsn, agentHubSchema).RequiredVersion())

	require.NoError(t, requireCurrentSchema(context.Background(), workerSchemaContexts(), &bytes.Buffer{}))
	require.ErrorIs(t,
		requireCurrentSchema(context.Background(), serveSchemaContexts(), &bytes.Buffer{}),
		pgmigrate.ErrStale)
}

func TestMigratingAnUpToDateDatabaseChangesNothing(t *testing.T) {
	dsn := newTestDatabase(t)
	require.NoError(t, migrateSchemas(context.Background(), dsn, allSchemaContexts(), &bytes.Buffer{}))
	first := recordedMigrations(t, dsn)

	var out bytes.Buffer
	require.NoError(t, migrateSchemas(context.Background(), dsn, allSchemaContexts(), &out))

	require.Equal(t, first, recordedMigrations(t, dsn), "a second migrate changed the applied set")
	require.Contains(t, out.String(), "already up to date")
	require.NotContains(t, out.String(), "applied")
	require.NoError(t, requireCurrentSchema(context.Background(), allSchemaContexts(), &bytes.Buffer{}),
		"an up-to-date database is accepted by a starting process")
}

func TestTwoConcurrentMigrateInvocationsBothSucceedAndApplyEachMigrationOnce(t *testing.T) {
	dsn := newTestDatabase(t)

	// Two operators (or a deployment's two replicas) running the step at the same
	// moment must serialize on the database, not corrupt each other.
	const invocations = 2
	failures := make(chan error, invocations)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range invocations {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			failures <- migrateSchemas(context.Background(), dsn, allSchemaContexts(), &bytes.Buffer{})
		}()
	}
	start.Done()
	done.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err, "a concurrent migrate invocation failed")
	}

	recorded := recordedMigrations(t, dsn)
	seen := map[string]int{}
	for _, name := range recorded {
		seen[name]++
	}
	for name, count := range seen {
		require.Equal(t, 1, count, "%s was recorded more than once", name)
	}
	for _, target := range allSchemaContexts() {
		require.True(t, schemaStateOf(t, dsn, target).UpToDate(), "%s is not up to date", target.name)
	}
}

func TestTheDevelopmentModeIsTheOnlyWayAStartingProcessAppliesDDL(t *testing.T) {
	dsn := newTestDatabase(t)
	t.Setenv(databaseURLEnv, dsn)
	t.Setenv(devAutoMigrateEnv, "1")

	var out bytes.Buffer
	require.NoError(t, requireCurrentSchema(context.Background(), serveSchemaContexts(), &out))

	require.Contains(t, out.String(), devAutoMigrateEnv, "the development mode announces itself")
	require.Contains(t, strings.ToLower(out.String()), "warning")
	require.True(t, schemaStateOf(t, dsn, executionStoreSchema).UpToDate())
}

func TestMigrateReportsAnUnreachableDatabaseInsteadOfPanicking(t *testing.T) {
	err := migrateSchemas(context.Background(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		allSchemaContexts(), &bytes.Buffer{})

	require.Error(t, err)
	require.False(t, errors.Is(err, pgmigrate.ErrStale), "an unreachable database is not a stale one")
}
