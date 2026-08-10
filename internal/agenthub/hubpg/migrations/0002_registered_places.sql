-- The places an operator registered: the directories the hub is allowed to work in.
--
-- A place with work in it needs no row here — it is observed from what ran — so this
-- table answers the one question observation cannot: which places exist before any
-- work has ever run in them. Nothing here is a workflow's state, and removing a row
-- only makes the hub forget that it may work somewhere.
CREATE TABLE IF NOT EXISTS registered_places (
    -- The working tree the probe named, absolute and cleaned. It is the identity: an
    -- operator who names a subdirectory registers the working tree that holds it, so
    -- naming the same place in two ways registers it once, and a retried request
    -- upserts the same row instead of creating a second place.
    directory     text        NOT NULL PRIMARY KEY,
    -- The repository that working tree belongs to, set only when the probe
    -- established that the two differ (a linked worktree). It is the probe's answer
    -- and never a comparison of path prefixes, so a registration can state no
    -- hierarchy of its own.
    repository    text        NOT NULL DEFAULT '',
    -- When the place was first registered. A repeat registration keeps it, so the
    -- provenance is of the registration and not of the last click.
    registered_at timestamptz NOT NULL DEFAULT now(),
    -- Which principal registered it, empty on a deployment that authenticates
    -- nobody. It is recorded for audit only: nothing is filtered by it.
    registered_by text        NOT NULL DEFAULT ''
);
