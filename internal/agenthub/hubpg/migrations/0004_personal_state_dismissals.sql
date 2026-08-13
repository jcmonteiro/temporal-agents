-- A dismissal belongs to one viewer and acknowledges one exact item state. The old
-- rows cannot be assigned to an authenticated principal or to the state that was
-- reviewed, so they are cleared rather than allowed to hide new work indefinitely.
DELETE FROM dismissals;

ALTER TABLE dismissals
    ADD COLUMN IF NOT EXISTS viewer text NOT NULL DEFAULT 'local-operator',
    ADD COLUMN IF NOT EXISTS state_revision text NOT NULL DEFAULT '';

ALTER TABLE dismissals DROP CONSTRAINT IF EXISTS dismissals_pkey;
ALTER TABLE dismissals ADD PRIMARY KEY (viewer, kind, item_id);

DROP INDEX IF EXISTS dismissals_dismissed_at_idx;
CREATE INDEX dismissals_viewer_dismissed_at_idx
    ON dismissals (viewer, dismissed_at DESC);
