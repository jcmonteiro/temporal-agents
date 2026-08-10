// Package instructionpg is the Postgres adapter behind the instruction context: the
// append-only versions of every governed instruction and the pointer that says which
// version a scope currently uses.
//
// Two properties of this adapter are load-bearing rather than incidental. A version
// is only ever inserted — the adapter has no statement that updates or deletes one —
// so the text a finished run recorded stays resolvable after any later edit. And
// publishing the shipped defaults takes a lock per key and compares the text before
// writing, so two processes starting together publish one version between them
// rather than one each.
package instructionpg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/pgmigrate"
	"temporal-agents/internal/schema"
)

// migrationFS holds this context's schema as embedded SQL, so the migrate step
// carries it and no file has to be deployed beside the binary.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Where the embedded migrations live, and the namespace they are recorded under, so
// this context numbers its files from 0001 independently of every other.
const (
	migrationDir       = "migrations"
	migrationNamespace = "instruction"
)

// publishLockClass is the advisory-lock class this adapter takes its per-key locks
// in. The two-argument form is used (class, key) so a lock taken here can never
// collide with the single-argument lock the migration step holds.
const publishLockClass int32 = 8_060_927

// Store is the driven adapter implementing the instruction context's ports over one
// connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies the ports it is injected as.
var _ instruction.Store = (*Store)(nil)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN stops the worker at startup rather than the first
// time a workflow resolves an instruction.
//
// The DSN is required and never logged: it commonly carries credentials.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the instruction store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the instruction store: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Migrate brings this context's schema up to date, idempotently. It is applied by
// the explicit migrate step, never by a starting worker.
func (s *Store) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything.
func (s *Store) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// currentSQL reads the version every requested (key, scope) currently points at. It
// applies no ordering and no precedence: which of the returned rows wins is the
// core's rule, and an adapter that ranked them here would be a second place that
// decides it.
const currentSQL = `
SELECT p.key, p.scope, p.version, v.body, v.hash
FROM instruction_pointers p
JOIN instruction_versions v ON v.key = p.key AND v.scope = p.scope AND v.version = p.version
WHERE p.key = ANY($1) AND p.scope = ANY($2)`

// Current implements instruction.Reader.
func (s *Store) Current(ctx context.Context, keys []instruction.Key, scopes []instruction.Scope) ([]instruction.Record, error) {
	if len(keys) == 0 || len(scopes) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, currentSQL, texts(keys), texts(scopes))
	if err != nil {
		return nil, fmt.Errorf("read the stored instructions: %w", err)
	}
	records, err := pgx.CollectRows(rows, scanRecord)
	if err != nil {
		return nil, fmt.Errorf("read the stored instructions: %w", err)
	}
	return records, nil
}

// Version implements instruction.Reader: it recovers one stored version, which is
// how a recorded provenance becomes readable text again.
func (s *Store) Version(ctx context.Context, key instruction.Key, scope instruction.Scope, version int) (instruction.Record, error) {
	rows, err := s.pool.Query(ctx, versionSQL, string(key), string(scope), int64(version))
	if err != nil {
		return instruction.Record{}, fmt.Errorf("read the stored instruction version: %w", err)
	}
	records, err := pgx.CollectRows(rows, scanRecord)
	if err != nil {
		return instruction.Record{}, fmt.Errorf("read the stored instruction version: %w", err)
	}
	if len(records) == 0 {
		return instruction.Record{}, fmt.Errorf("%w: %s at %s v%d", instruction.ErrNoSuchVersion, key, scope, version)
	}
	return records[0], nil
}

// versionSQL reads one stored version by its identity.
const versionSQL = `
SELECT key, scope, version, body, hash
FROM instruction_versions
WHERE key = $1 AND scope = $2 AND version = $3`

// pointedAtFactorySQL reads what the factory scope currently points at, which is
// what publication compares the build's text against.
const pointedAtFactorySQL = `
SELECT p.key, p.scope, p.version, v.body, v.hash
FROM instruction_pointers p
JOIN instruction_versions v ON v.key = p.key AND v.scope = p.scope AND v.version = p.version
WHERE p.key = $1 AND p.scope = $2`

