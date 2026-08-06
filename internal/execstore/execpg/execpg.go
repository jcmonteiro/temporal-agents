// Package execpg is the Postgres adapter behind the execstore ports: it is the
// only package that depends on pgx and the only one that holds SQL, so the
// dependency direction of the hexagon is enforced by the compiler rather than by
// convention. Only main imports it, to wire the adapter into the worker and the
// store-backed CLI commands.
package execpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/execstore"
)

// Postgres is the driven adapter implementing the execstore ports (both halves of
// execution recording plus the plan store) over Postgres with pgx. It is the only place SQL and pgx
// types appear: everything it exposes is the port's plain record types, so no
// driver detail reaches workflow or domain code.
type Postgres struct {
	pool *pgxpool.Pool
}

// Compile-time proof the adapter satisfies both halves of the execution port it is
// injected as: the worker's activities take the writer, the CLI takes the reader.
var (
	_ execstore.ExecutionWriter = (*Postgres)(nil)
	_ execstore.ExecutionReader = (*Postgres)(nil)
)

// Open connects to the Postgres instance at dsn and verifies the connection is
// usable, so a misconfigured DSN fails at startup rather than on the first
// write. It does not apply migrations: the worker calls Migrate, while the CLI
// (a reader) opens without touching the schema.
//
// The DSN is required and never logged: it commonly carries credentials, so
// errors report the failure without echoing it back.
func Open(ctx context.Context, dsn string) (*Postgres, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Deliberately not wrapping with the DSN: it may embed a password.
		return nil, fmt.Errorf("configure the execution store connection: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the execution store: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() {
	if p != nil && p.pool != nil {
		p.pool.Close()
	}
}

// saveExecutionSQL upserts a record keyed on the Temporal run ID. The upsert is
// what makes a Persist activity safe to retry: Temporal may re-run an activity
// whose result was lost after it had already committed, and a start write may
// legitimately land after a terminal one on a retry, so the update never lowers
// a settled row back to running (see the WHERE clause).
const saveExecutionSQL = `
INSERT INTO executions (
	run_id, workflow_id, kind, prompt, started_at, ended_at,
	status, tokens, schedule_id, parent_workflow_id, detail
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (run_id) DO UPDATE SET
	workflow_id        = EXCLUDED.workflow_id,
	kind               = EXCLUDED.kind,
	prompt             = EXCLUDED.prompt,
	started_at         = EXCLUDED.started_at,
	ended_at           = EXCLUDED.ended_at,
	status             = EXCLUDED.status,
	tokens             = EXCLUDED.tokens,
	schedule_id        = EXCLUDED.schedule_id,
	parent_workflow_id = EXCLUDED.parent_workflow_id,
	detail             = EXCLUDED.detail
WHERE NOT (executions.status <> 'running' AND EXCLUDED.status = 'running')`

// SaveExecution inserts or updates the record for e.RunID, idempotently (see
// saveExecutionSQL).
func (p *Postgres) SaveExecution(ctx context.Context, e execstore.Execution) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return fmt.Errorf("encode execution detail: %w", err)
	}
	_, err = p.pool.Exec(ctx, saveExecutionSQL,
		e.RunID, e.WorkflowID, string(e.Kind), e.Prompt, e.StartedAt,
		nullTime(e.EndedAt), string(e.Status), e.Tokens,
		nullString(e.ScheduleID), nullString(e.ParentWorkflowID), detail)
	if err != nil {
		return fmt.Errorf("record execution %s: %w", e.WorkflowID, err)
	}
	return nil
}

// executionColumns is the shared SELECT list, in the order scanExecution reads.
const executionColumns = `run_id, workflow_id, kind, prompt, started_at, ended_at,
	status, tokens, schedule_id, parent_workflow_id, detail`

// ListExecutions returns the records matching f, newest first. Ties on
// started_at are broken by run ID so paging and output stay stable.
func (p *Postgres) ListExecutions(ctx context.Context, f execstore.Filter) ([]execstore.Execution, error) {
	where, args := buildFilter(f)
	limit := f.Limit
	if limit <= 0 {
		limit = execstore.DefaultHistoryLimit
	}
	args = append(args, limit)
	query := "SELECT " + executionColumns + " FROM executions" + where +
		" ORDER BY started_at DESC, run_id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, readError("read execution history", err)
	}
	defer rows.Close()

	var out []execstore.Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, readError("read execution history", err)
	}
	return out, nil
}

// undefinedTable is the Postgres error code for "relation does not exist".
const undefinedTable = "42P01"

// readError wraps a read failure, translating a missing table into
// execstore.ErrNotMigrated, so a reader that runs before any worker has applied
// the schema gets an actionable message instead of raw SQL wording.
func readError(what string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == undefinedTable {
		return execstore.ErrNotMigrated
	}
	return fmt.Errorf("%s: %w", what, err)
}

// filterPlaceholder marks where a clause takes its argument. It is deliberately
// not "?": Postgres reads "?" as the jsonb existence operator (detail ? 'nodes'),
// so a future clause using that operator would have it silently rewritten into a
// positional parameter and produce a mangled query rather than a compile error.
// "{}" cannot occur in SQL, so no clause can contain one by accident.
const filterPlaceholder = "{}"

// buildFilter renders f as a WHERE clause plus its positional arguments. An
// empty filter yields no clause at all.
func buildFilter(f execstore.Filter) (string, []any) {
	var clauses []string
	var args []any
	// add takes exactly one argument, however many placeholders the clause has:
	// every filterPlaceholder in the clause is rewritten to the *same* positional
	// parameter. That is what lets "(workflow_id = {} OR parent_workflow_id = {})"
	// match one value in two columns. A clause needing two distinct values must be
	// added as two calls.
	add := func(clause string, arg any) {
		args = append(args, arg)
		clauses = append(clauses, strings.ReplaceAll(clause, filterPlaceholder, "$"+strconv.Itoa(len(args))))
	}
	if f.Kind != "" {
		add("kind = {}", string(f.Kind))
	}
	if f.WorkflowID != "" {
		// One execution and its children: the row(s) under that workflow ID (every
		// continue-as-new iteration) plus every child that recorded it as its parent.
		add("(workflow_id = {} OR parent_workflow_id = {})", f.WorkflowID)
	}
	if f.ScheduleID != "" {
		add("schedule_id = {}", f.ScheduleID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// scanExecution reads one row into the port's record type, translating the
// nullable columns into their zero-value equivalents.
func scanExecution(row pgx.Row) (execstore.Execution, error) {
	var (
		e      execstore.Execution
		kind   string
		status string
		ended  *time.Time
		sched  *string
		parent *string
		detail []byte
	)
	if err := row.Scan(&e.RunID, &e.WorkflowID, &kind, &e.Prompt, &e.StartedAt,
		&ended, &status, &e.Tokens, &sched, &parent, &detail); err != nil {
		return execstore.Execution{}, fmt.Errorf("read execution row: %w", err)
	}
	e.Kind = execstore.Kind(kind)
	e.Status = execstore.Status(status)
	if ended != nil {
		e.EndedAt = *ended
	}
	if sched != nil {
		e.ScheduleID = *sched
	}
	if parent != nil {
		e.ParentWorkflowID = *parent
	}
	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &e.Detail); err != nil {
			return execstore.Execution{}, fmt.Errorf("decode execution detail: %w", err)
		}
	}
	return e, nil
}

// nullString maps an empty string to a SQL NULL, keeping "absent" distinct from
// "empty" in the nullable columns.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullTime maps the zero time to a SQL NULL, so a still-running execution has no
// end time rather than one at year zero.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
