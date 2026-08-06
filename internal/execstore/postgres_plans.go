package execstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The plan store half of the Postgres adapter. Like the execution half it keeps
// all SQL here: callers see only the port's Plan record.

// Compile-time proof the adapter satisfies the plan port it is injected as.
var _ PlanStore = (*Postgres)(nil)

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
func (p *Postgres) SavePlan(ctx context.Context, plan Plan) error {
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
// ErrNoSuchPlan, so a caller can say "no such plan" rather than report a store
// failure — both abort, but for different reasons.
func (p *Postgres) Plan(ctx context.Context, id string) (Plan, error) {
	plan, err := scanPlan(p.pool.QueryRow(ctx, "SELECT "+planColumns+" FROM plans WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, fmt.Errorf("%w: %s", ErrNoSuchPlan, id)
	}
	if err != nil {
		return Plan{}, readError("read fleet plan "+id, err)
	}
	return plan, nil
}

// ListPlans returns the stored plans, newest first.
func (p *Postgres) ListPlans(ctx context.Context, limit int) ([]Plan, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	rows, err := p.pool.Query(ctx,
		"SELECT "+planColumns+" FROM plans ORDER BY created_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, readError("read stored fleet plans", err)
	}
	defer rows.Close()

	var out []Plan
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
func scanPlan(row pgx.Row) (Plan, error) {
	var (
		plan Plan
		name *string
	)
	if err := row.Scan(&plan.ID, &name, &plan.Goal, &plan.Nodes, &plan.Document, &plan.CreatedAt); err != nil {
		return Plan{}, err
	}
	if name != nil {
		plan.Name = *name
	}
	return plan, nil
}
