// Package pgmigrate applies a set of embedded SQL migrations to a Postgres
// database, idempotently and safely when several processes start at once.
//
// It is infrastructure shared by the Postgres adapters in this repository: each one
// embeds its own migrations and applies them through here, so the rules that make
// migrating safe (one advisory lock, one tracking table, a migration and its
// tracking row committing together) are stated once instead of once per adapter.
// Adapters keep their SQL, and therefore their schema ownership, to themselves: all
// this package knows is a filesystem of numbered files and the namespace to record
// them under.
package pgmigrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaMigrationsDDL creates the tracking table that makes applying migrations
// idempotent. It is written to run before every migration, so a fresh database and
// an up-to-date one take the same path.
//
// One table serves every adapter: they share a database, and a single record of
// "what has been applied here" is easier to reason about than one table per
// adapter. Names are namespaced (see Apply), so two adapters cannot collide on a
// filename.
const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	name       text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// lockID identifies the session-level advisory lock that serializes migrating. The
// value is arbitrary but must never change: it is the name every process agrees on.
// It is shared across adapters on purpose — they migrate the same database, so they
// must serialize with each other and not merely with their own kind.
const lockID int64 = 8_060_926_014_071_701

// unlockTimeout bounds cleanup after migration succeeds, fails, or is canceled.
const unlockTimeout = 5 * time.Second

// Apply applies every embedded migration under dir that has not been applied yet,
// in filename order, and records each in the tracking table. It is idempotent:
// re-running it against an up-to-date database does nothing, so a process can
// simply call it at startup.
//
// namespace prefixes the recorded name ("<namespace>/<file>"), which is what lets
// two adapters number their files from 0001 independently. An empty namespace
// records the bare filename, so an adapter that migrated before namespacing existed
// keeps recognising its already-applied migrations.
//
// The whole of it runs under an advisory lock, so two processes starting together
// serialize rather than race. The per-migration transactions below would already
// settle who applies a migration body, but CREATE TABLE IF NOT EXISTS is not itself
// concurrency-safe in Postgres: two sessions can both pass the existence check and
// one then fails with a duplicate-key error on the system catalog. Since a caller
// typically treats a failed migration as fatal, that race would stop a process from
// starting.
//
// Each migration then runs in its own transaction together with its tracking-table
// insert, so a migration and the record that it ran commit or roll back as one — a
// crash mid-way can never leave the schema half-applied but marked as done. The
// transactions run on the same pinned connection that holds the lock, so migrating
// needs exactly one connection: taking a second one from the pool would deadlock a
// caller whose DSN caps the pool at one (pool_max_conns=1).
func Apply(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir, namespace string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection to migrate on: %w", err)
	}
	// The advisory lock is session-scoped, so it must be taken and released on one
	// pinned connection rather than on the pool.
	locked := false
	defer func() { releaseConnection(ctx, conn, locked) }()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("take the migration lock: %w", err)
	}
	locked = true

	if _, err := conn.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	names, err := Names(fsys, dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, conn, recordedName(namespace, name), string(body)); err != nil {
			return err
		}
	}
	return nil
}

// releaseConnection removes the session lock with a cleanup context that survives
// caller cancellation. A connection that cannot prove it unlocked is closed rather
// than returned to the pool with a lock still attached to its session.
func releaseConnection(ctx context.Context, conn *pgxpool.Conn, locked bool) {
	if !locked {
		conn.Release()
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRow(cleanupCtx, `SELECT pg_advisory_unlock($1)`, lockID).Scan(&unlocked); err == nil && unlocked {
		conn.Release()
		return
	}
	raw := conn.Hijack()
	_ = raw.Close(cleanupCtx)
}

// recordedName is how a migration is tracked: namespaced when the caller gave a
// namespace, and the bare filename otherwise.
func recordedName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// applyMigration runs one migration unless it is already recorded as applied, on
// the caller's already-acquired connection (see Apply: the whole operation must need
// only the one).
//
// It is a free function, not a method: everything it touches arrives in its
// parameters, so it cannot reach the pool behind the caller's back — which is
// exactly the property the one-connection rule depends on.
//
// The "already applied?" check runs inside the transaction that applies it, so two
// processes starting at once cannot both apply the same migration: the second blocks
// on the first's row lock and then sees it recorded.
func applyMigration(ctx context.Context, conn *pgxpool.Conn, name, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ON CONFLICT DO NOTHING plus RETURNING yields no rows when the migration was
	// already applied, which is the signal to skip its body.
	rows, err := tx.Query(ctx,
		`INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING RETURNING name`, name)
	if err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if len(applied) == 0 {
		// Nothing to do: another process already applied this migration. The deferred
		// rollback closes the transaction, and its error is deliberately not returned —
		// failing the migration (and with it a startup) over a rollback of a correctly
		// skipped migration would turn a success into an outage.
		return nil
	}

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// Names lists the migration filenames under dir in the order they must be applied.
// Filenames are numbered ("0001_…"), so lexical order is apply order.
func Names(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
