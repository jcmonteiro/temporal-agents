package execpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"temporal-agents/internal/execstore"
)

// The plan store half of the Postgres adapter. Like the execution half it keeps
// all SQL here: callers see only the port's Plan record.

// Compile-time proof the adapter satisfies the plan port it is injected as.
var _ execstore.PlanStore = (*Postgres)(nil)

// savePlanSQL upserts a stored plan by handle. It is driven from a retryable
// activity, so a re-run must overwrite rather than duplicate. created_at is left
// alone on conflict, so a retry does not restamp the plan.
const savePlanSQL = `
INSERT INTO plans (id, name, goal, node_count, document, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET
	name       = EXCLUDED.name,
	goal       = EXCLUDED.goal,
	node_count = EXCLUDED.node_count,
	document   = EXCLUDED.document`

// SavePlan inserts or updates the plan under plan.ID.
//
// The document budget is checked here as well as by the calling activity (which
// refuses an oversized plan non-retryably, with the wording an operator reads): the
// budget belongs to the port, so any consumer of it is held to it.
func (p *Postgres) SavePlan(ctx context.Context, plan execstore.Plan) error {
	if len(plan.Document) > execstore.MaxPlanDocument {
		return fmt.Errorf("store fleet plan %s: document is %d bytes, over the %d-byte limit",
			plan.ID, len(plan.Document), execstore.MaxPlanDocument)
	}
	created := plan.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if _, err := p.pool.Exec(ctx, savePlanSQL,
		plan.ID, nullString(plan.Name), plan.Goal, plan.Nodes, plan.Document, created); err != nil {
		return fmt.Errorf("store fleet plan %s: %w", plan.ID, err)
	}
	return nil
}

// planColumns is the shared SELECT list, in the order scanPlan reads.
const planColumns = `id, name, goal, node_count, document, created_at`

// Plan resolves a plan by its handle. A handle matching nothing returns
// execstore.ErrNoSuchPlan, so a caller can say "no such plan" rather than report a
// store failure — both abort, but for different reasons.
func (p *Postgres) Plan(ctx context.Context, id string) (execstore.Plan, error) {
	plan, err := scanPlan(p.pool.QueryRow(ctx, "SELECT "+planColumns+" FROM plans WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return execstore.Plan{}, fmt.Errorf("%w: %s", execstore.ErrNoSuchPlan, id)
	}
	if err != nil {
		return execstore.Plan{}, readError("read fleet plan "+id, err)
	}
	return plan, nil
}

// Plans resolves all existing handles in one read, keyed by handle.
func (p *Postgres) Plans(ctx context.Context, ids []string) (map[string]execstore.Plan, error) {
	out := make(map[string]execstore.Plan, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.pool.Query(ctx, "SELECT "+planColumns+" FROM plans WHERE id = ANY($1::text[])", ids)
	if err != nil {
		return nil, readError("read fleet plans", err)
	}
	defer rows.Close()
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, readError("read fleet plans", err)
		}
		out[plan.ID] = plan
	}
	if err := rows.Err(); err != nil {
		return nil, readError("read fleet plans", err)
	}
	return out, nil
}

// ListPlans returns the stored plans, newest first.
func (p *Postgres) ListPlans(ctx context.Context, limit int) ([]execstore.Plan, error) {
	// The port's own limit rule (default and cap alike), so the protection does not
	// depend on which caller asked.
	limit = execstore.EffectiveLimit(limit, execstore.DefaultPlanLimit)
	rows, err := p.pool.Query(ctx,
		"SELECT "+planColumns+" FROM plans ORDER BY created_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, readError("read stored fleet plans", err)
	}
	defer rows.Close()

	// At most limit rows come back, so the slice is allocated once.
	out := make([]execstore.Plan, 0, limit)
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, readError("read stored fleet plans", err)
		}
		out = append(out, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, readError("read stored fleet plans", err)
	}
	return out, nil
}

// scanPlan reads one plans row into the port's record type, translating the
// nullable name into its zero-value equivalent.
func scanPlan(row pgx.Row) (execstore.Plan, error) {
	var (
		plan execstore.Plan
		name *string
	)
	if err := row.Scan(&plan.ID, &name, &plan.Goal, &plan.Nodes, &plan.Document, &plan.CreatedAt); err != nil {
		return execstore.Plan{}, err
	}
	if name != nil {
		plan.Name = *name
	}
	return plan, nil
}
