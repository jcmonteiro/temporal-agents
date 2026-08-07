-- The operator's dismissals: which finished items have been hidden from the
-- overview. It is view state, not work state — nothing here refers to a workflow's
-- outcome, and removing a row only makes an item visible again.
--
-- It lives in the same database as the execution record because it is the same
-- single-operator stack, but in a table of its own: the record is written by
-- workflows and is the memory of what ran, while this is written by an operator's
-- click and says nothing about what ran.
CREATE TABLE IF NOT EXISTS dismissals (
    -- The kind of item ("fleet", "run"), which is part of the identity: a fleet and
    -- a run could in principle carry the same id.
    kind         text        NOT NULL,
    -- The item's stable identity: a fleet's parent workflow id, or a run chain's
    -- workflow id. It is deliberately the *chain's* id and never a single
    -- iteration's run id, so dismissing a chained run keeps it dismissed as it
    -- continues as new.
    item_id      text        NOT NULL,
    -- When it was dismissed, so a listing can show the most recent first.
    dismissed_at timestamptz NOT NULL DEFAULT now(),
    -- Kind plus item is the identity, which is what makes the write idempotent: a
    -- client that retries a lost response upserts the same row instead of creating a
    -- second dismissal.
    PRIMARY KEY (kind, item_id)
);

-- The read path asks for every dismissal in force on each overview read and shows
-- them newest first.
CREATE INDEX IF NOT EXISTS dismissals_dismissed_at_idx ON dismissals (dismissed_at DESC);
