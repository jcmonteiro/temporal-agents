-- The steering context's own schema: the rounds that stopped for an operator, and
-- the conversations that may produce the guidance they are answered with.
--
-- It is a context of its own, with its own migrations and no foreign key to any
-- other context's tables. A session names the run that is waiting as a value, and
-- the execution record names the session it is waiting in as a value, so the two
-- stay independently migratable and neither can block the other from changing.

-- One waiting round. The row exists from the moment the loop stops until long after
-- it resumed: it is the record of what was decided, by whom, and about what.
CREATE TABLE IF NOT EXISTS steering_sessions (
    -- The session's identity, which is the identity of the orchestrated unit that
    -- waits. It is stable for the session's whole life, so a conversation can be
    -- keyed on it.
    id          text        NOT NULL PRIMARY KEY,
    -- The run that is waiting — the work an operator sees on the overview. A value,
    -- never a reference: the execution record is another context.
    item_id     text        NOT NULL,
    -- Which pause point this is ('local-review', 'remote-comments').
    round       text        NOT NULL,
    -- What the decision is about: the review's findings, or the unresolved comments,
    -- as the agent would have received them. Nothing else keeps it, and an operator
    -- cannot decide without reading it.
    material    text        NOT NULL DEFAULT '',
    -- Where the paused work runs, as the location probe established it.
    directory   text        NOT NULL DEFAULT '',
    repository  text        NOT NULL DEFAULT '',
    -- The guidance text as it stands, editable until the decision is sent.
    guidance    text        NOT NULL DEFAULT '',
    -- When the round started waiting, so an interface can say since when. An
    -- unbounded wait that cannot say how long it has been waiting is not prominent.
    opened_at   timestamptz NOT NULL DEFAULT now(),
    -- 'waiting' or 'decided'. A session has no other state: it waits for a human,
    -- and then it has been answered.
    state       text        NOT NULL DEFAULT 'waiting',
    -- What was decided ('guide', 'skip', 'stop'), empty while it waits. The first
    -- decision wins, which is enforced by only ever writing this where it is empty.
    choice      text        NOT NULL DEFAULT '',
    -- Who decided, recorded for audit. Any signed-in operator may answer, so this
    -- says who did, never who was allowed to.
    principal   text        NOT NULL DEFAULT '',
    -- When the decision that won was recorded, and null while none has been.
    decided_at  timestamptz
);

-- The rounds waiting for somebody, oldest first, is the read an operator's surface
-- opens with.
CREATE INDEX IF NOT EXISTS steering_sessions_waiting_idx
    ON steering_sessions (opened_at)
    WHERE state = 'waiting';

-- The conversation, one row per turn. It is append-only by contract: there is no
-- statement in the adapter that updates or deletes a turn, because the transcript is
-- authoritative here and a rewritten transcript would make the guidance
-- unexplainable.
CREATE TABLE IF NOT EXISTS steering_messages (
    -- The session the turn belongs to. Within one context a foreign key is exactly
    -- the right tool: a turn of a conversation nobody started is not readable.
    session_id text        NOT NULL REFERENCES steering_sessions (id) ON DELETE CASCADE,
    -- The turn's position in the session, counted from 1 and never reused. A reader
    -- that has seen n asks for what came after n, which is what makes the stream
    -- resumable rather than time-ordered.
    sequence   bigint      NOT NULL,
    -- Who produced it: 'operator' or 'agent'.
    role       text        NOT NULL,
    -- The principal who wrote it, empty for the agent's own turns.
    author     text        NOT NULL DEFAULT '',
    -- What was said.
    body       text        NOT NULL,
    -- What the turn cost. Only the agent's turns cost anything, and the cost is
    -- visible while the conversation grows because it is operator-driven.
    tokens     integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, sequence)
);
