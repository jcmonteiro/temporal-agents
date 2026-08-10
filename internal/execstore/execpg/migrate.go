package execpg

import (
	"context"
	"embed"

	"temporal-agents/internal/pgmigrate"
	"temporal-agents/internal/schema"
)

// migrationFS holds the schema as embedded SQL files, applied in filename order.
// Embedding them keeps the schema in the binary, so bringing the stack up needs
// no separate migrate binary, no manual psql step, and no files on the host.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationDir is where the embedded migrations live inside migrationFS.
const migrationDir = "migrations"

// migrationNamespace is the prefix this adapter's migrations are recorded under.
// It is deliberately empty: these migrations were recorded by their bare filename
// before the shared runner existed, and a database that has already applied them
// must keep recognising them rather than re-running the whole schema.
const migrationNamespace = ""

// Migrate applies every embedded migration that has not been applied yet, in
// filename order, and records each in the schema_migrations table. It is
// idempotent: re-running it against an up-to-date database does nothing.
//
// Applying it is an explicit step (`temporal-agents migrate`), not something a
// starting process does: two processes racing to apply DDL is a failure mode nobody
// can debug from a log, and a schema change is an operational decision. A starting
// process reads SchemaState instead and refuses to run against a stale database.
//
// The rules that make this safe under several workers starting at once — one
// advisory lock, one connection, each migration committing together with its
// tracking row — live in the shared runner (see pgmigrate.Apply), because the other
// Postgres adapter in this repository migrates the same database and must serialize
// with this one.
func (p *Postgres) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, p.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything. It is the read behind the explicit migrate step and
// behind the startup verification a worker and the API server run: a process that
// only verifies must not be able to apply DDL by accident.
func (p *Postgres) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, p.pool, migrationFS, migrationDir, migrationNamespace)
}

// migrationNames lists the embedded migration filenames in apply order.
func migrationNames() ([]string, error) {
	return pgmigrate.Names(migrationFS, migrationDir)
}
