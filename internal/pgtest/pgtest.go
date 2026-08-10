// Package pgtest brings up the throwaway Postgres the container suites run against.
//
// It exists because the bootstrap is the same everywhere and the image is a pinned
// digest: three copies of "start a container, create a database per test, stop it
// afterwards" are three places to bump a digest and three chances to bump only two of
// them. A suite now says what it needs (a database of its own) and nothing about how
// it is obtained.
//
// It is a normal (non _test) package because Go cannot import another package's test
// files, exactly as wftest is.
//
// A suite that cannot reach a Docker (or compatible) daemon fails rather than skips:
// a suite that quietly skips itself reports green while exercising none of the SQL it
// exists for.
package pgtest

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image pins the Postgres every suite runs, by digest, so the suites test the same
// database the tool itself runs against. It is stated once here; the compose file is
// checked against it by TestTheComposeStackRunsThePinnedPostgres.
const Image = "postgres:17@sha256:7958605b474b3d264a969cb3a123d6aa00ad1e1fe9da8a69984dabb704d93317"

// adminDatabase is the container's initial database. No test uses it for data: it is
// only where CREATE DATABASE is issued from.
const adminDatabase = "postgres"

// container is the throwaway Postgres a package's suite shares, started on first use.
//
// One container per test would also be correct, but a cold Postgres costs seconds.
// Instead each test gets a database of its own inside the one container (see
// NewDatabase), which isolates it just as completely for a few milliseconds and,
// unlike truncating shared tables, cannot leak state in either direction.
//
// It is started lazily rather than in Run, so a package's pure tests need no Docker
// daemon at all: only a test that asks for a database pays for one.
var (
	container     *postgres.PostgresContainer
	containerOnce sync.Once
)

// Run runs a package's tests and stops the shared container afterwards, so a suite's
// TestMain is one line:
//
//	func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }
//
// The container is stopped explicitly because os.Exit skips deferred calls. The
// testcontainers reaper would collect it anyway, but only after a delay.
func Run(m *testing.M) int {
	code := m.Run()
	if container != nil {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("could not terminate the throwaway Postgres: %v", err)
		}
	}
	return code
}

// sharedContainer returns the package's Postgres, starting it on first use. A failure
// to start is fatal rather than a skip.
func sharedContainer() *postgres.PostgresContainer {
	containerOnce.Do(func() {
		ctr, err := postgres.Run(context.Background(), Image,
			postgres.WithDatabase(adminDatabase),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			postgres.BasicWaitStrategies())
		if err != nil {
			log.Fatalf("could not start the throwaway Postgres "+
				"(is a Docker daemon running?): %v", err)
		}
		container = ctr
	})
	return container
}

// dbSeq numbers the per-test databases. A counter keeps the names short, unique and
// always valid, which a test name (which may contain slashes, spaces and capitals)
// would not be.
var dbSeq atomic.Int64

// NewDatabase creates an empty database on the shared container and returns its DSN.
// An empty database is also a genuinely stale one: it is at no schema version at all.
func NewDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	adminDSN, err := sharedContainer().ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("could not read the throwaway Postgres' connection string: %v", err)
	}

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("could not connect to the throwaway Postgres: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("test_%d", dbSeq.Add(1))
	// The name is generated here, not taken from input, so quoting it is enough.
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("could not create the test database %s: %v", name, err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("the throwaway Postgres' connection string is not a URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}
