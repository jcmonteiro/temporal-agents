// Package hubpg is the Postgres adapter behind the Agent Hub's own durable writes:
// what the operator hid from the overview, and where the operator allows the hub to
// work.
//
// It is kept apart from the execution record's adapter on purpose. What this store
// holds is the operator's own state — which finished item has been hidden, which
// directory may be worked in — while the record is the memory of what ran; they are
// written by different actors, for different reasons, and one going down must not be
// able to corrupt the other. Keeping these writes in an adapter of their own (behind
// ports of their own) is what keeps the rest of the read path read-only by
// construction.
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
	"temporal-agents/internal/schema"
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

// Store is the driven adapter implementing the Agent Hub's write ports over
// Postgres with pgx. It is the only place in the read path where SQL appears:
// everything it exposes is the ports' plain record types.
//
// One type serves both ports because they are one schema, written by one actor,
// through one connection pool: splitting them would buy a second pool and a second
// place to keep the migration namespace right, and nothing else.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies the ports it is injected as.
var (
	_ agenthub.DismissalStore = (*Store)(nil)
	_ agenthub.PlaceStore     = (*Store)(nil)
	_ agenthub.LaunchStore    = (*Store)(nil)
)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN fails when the server starts rather than the first
// time an operator dismisses something or registers a place.
//
// The DSN is required and never logged: it commonly carries credentials, so errors
// report the failure without echoing it back.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the hub store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the hub store: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the connection pool.
func (d *Store) Close() {
	if d != nil && d.pool != nil {
		d.pool.Close()
	}
}

// Migrate brings this adapter's schema up to date, idempotently. It is applied by the
// explicit migrate step, never by a starting API server: an operator's dismissal is
// not allowed to be the thing that discovers a missing table, and a server is not
// allowed to be the thing that creates one.
func (d *Store) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, d.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything. The API server verifies it at startup and fails fast
// rather than applying DDL.
func (d *Store) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, d.pool, migrationFS, migrationDir, migrationNamespace)
}

// listDismissalsSQL reads one viewer's dismissals, newest first.
const listDismissalsSQL = `
SELECT viewer, kind, item_id, state_revision, dismissed_at
FROM dismissals
WHERE viewer = $1
ORDER BY dismissed_at DESC, kind, item_id`

