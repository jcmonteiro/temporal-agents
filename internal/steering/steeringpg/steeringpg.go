// Package steeringpg is the Postgres adapter behind the human-in-the-loop pause: the
// rounds that stopped for an operator, the conversations that may produce their
// guidance, and the decisions that ended them.
//
// Two properties of this adapter are load-bearing rather than incidental. A decision
// is written only where none is recorded yet, in one statement, so the first decision
// wins even when two browser tabs race — the rule cannot be implemented by reading
// and then writing, because that is the race itself. And a turn of the conversation
// is only ever inserted, with a per-session sequence computed under a lock, so the
// transcript is append-only and its sequence is dense and monotonic: a reader that
// has seen n can ask for what came after n and be sure it missed nothing.
package steeringpg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/pgmigrate"
	"temporal-agents/internal/place"
	"temporal-agents/internal/schema"
	"temporal-agents/internal/steering"
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
	migrationNamespace = "steering"
)

// appendLockClass is the advisory-lock class the per-session append lock is taken
// in. The two-argument form is used (class, key) so a lock taken here can never
// collide with the single-argument lock the migration step holds, nor with the
// configuration store's own class.
const appendLockClass int32 = 8_060_928

// Store is the driven adapter implementing the steering ports over one connection
// pool.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies the ports it is injected as.
var (
	_ steering.SessionStore    = (*Store)(nil)
	_ steering.SessionRecorder = (*Store)(nil)
)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN stops the process at startup rather than the first
// time a review round tries to pause.
//
// The DSN is required and never logged: it commonly carries credentials.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the steering store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the steering store: %w", err)
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
// the explicit migrate step, never by a starting worker or API server.
func (s *Store) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// SchemaState reports what this context's schema is at and what this build requires,
// without changing anything.
func (s *Store) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

// sessionColumns is the projection every session read shares, so one scanner serves
// them all.
const sessionColumns = `id, item_id, round, material, directory, repository, guidance,
	opened_at, state, choice, principal, decided_at`

// openSessionSQL records a round as waiting, and does nothing at all when the
// session is already there: the activity that calls it is replayed, and a replay
// must not reopen a session that has since been decided.
const openSessionSQL = `
INSERT INTO steering_sessions (id, item_id, round, material, directory, repository, opened_at, state)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'waiting')
ON CONFLICT (id) DO NOTHING`

// OpenSession implements steering.SessionRecorder.
func (s *Store) OpenSession(ctx context.Context, session steering.Session) (steering.Session, error) {
	if strings.TrimSpace(session.ID) == "" {
		return steering.Session{}, errors.New("a steering session needs an identity")
	}
	openedAt := session.OpenedAt
	if openedAt.IsZero() {
		openedAt = time.Now().UTC()
	}
	if _, err := s.pool.Exec(ctx, openSessionSQL, session.ID, session.ItemID, string(session.Round),
		session.Material, session.Place.Directory, session.Place.Repository, openedAt); err != nil {
		return steering.Session{}, fmt.Errorf("open the steering session: %w", err)
	}
	// The stored row is returned rather than the argument, so a caller always learns
	// the session as it really stands — including one that was opened before and has
	// already been decided.
	return s.Session(ctx, session.ID)
}

// closeSessionSQL settles a session, writing the decision only where none was
// recorded. A decision sent through the API is already the authoritative one; a
// session that was signalled directly still has to end with what it was told; and a
// settlement carrying no decision is a session whose loop is gone, which is recorded
// as abandoned rather than left waiting for an answer nobody can act on.
const closeSessionSQL = `
UPDATE steering_sessions
SET state = CASE WHEN choice <> '' OR $2 <> '' THEN 'decided' ELSE 'abandoned' END,
    choice = CASE WHEN choice = '' THEN $2 ELSE choice END,
    guidance = CASE WHEN choice = '' AND $3 <> '' THEN $3 ELSE guidance END,
    principal = CASE WHEN choice = '' THEN $4 ELSE principal END,
    decided_at = CASE WHEN choice <> '' OR $2 <> '' THEN COALESCE(decided_at, $5) ELSE decided_at END
WHERE id = $1`

