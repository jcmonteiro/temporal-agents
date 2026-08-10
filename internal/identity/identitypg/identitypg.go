// Package identitypg is the Postgres adapter behind the identity context's stores:
// the principals who have signed in, their sessions, and the sign-ins in flight.
//
// It is the identity context's own schema, migrated on its own and referenced by no
// other context, which is what lets authentication be brought up, tested and
// migrated without the execution record or the hub's view state.
//
// Two properties of this adapter are load-bearing rather than incidental. A pending
// sign-in is taken with a deleting read, so two callbacks racing on one code can
// never both be completed. And a session is found by the digest of the browser's
// token, never by the token itself, so the table is not a set of usable credentials
// even to somebody holding a dump of it.
package identitypg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/identity"
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
	migrationNamespace = "identity"
)

// Store is the driven adapter implementing the identity context's three stores over
// one connection pool. They are one adapter because they are one schema and one
// transactional boundary; they stay three ports because the core has three separate
// reasons to write.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies every port it is injected as.
var (
	_ identity.SessionStore       = (*Store)(nil)
	_ identity.PrincipalStore     = (*Store)(nil)
	_ identity.PendingSignInStore = (*Store)(nil)
)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN stops the server at startup rather than the first
// time somebody tries to sign in.
//
// The DSN is required and never logged: it commonly carries credentials.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the identity store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the identity store: %w", err)
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
// the explicit migrate step, never by a starting server: a sign-in must not be the
// thing that discovers a missing table.
func (s *Store) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything.
func (s *Store) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// upsertPrincipalSQL records a principal, refreshing what the provider disclosed
// this time and keeping when the principal was first seen.
const upsertPrincipalSQL = `
INSERT INTO identity_principals (issuer, subject, display_name, email, first_seen_at, last_seen_at)
VALUES ($1, $2, $3, $4, now(), now())
ON CONFLICT (issuer, subject) DO UPDATE
SET display_name = EXCLUDED.display_name,
    email        = EXCLUDED.email,
    last_seen_at = now()`

// UpsertPrincipal implements identity.PrincipalStore.
func (s *Store) UpsertPrincipal(ctx context.Context, principal identity.Principal) error {
	if !principal.Valid() {
		return errors.New("a principal needs both an issuer and a subject")
	}
	_, err := s.pool.Exec(ctx, upsertPrincipalSQL,
		principal.Issuer, principal.Subject, principal.Name, principal.Email)
	if err != nil {
		return fmt.Errorf("record the principal: %w", err)
	}
	return nil
}

// readPrincipalSQL reads one principal by its identity.
const readPrincipalSQL = `
SELECT issuer, subject, display_name, email
FROM identity_principals
WHERE issuer = $1 AND subject = $2`

// Principal implements identity.PrincipalStore.
func (s *Store) Principal(ctx context.Context, issuer, subject string) (identity.Principal, error) {
	var principal identity.Principal
	err := s.pool.QueryRow(ctx, readPrincipalSQL, issuer, subject).Scan(
		&principal.Issuer, &principal.Subject, &principal.Name, &principal.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, identity.ErrNoPrincipal
	}
	if err != nil {
		return identity.Principal{}, fmt.Errorf("read the principal: %w", err)
	}
	return principal, nil
}

// createSessionSQL stores a session. The principal is upserted alongside it in one
// transaction (see CreateSession), so the session can never reference an identity
// the database does not have.
const createSessionSQL = `
INSERT INTO identity_sessions (
    token_hash, issuer, subject, issued_at, expires_at,
    access_token, refresh_token, token_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// CreateSession implements identity.SessionStore.
func (s *Store) CreateSession(ctx context.Context, session identity.Session) error {
	if len(session.TokenHash) == 0 {
		return errors.New("a session needs a token hash")
	}
	if !session.Principal.Valid() {
		return errors.New("a session needs a principal")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("open the session write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The principal is written first and in the same transaction, because the session
	// references it: a sign-in is one fact, and half of it recorded is a session that
	// cannot be read back.
	if _, err := tx.Exec(ctx, upsertPrincipalSQL,
		session.Principal.Issuer, session.Principal.Subject,
		session.Principal.Name, session.Principal.Email); err != nil {
		return fmt.Errorf("record the principal: %w", err)
	}
	if _, err := tx.Exec(ctx, createSessionSQL,
		session.TokenHash, session.Principal.Issuer, session.Principal.Subject,
		session.IssuedAt, session.ExpiresAt,
		session.Tokens.Access, session.Tokens.Refresh, nullableTime(session.Tokens.ExpiresAt),
	); err != nil {
		return fmt.Errorf("record the session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit the session: %w", err)
	}
	return nil
}

// readSessionSQL reads a session with the principal's display fields, so resolving a
// credential is one round trip rather than two.
const readSessionSQL = `
SELECT s.token_hash, s.issued_at, s.expires_at,
       s.access_token, s.refresh_token, s.token_expires_at,
       p.issuer, p.subject, p.display_name, p.email