// appendVersionSQL inserts the next version of one (key, scope). The number is
// computed from the rows themselves, under the caller's lock, so versions stay dense
// and are never reused.
const appendVersionSQL = `
INSERT INTO instruction_versions (key, scope, version, body, hash)
SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4
FROM instruction_versions WHERE key = $1 AND scope = $2
RETURNING key, scope, version, body, hash`

// pointSQL moves one scope's pointer to a version, which is the only write that ever
// changes what a scope resolves to.
const pointSQL = `
INSERT INTO instruction_pointers (key, scope, version)
VALUES ($1, $2, $3)
ON CONFLICT (key, scope) DO UPDATE SET version = EXCLUDED.version, updated_at = now()`

// PublishFactory implements instruction.Publisher.
//
// It appends a version only when the shipped text differs from the one the factory
// scope already points at, so a restart adds nothing and an upgrade adds exactly
// one version. The comparison and the insert share a transaction and a per-key
// advisory lock, because two processes starting together would otherwise both read
// "nothing published" and both insert version 1.
func (s *Store) PublishFactory(ctx context.Context, key instruction.Key, text string) (instruction.Record, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return instruction.Record{}, fmt.Errorf("publish the shipped instruction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, publishLockClass, lockKey(key)); err != nil {
		return instruction.Record{}, fmt.Errorf("take the publication lock for %s: %w", key, err)
	}

	current, err := publishedFactory(ctx, tx, key)
	if err != nil {
		return instruction.Record{}, err
	}
	if current.Text == text && current.Version > 0 {
		// Already published, unchanged: publication is a no-op, which is what makes it
		// safe to run at every startup.
		return current, nil
	}

	rows, err := tx.Query(ctx, appendVersionSQL, string(key), string(instruction.FactoryScope), text, instruction.Hash(text))
	if err != nil {
		return instruction.Record{}, fmt.Errorf("append the shipped instruction for %s: %w", key, err)
	}
	appended, err := pgx.CollectExactlyOneRow(rows, scanRecord)
	if err != nil {
		return instruction.Record{}, fmt.Errorf("append the shipped instruction for %s: %w", key, err)
	}
	if _, err := tx.Exec(ctx, pointSQL, string(key), string(instruction.FactoryScope), appended.Version); err != nil {
		return instruction.Record{}, fmt.Errorf("point %s at its shipped instruction: %w", key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return instruction.Record{}, fmt.Errorf("publish the shipped instruction for %s: %w", key, err)
	}
	return appended, nil
}

// publishedFactory reads what the factory scope points at, reporting "nothing
// published yet" as the zero record rather than as an error: a fresh database is not
// a failure.
func publishedFactory(ctx context.Context, tx pgx.Tx, key instruction.Key) (instruction.Record, error) {
	rows, err := tx.Query(ctx, pointedAtFactorySQL, string(key), string(instruction.FactoryScope))
	if err != nil {
		return instruction.Record{}, fmt.Errorf("read the published instruction for %s: %w", key, err)
	}
	records, err := pgx.CollectRows(rows, scanRecord)
	if err != nil {
		return instruction.Record{}, fmt.Errorf("read the published instruction for %s: %w", key, err)
	}
	if len(records) == 0 {
		return instruction.Record{}, nil
	}
	return records[0], nil
}

// scanRecord maps one row onto the port's record type, so the port's types are the
// only ones that leave this package.
func scanRecord(row pgx.CollectableRow) (instruction.Record, error) {
	var key, scope string
	var record instruction.Record
	var version int64
	if err := row.Scan(&key, &scope, &version, &record.Text, &record.Hash); err != nil {
		return instruction.Record{}, err
	}
	record.Key = instruction.Key(key)
	record.Scope = instruction.Scope(scope)
	record.Version = int(version)
	return record, nil
}

// texts renders a slice of the port's string-like values as the plain strings the
// driver binds.
func texts[T ~string](values []T) []string {
	plain := make([]string, 0, len(values))
	for _, value := range values {
		plain = append(plain, string(value))
	}
	return plain
}

// lockKey maps an instruction key onto the second half of the advisory lock. A
// collision between two keys would only serialize two publications that could have
// run at once, so a fast non-cryptographic hash is the right tool.
func lockKey(key instruction.Key) int32 {
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(key))
	return int32(digest.Sum32())
}