// CloseSession implements steering.SessionRecorder.
func (s *Store) CloseSession(ctx context.Context, id string, decision steering.Decision, at time.Time) error {
	tag, err := s.pool.Exec(ctx, closeSessionSQL, id, string(decision.Choice),
		decision.Guidance, decision.Principal, at)
	if err != nil {
		return fmt.Errorf("settle the steering session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", steering.ErrNoSuchSession, id)
	}
	return nil
}

// waitingSessionsSQL reads the rounds still waiting, oldest first: the one that has
// been waiting longest is the one most in need of an answer.
const waitingSessionsSQL = `
SELECT ` + sessionColumns + `
FROM steering_sessions
WHERE state = 'waiting'
ORDER BY opened_at, id`

// WaitingSessions implements steering.SessionStore.
func (s *Store) WaitingSessions(ctx context.Context) ([]steering.Session, error) {
	rows, err := s.pool.Query(ctx, waitingSessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("read the waiting steering sessions: %w", err)
	}
	sessions, err := pgx.CollectRows(rows, scanSession)
	if err != nil {
		return nil, fmt.Errorf("read the waiting steering sessions: %w", err)
	}
	return sessions, nil
}

// sessionSQL reads one session by its identity.
const sessionSQL = `SELECT ` + sessionColumns + ` FROM steering_sessions WHERE id = $1`

// Session implements steering.SessionStore.
func (s *Store) Session(ctx context.Context, id string) (steering.Session, error) {
	rows, err := s.pool.Query(ctx, sessionSQL, id)
	if err != nil {
		return steering.Session{}, fmt.Errorf("read the steering session: %w", err)
	}
	sessions, err := pgx.CollectRows(rows, scanSession)
	if err != nil {
		return steering.Session{}, fmt.Errorf("read the steering session: %w", err)
	}
	if len(sessions) == 0 {
		return steering.Session{}, fmt.Errorf("%w: %s", steering.ErrNoSuchSession, id)
	}
	return sessions[0], nil
}

// messagesSQL reads a conversation from a sequence onwards, in order.
const messagesSQL = `
SELECT session_id, sequence, role, author, body, tokens, created_at
FROM steering_messages
WHERE session_id = $1 AND sequence > $2
ORDER BY sequence`

// Messages implements steering.SessionStore.
func (s *Store) Messages(ctx context.Context, id string, afterSequence int64) ([]steering.Message, error) {
	// A conversation is only meaningful with the session it belongs to, so an unknown
	// session is reported as one rather than as an empty transcript.
	if _, err := s.Session(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, messagesSQL, id, afterSequence)
	if err != nil {
		return nil, fmt.Errorf("read the steering conversation: %w", err)
	}
	messages, err := pgx.CollectRows(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("read the steering conversation: %w", err)
	}
	return messages, nil
}

// appendMessageSQL appends one turn, numbering it from the rows themselves under the
// caller's lock, so sequences stay dense and are never reused.
const appendMessageSQL = `
INSERT INTO steering_messages (session_id, sequence, role, author, body, tokens, created_at)
SELECT $1, COALESCE(MAX(sequence), 0) + 1, $2, $3, $4, $5, $6
FROM steering_messages WHERE session_id = $1
RETURNING session_id, sequence, role, author, body, tokens, created_at`

