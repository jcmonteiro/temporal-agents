-- ListExecutions orders by "started_at DESC, run_id DESC": the tie-break is what
-- keeps paging and printed output stable when two executions start in the same
-- microsecond. The indexes 0001 created stop at started_at, so Postgres could not
-- walk one and stop at the limit — it had to sort the matching rows to satisfy the
-- second key, which is exactly what the indexes were meant to avoid.
--
-- Recreate them with run_id DESC as a trailing column, so the index order matches
-- the query order exactly and a limited query really can stop early. The single
-- leading columns are unchanged, so every access path 0001 covered is still covered.
DROP INDEX IF EXISTS executions_started_at_idx;
DROP INDEX IF EXISTS executions_kind_started_at_idx;
DROP INDEX IF EXISTS executions_workflow_id_idx;
DROP INDEX IF EXISTS executions_parent_workflow_id_idx;
DROP INDEX IF EXISTS executions_schedule_id_idx;

CREATE INDEX IF NOT EXISTS executions_started_at_idx
    ON executions (started_at DESC, run_id DESC);
CREATE INDEX IF NOT EXISTS executions_kind_started_at_idx
    ON executions (kind, started_at DESC, run_id DESC);
CREATE INDEX IF NOT EXISTS executions_workflow_id_idx
    ON executions (workflow_id, started_at DESC, run_id DESC);
CREATE INDEX IF NOT EXISTS executions_parent_workflow_id_idx
    ON executions (parent_workflow_id, started_at DESC, run_id DESC);
CREATE INDEX IF NOT EXISTS executions_schedule_id_idx
    ON executions (schedule_id, started_at DESC, run_id DESC);
