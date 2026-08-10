-- The instruction context's own schema: the versions of every instruction the tool
-- governs, and which version each scope currently points at.
--
-- It is a context of its own, with its own migrations and no foreign key to any
-- other context's tables. An execution records which instruction version it used as
-- values (key, scope, version, hash) rather than as a reference, so the record and
-- these tables stay independently migratable, and neither can be blocked from
-- changing by the other.

-- Every version of every instruction, for every scope it was ever set at. The table
-- is append-only by contract: an edit inserts the next version, and a reset moves or
-- removes a pointer. Nothing a finished run referenced is ever updated or deleted,
-- because provenance has to stay resolvable after any later edit.
CREATE TABLE IF NOT EXISTS instruction_versions (
    -- Which governed instruction this is a version of ("review.perform").
    key        text        NOT NULL,
    -- Where it was set: 'global', 'factory', or 'directory:<absolute path>'. The
    -- scope is stored as the value the core computes, so the database holds no rule
    -- about how places relate; the chain is resolved in the core.
    scope      text        NOT NULL,
    -- Which version of this (key, scope) it is, counted from 1 and never reused.
    version    bigint      NOT NULL,
    -- The instruction itself.
    body       text        NOT NULL,
    -- The content hash of the body, recorded with every execution that used it so a
    -- past run's instruction is identifiable even by hand.
    hash       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key, scope, version)
);

-- Which version each scope currently resolves to. It is a pointer rather than a
-- flag on a version row so a reset is one write, and so the version rows stay
-- untouched by it.
CREATE TABLE IF NOT EXISTS instruction_pointers (
    key        text        NOT NULL,
    scope      text        NOT NULL,
    version    bigint      NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key, scope),
    -- Within one context a foreign key is exactly the right tool: a pointer to a
    -- version that does not exist would resolve to nothing at all, and the failure
    -- would surface as an agent running with no instruction.
    FOREIGN KEY (key, scope, version)
        REFERENCES instruction_versions (key, scope, version)
);
