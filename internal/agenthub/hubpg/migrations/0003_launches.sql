-- What was started from the hub: which request started which execution, where, and
-- by whom.
--
-- It is the hub's own memory and not the work's. The execution record says what ran;
-- this says that one client request asked for it, which is what makes a repeated
-- request — a double click, a retried fetch, a reload — end with one run instead of
-- several. Attribution lives here for the same reason: who asked is an audit fact,
-- and putting it into a workflow's input would write it into a replayable execution
-- history that has no use for it.
CREATE TABLE IF NOT EXISTS launches (
    -- The caller's own identity for the request. It is the identity of this row:
    -- the same request always describes the same launch.
    request_id  text        NOT NULL PRIMARY KEY,
    -- The execution the request started, minted from the request identity so the
    -- two can never disagree.
    workflow_id text        NOT NULL,
    -- What was started ("develop", "review").
    kind        text        NOT NULL,
    -- The working tree it runs in, resolved by the server from the place the
    -- request named. A request never carries a path.
    directory   text        NOT NULL,
    -- What the agent was told to do, empty for a review.
    prompt      text        NOT NULL DEFAULT '',
    -- When the work was first started under this request identity. A repeat does
    -- not move it.
    started_at  timestamptz NOT NULL DEFAULT now(),
    -- Which principal started it, empty on a deployment that authenticates nobody.
    started_by  text        NOT NULL DEFAULT ''
);

-- The run page answers "who started this, and from what request" by the execution.
CREATE UNIQUE INDEX IF NOT EXISTS launches_workflow_id_idx ON launches (workflow_id);
