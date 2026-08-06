-- Approved fleet plans, replacing the fleet-plan.json file as the source of
-- truth. A plan is referred to by its generated handle; the executions it drove
-- carry that handle in their detail column, so a plan and its runs correlate.
CREATE TABLE IF NOT EXISTS plans (
    -- The generated handle, and the only way to resolve a plan for execution.
    id         text        PRIMARY KEY,
    -- An optional operator-chosen label. Display-only metadata: nothing makes it
    -- unique, so it deliberately cannot select a plan.
    name       text,
    -- The goal and node count are stored alongside the document so listing plans
    -- needs no decode.
    goal       text        NOT NULL,
    node_count integer     NOT NULL DEFAULT 0,
    -- The plan graph itself, kept opaque so its schema stays owned by the fleet
    -- core rather than by the database.
    document   jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Plans are listed newest first.
CREATE INDEX IF NOT EXISTS plans_created_at_idx ON plans (created_at DESC);
