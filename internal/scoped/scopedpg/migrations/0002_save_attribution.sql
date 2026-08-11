-- A configured override is an audited mutation. Keep the principal on the immutable
-- version row, because attribution belongs to the edit and must survive later edits
-- and resets. Factory publication and explicitly unauthenticated local deployments
-- use the empty value.
ALTER TABLE scoped_values
    ADD COLUMN IF NOT EXISTS saved_by text NOT NULL DEFAULT '';
