// Package hubpg is the Postgres adapter behind the Agent Hub's dismissal store: the
// only durable write the read API owns.
//
// It is kept apart from the execution record's adapter on purpose. A dismissal is
// the operator's view state — which finished item has been hidden — while the record
// is the memory of what ran; they are written by different actors, for different
// reasons, and one going down must not be able to corrupt the other. Keeping the
// single write in its own adapter (behind its own port) is what keeps the rest of the
// read path read-only by construction.
package hubpg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/pgmigrate"
)

// migrationFS holds this adapter's schema as embedded SQL, so bringing the stack up
// needs no migrate binary and no psql step.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Where the embedded migrations live, and the namespace they are recorded under. The
// namespace is what lets this adapter number its files from 0001 independently of the
// execution record's, even though both are tracked in one table.
const (
	migrationDir       = "migrations"
	migrationNamespace = "agenthub"
)

// Dismissals is the driven adapter implementing agenthub.DismissalStore over
// Postgres with pgx. It is the only place in the read path where SQL appears:
// everything it exposes is the port's plain record type.
type Dismissals struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies the port it is injected as.
var _ agenthub.DismissalStore = (*Dismissals)(nil)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN fails when the server starts rather than the first
// time an operator dismisses something.
//
// The DSN is required and never logged: it commonly carries credentials, so errors
// report the failure without echoing it back.
func Open(ctx context.Context, dsn string) (*Dismissals, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the dismissal store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the dismissal store: %w", err)
	}
	return &Dismissals{pool: pool}, nil
}

// Close releases the connection pool.
func (d *Dismissals) Close() {
	if d != nil && d.pool != nil {
		d.pool.Close()
	}
}

// Migrate brings this adapter's schema up to date, idempotently. It is applied by the
// explicit migrate step, never by a starting API server: an operator's dismissal is
// not allowed to be the thing that discovers a missing table, and a server is not
// allowed to be the thing that creates one.
func (d *Dismissals) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, d.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything. The API server verifies it at startup and fails fast
// rather than applying DDL.
func (d *Dismissals) SchemaState(ctx context.Context) (pgmigrate.State, error) {
	return pgmigrate.Inspect(ctx, d.pool, migrationFS, migrationDir, migrationNamespace)
}

// listDismissalsSQL reads every dismissal in force, newest first.
const listDismissalsSQL = `
SELECT kind, item_id, dismissed_at
FROM dismissals
ORDER BY dismissed_at DESC, kind, item_id`

// Dismissals implements agenthub.DismissalStore.
func (d *Dismissals) Dismissals(ctx context.Context) ([]agenthub.Dismissal, error) {
	rows, err := d.pool.Query(ctx, listDismissalsSQL)
	if err != nil {
		return nil, fmt.Errorf("read the dismissals: %w", err)
	}
	dismissals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (agenthub.Dismissal, error) {
		var kind, itemID string
		var dismissal agenthub.Dismissal
		if err := row.Scan(&kind, &itemID, &dismissal.DismissedAt); err != nil {
			return agenthub.Dismissal{}, err
		}
		dismissal.Kind = agenthub.ItemKind(kind)
		dismissal.ItemID = itemID
		return dismissal, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the dismissals: %w", err)
	}
	return dismissals, nil
}

// dismissSQL upserts on the dismissal's identity, keeping the original time. That is
// what makes the write idempotent: a client that retries a lost response gets the
// same dismissal rather than a second one, and the moment the item was first hidden
// is not rewritten.
const dismissSQL = `
INSERT INTO dismissals (kind, item_id, dismissed_at)
VALUES ($1, $2, $3)
ON CONFLICT (kind, item_id)
DO UPDATE SET dismissed_at = dismissals.dismissed_at
RETURNING kind, item_id, dismissed_at`

// Dismiss implements agenthub.DismissalStore.
func (d *Dismissals) Dismiss(ctx context.Context, dismissal agenthub.Dismissal) (agenthub.Dismissal, error) {
	var kind string
	stored := agenthub.Dismissal{}
	err := d.pool.QueryRow(ctx, dismissSQL,
		string(dismissal.Kind), dismissal.ItemID, dismissal.DismissedAt,
	).Scan(&kind, &stored.ItemID, &stored.DismissedAt)
	if err != nil {
		return agenthub.Dismissal{}, fmt.Errorf("record the dismissal: %w", err)
	}
	stored.Kind = agenthub.ItemKind(kind)
	return stored, nil
}

// undismissSQL removes one dismissal.
const undismissSQL = `DELETE FROM dismissals WHERE kind = $1 AND item_id = $2`

// Undismiss implements agenthub.DismissalStore, reporting agenthub.ErrNotFound when
// there was nothing to remove so the transport can tell a deletion apart from a
// no-op.
func (d *Dismissals) Undismiss(ctx context.Context, kind agenthub.ItemKind, itemID string) error {
	tag, err := d.pool.Exec(ctx, undismissSQL, string(kind), itemID)
	if err != nil {
		return fmt.Errorf("remove the dismissal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return agenthub.ErrNotFound
	}
	return nil
}
