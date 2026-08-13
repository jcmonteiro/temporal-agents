package execpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"temporal-agents/internal/execstore"
)

// Compile-time proof that Postgres provides the collection-oriented read port.
var _ execstore.OverviewReader = (*Postgres)(nil)

// ListExecutionChains returns the first stable page for compatibility callers.
func (p *Postgres) ListExecutionChains(ctx context.Context, filter execstore.ChainFilter) ([]execstore.ExecutionChain, error) {
	page, err := p.ListExecutionChainPage(ctx, filter)
	return page.Items, err
}

// ListExecutionChainPage selects workflow identities before it loads their rows,
// then aggregates every iteration of each selected chain.
func (p *Postgres) ListExecutionChainPage(ctx context.Context, filter execstore.ChainFilter) (execstore.ExecutionChainPage, error) {
	args, err := chainArgs(filter)
	if err != nil {
		return execstore.ExecutionChainPage{}, err
	}
	rows, err := p.pool.Query(ctx, selectedExecutionsSQL(false), args...)
	if err != nil {
		return execstore.ExecutionChainPage{}, readError("read execution chains", err)
	}
	defer rows.Close()

	groups := map[string][]execstore.Execution{}
	var order []string
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return execstore.ExecutionChainPage{}, err
		}
		if _, known := groups[execution.WorkflowID]; !known {
			order = append(order, execution.WorkflowID)
		}
		groups[execution.WorkflowID] = append(groups[execution.WorkflowID], execution)
	}
	if err := rows.Err(); err != nil {
		return execstore.ExecutionChainPage{}, readError("read execution chains", err)
	}

	chains := make([]execstore.ExecutionChain, 0, len(order))
	for _, workflowID := range order {
		chains = append(chains, aggregateChain(groups[workflowID]))
	}
	items, next, err := chainPage(chains, filter, func(chain execstore.ExecutionChain) execstore.ExecutionChain {
		return chain
	})
	return execstore.ExecutionChainPage{Items: items, Next: next}, err
}

// ListExecutionTrees returns the first stable page for compatibility callers.
func (p *Postgres) ListExecutionTrees(ctx context.Context, filter execstore.ChainFilter) ([]execstore.ExecutionTree, error) {
	page, err := p.ListExecutionTreePage(ctx, filter)
	return page.Items, err
}

// ListExecutionTreePage selects root workflow identities before it loads every
// root iteration and direct child row for those roots.
func (p *Postgres) ListExecutionTreePage(ctx context.Context, filter execstore.ChainFilter) (execstore.ExecutionTreePage, error) {
	args, err := chainArgs(filter)
	if err != nil {
		return execstore.ExecutionTreePage{}, err
	}
	rows, err := p.pool.Query(ctx, selectedExecutionsSQL(true), args...)
	if err != nil {
		return execstore.ExecutionTreePage{}, readError("read execution trees", err)
	}
	defer rows.Close()

	trees := map[string]*execstore.ExecutionTree{}
	var order []string
	kinds := kindSet(filter.Kinds)
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return execstore.ExecutionTreePage{}, err
		}
		rootID := execution.WorkflowID
		if execution.ParentWorkflowID != "" {
			rootID = execution.ParentWorkflowID
		}
		tree, known := trees[rootID]
		if !known {
			tree = &execstore.ExecutionTree{}
			trees[rootID] = tree
			order = append(order, rootID)
		}
		tree.Executions = append(tree.Executions, execution)
		if execution.WorkflowID == rootID && kinds[execution.Kind] {
			tree.Chain = appendToChain(tree.Chain, execution)
		}
	}
	if err := rows.Err(); err != nil {
		return execstore.ExecutionTreePage{}, readError("read execution trees", err)
	}

	out := make([]execstore.ExecutionTree, 0, len(order))
	for _, rootID := range order {
		out = append(out, *trees[rootID])
	}
	items, next, err := chainPage(out, filter, func(tree execstore.ExecutionTree) execstore.ExecutionChain {
		return tree.Chain
	})
	return execstore.ExecutionTreePage{Items: items, Next: next}, err
}