// AppendMessage implements steering.SessionStore.
//
// The sequence is computed inside the same transaction as the insert and under a
// per-session advisory lock, because two turns appended at once would otherwise read
// the same maximum and claim the same position.
func (s *Store) AppendMessage(ctx context.Context, message steering.Message) (steering.Message, error) {
	if err := message.Validate(); err != nil {
		return steering.Message{}, err
	}
	at := message.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return steering.Message{}, fmt.Errorf("append to the steering conversation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`,
		appendLockClass, lockKey(message.SessionID)); err != nil {
		return steering.Message{}, fmt.Errorf("take the conversation's append lock: %w", err)
	}
	rows, err := tx.Query(ctx, appendMessageSQL, message.SessionID, string(message.Role),
		message.Author, message.Text, message.Tokens, at)
	if err != nil {
		return steering.Message{}, appendFailure(err)
	}
	appended, err := pgx.CollectExactlyOneRow(rows, scanMessage)
	if err != nil {
		return steering.Message{}, appendFailure(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return steering.Message{}, fmt.Errorf("append to the steering conversation: %w", err)
	}
	return appended, nil
}

// recordDecisionSQL writes a decision only where none is recorded, and returns the
// row either way.
//
// It is one statement on purpose: "read whether it was decided, then decide" is the
// race it exists to prevent, and the two tabs that produce it are ordinary use.
const recordDecisionSQL = `
WITH decided AS (
    UPDATE steering_sessions
    SET state = 'decided', choice = $2, guidance = CASE WHEN $3 <> '' THEN $3 ELSE guidance END,
        principal = $4, decided_at = $5
    WHERE id = $1 AND choice = ''
    RETURNING ` + sessionColumns + `
)
SELECT ` + sessionColumns + ` FROM decided
UNION ALL
SELECT ` + sessionColumns + ` FROM steering_sessions
WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM decided)`

// RecordDecision implements steering.SessionStore.
func (s *Store) RecordDecision(ctx context.Context, id string, decision steering.Decision, at time.Time) (steering.Session, error) {
	rows, err := s.pool.Query(ctx, recordDecisionSQL, id, string(decision.Choice),
		decision.Guidance, decision.Principal, at)
	if err != nil {
		return steering.Session{}, fmt.Errorf("record the steering decision: %w", err)
	}
	sessions, err := pgx.CollectRows(rows, scanSession)
	if err != nil {
		return steering.Session{}, fmt.Errorf("record the steering decision: %w", err)
	}
	if len(sessions) == 0 {
		return steering.Session{}, fmt.Errorf("%w: %s", steering.ErrNoSuchSession, id)
	}
	return sessions[0], nil
}

// scanSession maps one row onto the port's session type, so the port's types are the
// only ones that leave this package.
func scanSession(row pgx.CollectableRow) (steering.Session, error) {
	var session steering.Session
	var round, state, choice string
	var decidedAt *time.Time
	var facts place.Facts
	if err := row.Scan(&session.ID, &session.ItemID, &round, &session.Material,
		&facts.Directory, &facts.Repository, &session.Guidance, &session.OpenedAt,
		&state, &choice, &session.Decision.Principal, &decidedAt); err != nil {
		return steering.Session{}, err
	}
	session.Round = steering.Round(round)
	session.State = steering.State(state)
	session.Decision.Choice = steering.Choice(choice)
	// The guidance the decision carries is the stored guidance: the text an operator
	// composed is the text the agent was handed, and keeping two copies would let
	// them disagree.
	if session.Decision.Choice == steering.ChoiceGuide {
		session.Decision.Guidance = session.Guidance
	}
	session.Place = facts
	if decidedAt != nil {
		session.DecidedAt = *decidedAt
	}
	return session, nil
}

// scanMessage maps one row onto the port's message type.
func scanMessage(row pgx.CollectableRow) (steering.Message, error) {
	var message steering.Message
	var role string
	if err := row.Scan(&message.SessionID, &message.Sequence, &role, &message.Author,
		&message.Text, &message.Tokens, &message.At); err != nil {
		return steering.Message{}, err
	}
	message.Role = steering.Role(role)
	return message, nil
}

// appendFailure names an append against a session nobody opened as exactly that: the
// foreign key is what refuses it, and a caller must be able to tell it from an
// outage.
func appendFailure(err error) error {
	if strings.Contains(err.Error(), "steering_messages_session_id_fkey") {
		return fmt.Errorf("%w: the conversation has no session", steering.ErrNoSuchSession)
	}
	return fmt.Errorf("append to the steering conversation: %w", err)
}

// lockKey maps a session onto the second half of the advisory lock. A collision
// between two sessions would only serialize two appends that could have run at once,
// so a fast non-cryptographic hash is the right tool.
func lockKey(sessionID string) int32 {
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(sessionID))
	return int32(digest.Sum32())
}
