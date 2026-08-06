package execstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
)

// migrationFS holds the schema as embedded SQL files, applied in filename order.
// Embedding them keeps the schema in the binary, so bringing the stack up needs
// no separate migrate binary, no manual psql step, and no files on the host.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// schemaMigrationsDDL creates the tracking table that makes applying migrations
// idempotent. It is written to run before every migration, so a fresh database
// and an up-to-date one take the same path.
const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	name       text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// Migrate applies every embedded migration that has not been applied yet, in
// filename order, and records each in the schema_migrations table. It is
// idempotent: re-running it against an up-to-date database does nothing, so the
// worker can simply call it at startup.
//
// Each migration runs in its own transaction together with its tracking-table
// insert, so a migration and the record that it ran commit or roll back as one —
// a crash mid-way can never leave the schema half-applied but marked as done.
func (p *Postgres) Migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := p.applyMigration(ctx, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration unless it is already recorded as applied.
// The "already applied?" check runs inside the transaction that applies it, so
// two workers starting at once cannot both apply the same migration: the second
// blocks on the first's row lock and then sees it recorded.
func (p *Postgres) applyMigration(ctx context.Context, name, body string) error {
	tx, err := p.pool.Begin(ctx)
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
		return tx.Rollback(ctx)
	}

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// migrationNames lists the embedded migration filenames in the order they must
// be applied. Filenames are numbered ("0001_…"), so lexical order is apply
// order.
func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
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