// ListScheduleActionChains returns the newest bounded action chains for every
// requested schedule in one database read. The limit applies to first-run chain
// identities before all continue-as-new iterations for those actions are loaded.
func (p *Postgres) ListScheduleActionChains(ctx context.Context, scheduleIDs []string, perScheduleLimit int) (map[string][]execstore.ExecutionChain, error) {
	out := make(map[string][]execstore.ExecutionChain, len(scheduleIDs))
	if len(scheduleIDs) == 0 {
		return out, nil
	}
	limit := execstore.EffectiveLimit(perScheduleLimit, execstore.DefaultHistoryLimit)
	query := `WITH action_starts AS (
	SELECT schedule_id, COALESCE(first_run_id, run_id) AS action_id,
		MIN(started_at) AS action_started
	FROM executions
	WHERE schedule_id = ANY($1::text[])
	GROUP BY schedule_id, COALESCE(first_run_id, run_id)
), selected AS (
	SELECT schedule_id, action_id, action_started,
		row_number() OVER (
			PARTITION BY schedule_id
			ORDER BY action_started DESC, action_id DESC
		) AS action_number
	FROM action_starts
)
SELECT e.run_id, e.first_run_id, e.workflow_id, e.kind, e.prompt, e.started_at, e.ended_at,
	e.status, e.tokens, e.schedule_id, e.parent_workflow_id, e.detail
FROM executions AS e
JOIN selected
	ON selected.schedule_id = e.schedule_id
	AND selected.action_id = COALESCE(e.first_run_id, e.run_id)
WHERE selected.action_number <= $2
ORDER BY selected.schedule_id, selected.action_number, e.started_at DESC, e.run_id DESC`
	rows, err := p.pool.Query(ctx, query, scheduleIDs, limit)
	if err != nil {
		return nil, readError("read schedule action chains", err)
	}
	defer rows.Close()
	groups := make(map[string]map[string][]execstore.Execution, len(scheduleIDs))
	orders := make(map[string][]string, len(scheduleIDs))
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		actionID := executionActionID(execution)
		if groups[execution.ScheduleID] == nil {
			groups[execution.ScheduleID] = map[string][]execstore.Execution{}
		}
		if _, known := groups[execution.ScheduleID][actionID]; !known {
			orders[execution.ScheduleID] = append(orders[execution.ScheduleID], actionID)
		}
		groups[execution.ScheduleID][actionID] = append(
			groups[execution.ScheduleID][actionID], execution)
	}
	if err := rows.Err(); err != nil {
		return nil, readError("read schedule action chains", err)
	}
	for scheduleID, actionIDs := range orders {
		for _, actionID := range actionIDs {
			out[scheduleID] = append(out[scheduleID], aggregateChain(groups[scheduleID][actionID]))
		}
	}
	return out, nil
}

func selectedExecutionsSQL(tree bool) string {
	join := "e.workflow_id = selected.workflow_id"
	if tree {
		join = "e.workflow_id = selected.workflow_id OR e.parent_workflow_id = selected.workflow_id"
	}
	const qualifiedExecutionColumns = `e.run_id, e.first_run_id, e.workflow_id, e.kind, e.prompt, e.started_at, e.ended_at,
	e.status, e.tokens, e.schedule_id, e.parent_workflow_id, e.detail`
	return fmt.Sprintf(`WITH eligible AS (
	SELECT workflow_id, MIN(started_at) AS chain_started
	FROM executions
	WHERE kind = ANY($1::text[])
		AND (parent_workflow_id IS NULL OR detail @> '{"detached": true}'::jsonb)
		AND schedule_id IS NULL
		AND ($2 = '' OR workflow_id = $2)
		AND NOT (workflow_id = ANY($3::text[]))
	GROUP BY workflow_id
), page AS (
	SELECT workflow_id, chain_started
	FROM eligible
	WHERE ($6::timestamptz IS NULL OR chain_started < $6
		OR (chain_started = $6 AND workflow_id > $7))
	ORDER BY chain_started DESC, workflow_id ASC
	LIMIT ($4 + 1)
), required AS (
	SELECT workflow_id, chain_started
	FROM eligible
	WHERE workflow_id = ANY($5::text[])
		AND NOT EXISTS (
			SELECT 1 FROM page WHERE page.workflow_id = eligible.workflow_id
		)
	ORDER BY chain_started DESC, workflow_id ASC
	LIMIT $4
), selected AS (
	SELECT workflow_id, chain_started FROM page
	UNION
	SELECT workflow_id, chain_started FROM required
)
SELECT %s
FROM executions AS e
JOIN selected ON (%s)
ORDER BY selected.chain_started DESC, selected.workflow_id ASC, e.started_at DESC, e.run_id DESC`, qualifiedExecutionColumns, join)
}

type executionChainCursor struct {
	StartedAt  time.Time `json:"startedAt"`
	WorkflowID string    `json:"workflowId"`
}

