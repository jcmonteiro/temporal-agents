-- A schedule reuses one workflow ID for every firing. Temporal's first run ID is
-- stable only within one firing's continue-as-new chain, so it is the durable
-- identity that lets the overview aggregate and bound schedule actions correctly.
-- Existing rows cannot recover this identity from the execution record alone. NULL
-- therefore means that the row is treated as a single-run legacy action.
ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS first_run_id text;

-- Support the schedule overview's grouping and newest-action selection without
-- scanning every execution recorded for a long-lived schedule.
CREATE INDEX IF NOT EXISTS executions_schedule_action_idx
    ON executions (schedule_id, (COALESCE(first_run_id, run_id)), started_at DESC, run_id DESC);