FROM identity_sessions s
JOIN identity_principals p ON p.issuer = s.issuer AND p.subject = s.subject
WHERE s.token_hash = $1`

// Session implements identity.SessionStore.
func (s *Store) Session(ctx context.Context, tokenHash []byte) (identity.Session, error) {
	if len(tokenHash) == 0 {
		return identity.Session{}, identity.ErrNoSession
	}
	var (
		session   identity.Session
		expiresAt *time.Time
	)
	err := s.pool.QueryRow(ctx, readSessionSQL, tokenHash).Scan(
		&session.TokenHash, &session.IssuedAt, &session.ExpiresAt,
		&session.Tokens.Access, &session.Tokens.Refresh, &expiresAt,
		&session.Principal.Issuer, &session.Principal.Subject,
		&session.Principal.Name, &session.Principal.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Session{}, identity.ErrNoSession
	}
	if err != nil {
		return identity.Session{}, fmt.Errorf("read the session: %w", err)
	}
	if expiresAt != nil {
		session.Tokens.ExpiresAt = *expiresAt
	}
	return session, nil
}

// updateSessionTokensSQL replaces the provider tokens after a refresh, and nothing
// else: a refresh renews what the provider stands behind, it does not extend the
// session's own lifetime.
const updateSessionTokensSQL = `
UPDATE identity_sessions
SET access_token = $2, refresh_token = $3, token_expires_at = $4
WHERE token_hash = $1`

// UpdateSessionTokens implements identity.SessionStore.
func (s *Store) UpdateSessionTokens(ctx context.Context, tokenHash []byte, tokens identity.Tokens) error {
	tag, err := s.pool.Exec(ctx, updateSessionTokensSQL,
		tokenHash, tokens.Access, tokens.Refresh, nullableTime(tokens.ExpiresAt))
	if err != nil {
		return fmt.Errorf("renew the session's tokens: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNoSession
	}
	return nil
}

// endSessionSQL removes one session. Removal, not a flag: an ended session must be
// unreadable on the very next request, and a row that is still there is a row some
// future query can forget to filter.
const endSessionSQL = `DELETE FROM identity_sessions WHERE token_hash = $1`

// EndSession implements identity.SessionStore.
func (s *Store) EndSession(ctx context.Context, tokenHash []byte) error {
	tag, err := s.pool.Exec(ctx, endSessionSQL, tokenHash)
	if err != nil {
		return fmt.Errorf("end the session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNoSession
	}
	return nil
}

// deleteExpiredSessionsSQL sweeps sessions that ended by themselves.
const deleteExpiredSessionsSQL = `DELETE FROM identity_sessions WHERE expires_at <= $1`

// DeleteExpiredSessions implements identity.SessionStore.
func (s *Store) DeleteExpiredSessions(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, deleteExpiredSessionsSQL, before)
	if err != nil {
		return 0, fmt.Errorf("sweep the expired sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// startSignInSQL stores a pending sign-in.
const startSignInSQL = `
INSERT INTO identity_sign_ins (
    token_hash, state, nonce, code_verifier, return_to, started_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

// StartSignIn implements identity.PendingSignInStore.
func (s *Store) StartSignIn(ctx context.Context, pending identity.PendingSignIn) error {
	if len(pending.TokenHash) == 0 {
		return errors.New("a pending sign-in needs a token hash")
	}
	_, err := s.pool.Exec(ctx, startSignInSQL,
		pending.TokenHash, pending.State, pending.Nonce, pending.CodeVerifier,
		pending.ReturnTo, pending.StartedAt, pending.ExpiresAt)
	if err != nil {
		return fmt.Errorf("start the sign-in: %w", err)
	}
	return nil
}

// takePendingSignInSQL reads and removes a pending sign-in in one statement.
//
// The deleting read is the replay protection, and it has to be one statement:
// SELECT-then-DELETE would let two callbacks with the same code both read the row
// before either removed it, and both would then be completed.
const takePendingSignInSQL = `
DELETE FROM identity_sign_ins
WHERE token_hash = $1
RETURNING token_hash, state, nonce, code_verifier, return_to, started_at, expires_at`

// TakePendingSignIn implements identity.PendingSignInStore.
func (s *Store) TakePendingSignIn(ctx context.Context, tokenHash []byte) (identity.PendingSignIn, error) {
	if len(tokenHash) == 0 {
		return identity.PendingSignIn{}, identity.ErrNoPendingSignIn
	}
	var pending identity.PendingSignIn
	err := s.pool.QueryRow(ctx, takePendingSignInSQL, tokenHash).Scan(
		&pending.TokenHash, &pending.State, &pending.Nonce, &pending.CodeVerifier,
		&pending.ReturnTo, &pending.StartedAt, &pending.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.PendingSignIn{}, identity.ErrNoPendingSignIn
	}
	if err != nil {
		return identity.PendingSignIn{}, fmt.Errorf("take the pending sign-in: %w", err)
	}
	return pending, nil
}

// deleteExpiredSignInsSQL sweeps sign-ins nobody finished.
const deleteExpiredSignInsSQL = `DELETE FROM identity_sign_ins WHERE expires_at <= $1`

// DeleteExpiredSignIns implements identity.PendingSignInStore.
func (s *Store) DeleteExpiredSignIns(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, deleteExpiredSignInsSQL, before)
	if err != nil {
		return 0, fmt.Errorf("sweep the abandoned sign-ins: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// nullableTime writes a zero time as SQL NULL, so "the provider did not say when
// this expires" is stored as the absence of a value rather than as the year 1.
func nullableTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}