func chainArgs(filter execstore.ChainFilter) ([]any, error) {
	kinds := make([]string, 0, len(filter.Kinds))
	for _, kind := range filter.Kinds {
		kinds = append(kinds, string(kind))
	}
	excluded := filter.ExcludedWorkflowIDs
	if excluded == nil {
		excluded = []string{}
	}
	required := filter.RequiredWorkflowIDs
	if required == nil {
		required = []string{}
	}
	var startedAt *time.Time
	var workflowID string
	if len(filter.Cursor) > 0 {
		var cursor executionChainCursor
		if err := json.Unmarshal(filter.Cursor, &cursor); err != nil || cursor.StartedAt.IsZero() || cursor.WorkflowID == "" {
			return nil, errors.New("invalid execution-chain cursor")
		}
		startedAt = &cursor.StartedAt
		workflowID = cursor.WorkflowID
	}
	limit := execstore.EffectiveLimit(filter.Limit, execstore.DefaultHistoryLimit)
	return []any{kinds, filter.WorkflowID, excluded, limit, required, startedAt, workflowID}, nil
}

func chainPage[T any](all []T, filter execstore.ChainFilter, chainOf func(T) execstore.ExecutionChain) ([]T, []byte, error) {
	limit := execstore.EffectiveLimit(filter.Limit, execstore.DefaultHistoryLimit)
	if len(all) <= limit {
		return all, nil, nil
	}
	items := append([]T(nil), all[:limit]...)
	required := make(map[string]bool, len(filter.RequiredWorkflowIDs))
	for _, workflowID := range filter.RequiredWorkflowIDs {
		required[workflowID] = true
	}
	for _, item := range all[limit:] {
		if len(items) == 2*limit {
			break
		}
		if required[chainOf(item).Latest.WorkflowID] {
			items = append(items, item)
		}
	}
	last := chainOf(all[limit-1])
	next, err := json.Marshal(executionChainCursor{
		StartedAt: last.StartedAt, WorkflowID: last.Latest.WorkflowID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("encode execution-chain cursor: %w", err)
	}
	return items, next, nil
}

func executionActionID(execution execstore.Execution) string {
	if execution.FirstRunID != "" {
		return execution.FirstRunID
	}
	return execution.RunID
}

func aggregateChain(executions []execstore.Execution) execstore.ExecutionChain {
	var chain execstore.ExecutionChain
	for _, execution := range executions {
		chain = appendToChain(chain, execution)
	}
	return chain
}

func appendToChain(chain execstore.ExecutionChain, execution execstore.Execution) execstore.ExecutionChain {
	chain.Iterations++
	chain.Tokens += execution.Tokens
	if chain.StartedAt.IsZero() || execution.StartedAt.Before(chain.StartedAt) {
		chain.StartedAt = execution.StartedAt
	}
	if chain.Latest.WorkflowID == "" || execution.StartedAt.After(chain.Latest.StartedAt) ||
		execution.StartedAt.Equal(chain.Latest.StartedAt) && execution.RunID > chain.Latest.RunID {
		older := chain.Latest
		chain.Latest = execution
		preserveExecutionFacts(&chain.Latest, older)
	} else {
		preserveExecutionFacts(&chain.Latest, execution)
	}
	chain.Latest.StartedAt = chain.StartedAt
	chain.Latest.Tokens = chain.Tokens
	return chain
}

func preserveExecutionFacts(target *execstore.Execution, source execstore.Execution) {
	if target.Prompt == "" {
		target.Prompt = source.Prompt
	}
	if target.ScheduleID == "" {
		target.ScheduleID = source.ScheduleID
	}
	if target.ParentWorkflowID == "" {
		target.ParentWorkflowID = source.ParentWorkflowID
	}
	if target.Detail.PlanID == "" {
		target.Detail.PlanID = source.Detail.PlanID
	}
	if len(target.Detail.Nodes) == 0 {
		target.Detail.Nodes = source.Detail.Nodes
	}
	// The place travels as a pair — the working tree and the repository it belongs to
	// — and is preserved as a pair: taking the directory from one iteration and the
	// repository from another would state a relation neither of them recorded.
	if target.Detail.Directory == "" {
		target.Detail.Directory = source.Detail.Directory
		target.Detail.Repository = source.Detail.Repository
	}
	// The instructions are resolved once per unit of work and travel across
	// continue-as-new, so they are a fact about the chain: an iteration that recorded
	// none must not hide the ones an earlier iteration ran under.
	if len(target.Detail.Instructions) == 0 {
		target.Detail.Instructions = source.Detail.Instructions
	}
}

func kindSet(kinds []execstore.Kind) map[execstore.Kind]bool {
	set := make(map[execstore.Kind]bool, len(kinds))
	for _, kind := range kinds {
		set[kind] = true
	}
	return set
}
