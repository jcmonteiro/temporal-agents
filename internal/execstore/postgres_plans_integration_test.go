package execstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The plan store is exercised against a real Postgres for the same reason the
// execution store is: the jsonb document round-trip and the upsert's conflict
// handling are properties of the database, not of the Go code. See
// postgres_integration_test.go for how to run these.

// newTestPlanStore opens the test database, applies the schema, and empties the
// plans table so each test starts from a known state.
func newTestPlanStore(t *testing.T) *Postgres {
	t.Helper()
	store := newTestStore(t)
	_, err := store.pool.Exec(context.Background(), "TRUNCATE plans")
	require.NoError(t, err)
	return store
}

func TestPostgres_RoundTripsAPlanByHandle(t *testing.T) {
	store := newTestPlanStore(t)
	ctx := context.Background()
	want := Plan{
		ID:        "plan-abcd1234efgh",
		Name:      "tenancy",
		Goal:      "add multi-tenant support",
		Nodes:     2,
		Document:  []byte(`{"goal":"add multi-tenant support","nodes":[{"id":"core"},{"id":"rest"}]}`),
		CreatedAt: stamp,
	}

	require.NoError(t, store.SavePlan(ctx, want))
	got, err := store.Plan(ctx, want.ID)

	require.NoError(t, err)
	got.CreatedAt = got.CreatedAt.UTC()
	require.Equal(t, want.ID, got.ID)
	require.Equal(t, want.Name, got.Name)
	require.Equal(t, want.Goal, got.Goal)
	require.Equal(t, want.Nodes, got.Nodes)
	require.Equal(t, want.CreatedAt, got.CreatedAt)
	require.JSONEq(t, string(want.Document), string(got.Document))
}

func TestPostgres_PlanWithoutAName(t *testing.T) {
	store := newTestPlanStore(t)
	ctx := context.Background()

	require.NoError(t, store.SavePlan(ctx, Plan{
		ID: "plan-1", Goal: "a goal", Document: []byte(`{"goal":"a goal"}`)}))
	got, err := store.Plan(ctx, "plan-1")

	require.NoError(t, err)
	require.Empty(t, got.Name, "an absent name round-trips as empty, not as an error")
	require.False(t, got.CreatedAt.IsZero(), "the store stamps a plan that arrives without a time")
}

func TestPostgres_SavePlanUpsertsOnHandleAndKeepsTheOriginalTime(t *testing.T) {
	store := newTestPlanStore(t)
	ctx := context.Background()
	plan := Plan{ID: "plan-1", Goal: "first", Nodes: 1,
		Document: []byte(`{"goal":"first"}`), CreatedAt: stamp}
	require.NoError(t, store.SavePlan(ctx, plan))

	// The write is driven from a retryable activity, so a re-run must overwrite the
	// plan under the same handle rather than duplicate it or restamp it.
	updated := plan
	updated.Goal = "second"
	updated.Document = []byte(`{"goal":"second"}`)
	updated.CreatedAt = stamp.Add(time.Hour)
	require.NoError(t, store.SavePlan(ctx, updated))

	plans, err := store.ListPlans(ctx, 0)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, "second", plans[0].Goal)
	require.Equal(t, stamp, plans[0].CreatedAt.UTC())
}

func TestPostgres_PlanUnknownHandle(t *testing.T) {
	store := newTestPlanStore(t)

	_, err := store.Plan(context.Background(), "plan-nope")

	// "No such plan" must be distinguishable from a store failure: both abort, but
	// the operator needs to know which happened.
	require.ErrorIs(t, err, ErrNoSuchPlan)
}

func TestPostgres_ListPlansNewestFirst(t *testing.T) {
	store := newTestPlanStore(t)
	ctx := context.Background()
	for i, id := range []string{"plan-1", "plan-2", "plan-3"} {
		require.NoError(t, store.SavePlan(ctx, Plan{
			ID: id, Goal: "goal", Document: []byte(`{}`),
			CreatedAt: stamp.Add(time.Duration(i) * time.Minute)}))
	}

	plans, err := store.ListPlans(ctx, 2)

	require.NoError(t, err)
	require.Len(t, plans, 2)
	require.Equal(t, []string{"plan-3", "plan-2"}, []string{plans[0].ID, plans[1].ID})
}
