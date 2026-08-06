-- One table holds every recorded execution. The kind discriminator plus the
-- jsonb detail column keep the type boundary in code rather than in the schema,
-- so a new per-kind field needs no migration.
CREATE TABLE IF NOT EXISTS executions (
    -- The Temporal run ID: unique per continue-as-new iteration, so a chained
    -- run's iterations are separate rows. Every write upserts on it, which is
    -- what makes a retried Persist activity idempotent.
    run_id             text PRIMARY KEY,
    -- The Temporal workflow ID: groups a chained run's iterations and correlates
    -- a tree of executions together with parent_workflow_id.
    workflow_id        text        NOT NULL,
    kind               text        NOT NULL,
    prompt             text        NOT NULL DEFAULT '',
    started_at         timestamptz NOT NULL,
    -- NULL while the execution is still running.
    ended_at           timestamptz,
    status             text        NOT NULL,
    -- This execution's own incremental token usage, never an inclusive running
    -- total, so summing rows cannot double-count.
    tokens             bigint      NOT NULL DEFAULT 0,
    -- The schedule that fired this run, NULL when it was started directly.
    schedule_id        text,
    -- The workflow that started this one as a child, NULL for a top-level
    -- execution.
    parent_workflow_id text,
    detail             jsonb       NOT NULL DEFAULT '{}'::jsonb
);

-- history reads newest-first, optionally narrowed by kind, by workflow (a run
-- and its children) or by schedule; index each of those access paths. Ordering
-- the composite indexes by started_at DESC lets a limited query stop early
-- instead of sorting the whole table as history grows.
CREATE INDEX IF NOT EXISTS executions_started_at_idx ON executions (started_at DESC);
CREATE INDEX IF NOT EXISTS executions_kind_started_at_idx ON executions (kind, started_at DESC);
CREATE INDEX IF NOT EXISTS executions_workflow_id_idx ON executions (workflow_id, started_at DESC);
CREATE INDEX IF NOT EXISTS executions_parent_workflow_id_idx ON executions (parent_workflow_id, started_at DESC);
CREATE INDEX IF NOT EXISTS executions_schedule_id_idx ON executions (schedule_id, started_at DESC);