// Dismissals implements agenthub.DismissalStore.
func (d *Store) Dismissals(ctx context.Context, viewer agenthub.ViewerID) ([]agenthub.Dismissal, error) {
	rows, err := d.pool.Query(ctx, listDismissalsSQL, string(viewer))
	if err != nil {
		return nil, fmt.Errorf("read the dismissals: %w", err)
	}
	dismissals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (agenthub.Dismissal, error) {
		var viewer, kind string
		var dismissal agenthub.Dismissal
		if err := row.Scan(&viewer, &kind, &dismissal.ItemID,
			&dismissal.StateRevision, &dismissal.DismissedAt); err != nil {
			return agenthub.Dismissal{}, err
		}
		dismissal.Viewer = agenthub.ViewerID(viewer)
		dismissal.Kind = agenthub.ItemKind(kind)
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
INSERT INTO dismissals (viewer, kind, item_id, state_revision, dismissed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (viewer, kind, item_id)
DO UPDATE SET
	state_revision = EXCLUDED.state_revision,
	dismissed_at = CASE
		WHEN dismissals.state_revision = EXCLUDED.state_revision THEN dismissals.dismissed_at
		ELSE EXCLUDED.dismissed_at
	END
RETURNING viewer, kind, item_id, state_revision, dismissed_at`

// Dismiss implements agenthub.DismissalStore.
func (d *Store) Dismiss(ctx context.Context, dismissal agenthub.Dismissal) (agenthub.Dismissal, error) {
	var viewer, kind string
	stored := agenthub.Dismissal{}
	err := d.pool.QueryRow(ctx, dismissSQL,
		string(dismissal.Viewer), string(dismissal.Kind), dismissal.ItemID,
		dismissal.StateRevision, dismissal.DismissedAt,
	).Scan(&viewer, &kind, &stored.ItemID, &stored.StateRevision, &stored.DismissedAt)
	if err != nil {
		return agenthub.Dismissal{}, fmt.Errorf("record the dismissal: %w", err)
	}
	stored.Viewer = agenthub.ViewerID(viewer)
	stored.Kind = agenthub.ItemKind(kind)
	return stored, nil
}

// undismissSQL removes one dismissal.
const undismissSQL = `DELETE FROM dismissals WHERE viewer = $1 AND kind = $2 AND item_id = $3`

// Undismiss implements agenthub.DismissalStore, reporting agenthub.ErrNotFound when
// there was nothing to remove so the transport can tell a deletion apart from a
// no-op.
func (d *Store) Undismiss(ctx context.Context, viewer agenthub.ViewerID, kind agenthub.ItemKind, itemID string) error {
	tag, err := d.pool.Exec(ctx, undismissSQL, string(viewer), string(kind), itemID)
	if err != nil {
		return fmt.Errorf("remove the dismissal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return agenthub.ErrNotFound
	}
	return nil
}

// listRegisteredPlacesSQL reads every registered place, in a stable order.
const listRegisteredPlacesSQL = `
SELECT directory, repository, registered_at, registered_by
FROM registered_places
ORDER BY directory`

// Registrations implements agenthub.PlaceStore.
func (d *Store) Registrations(ctx context.Context) ([]agenthub.PlaceRegistration, error) {
	rows, err := d.pool.Query(ctx, listRegisteredPlacesSQL)
	if err != nil {
		return nil, fmt.Errorf("read the registered places: %w", err)
	}
	places, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (agenthub.PlaceRegistration, error) {
		var registration agenthub.PlaceRegistration
		if err := row.Scan(&registration.Place.Directory, &registration.Place.Repository,
			&registration.RegisteredAt, &registration.RegisteredBy); err != nil {
			return agenthub.PlaceRegistration{}, err
		}
		return registration, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the registered places: %w", err)
	}
	return places, nil
}

// registerPlaceSQL upserts on the registration's identity, keeping everything the
// first registration recorded. That is what makes registering a place twice a no-op:
// the second request describes the place that is already there, with the moment it
// was first allowed and the operator who allowed it, rather than rewriting either.
const registerPlaceSQL = `
INSERT INTO registered_places (directory, repository, registered_at, registered_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (directory)
DO UPDATE SET directory = registered_places.directory
RETURNING directory, repository, registered_at, registered_by`

// Register implements agenthub.PlaceStore.
func (d *Store) Register(ctx context.Context, registration agenthub.PlaceRegistration) (agenthub.PlaceRegistration, error) {
	var stored agenthub.PlaceRegistration
	err := d.pool.QueryRow(ctx, registerPlaceSQL,
		registration.Place.Directory, registration.Place.Repository,
		registration.RegisteredAt, registration.RegisteredBy,
	).Scan(&stored.Place.Directory, &stored.Place.Repository,
		&stored.RegisteredAt, &stored.RegisteredBy)
	if err != nil {
		return agenthub.PlaceRegistration{}, fmt.Errorf("register the place: %w", err)
	}
	return stored, nil
}

// launchSQL upserts on the request identity, keeping everything the first launch
// recorded. That is what makes a repeated request one run: the second request
// describes the launch that is already there rather than recording a second one.
const launchSQL = `
INSERT INTO launches (request_id, workflow_id, kind, directory, prompt, started_at, started_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (request_id)
DO UPDATE SET request_id = launches.request_id
RETURNING request_id, workflow_id, kind, directory, prompt, started_at, started_by`

// Launch implements agenthub.LaunchStore.
func (d *Store) Launch(ctx context.Context, launch agenthub.Launch) (agenthub.Launch, error) {
	stored, err := scanLaunch(d.pool.QueryRow(ctx, launchSQL,
		launch.RequestID, launch.WorkflowID, string(launch.Kind), launch.Place.Directory,
		launch.Prompt, launch.StartedAt, launch.StartedBy))
	if err != nil {
		return agenthub.Launch{}, fmt.Errorf("record the start: %w", err)
	}
	return stored, nil
}

// launchOfSQL reads what one request started.
const launchOfSQL = `
SELECT request_id, workflow_id, kind, directory, prompt, started_at, started_by
FROM launches
WHERE request_id = $1`

// LaunchOf implements agenthub.LaunchStore, reporting agenthub.ErrNotFound for a
// request that started nothing, so the core can tell a repeat from a first attempt.
func (d *Store) LaunchOf(ctx context.Context, requestID string) (agenthub.Launch, error) {
	launch, err := scanLaunch(d.pool.QueryRow(ctx, launchOfSQL, requestID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return agenthub.Launch{}, agenthub.ErrNotFound
	case err != nil:
		return agenthub.Launch{}, fmt.Errorf("read what this request started: %w", err)
	}
	return launch, nil
}

// launchOfRunSQL reads what started one execution.
const launchOfRunSQL = `
SELECT request_id, workflow_id, kind, directory, prompt, started_at, started_by
FROM launches
WHERE workflow_id = $1`

// LaunchOfRun implements agenthub.LaunchStore, reporting agenthub.ErrNotFound for
// work this hub did not start.
func (d *Store) LaunchOfRun(ctx context.Context, workflowID string) (agenthub.Launch, error) {
	launch, err := scanLaunch(d.pool.QueryRow(ctx, launchOfRunSQL, workflowID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return agenthub.Launch{}, agenthub.ErrNotFound
	case err != nil:
		return agenthub.Launch{}, fmt.Errorf("read what started this run: %w", err)
	}
	return launch, nil
}

// scanLaunch reads one launch row.
func scanLaunch(row pgx.Row) (agenthub.Launch, error) {
	var launch agenthub.Launch
	var kind string
	if err := row.Scan(&launch.RequestID, &launch.WorkflowID, &kind, &launch.Place.Directory,
		&launch.Prompt, &launch.StartedAt, &launch.StartedBy); err != nil {
		return agenthub.Launch{}, err
	}
	launch.Kind = agenthub.StartKind(kind)
	return launch, nil
}
