# Brief — Durable execution history and plan store

## Problem

Everything the tool does happens as a Temporal workflow, but the only record of
what happened lives in Temporal's event history. That record is:

- **Retention-limited** — Temporal ages out completed executions, so older runs
  are simply gone.
- **Wiped on reset** — `docker compose down -v` (documented as the normal way to
  clear stored state) erases all history.
- **Awkward to query across runs** — `list` shows only what is *currently*
  running or scheduled; there is no answer to "what have I run over the last
  week, with what outcome, at what token cost?"

Separately, a fleet **plan** is a hand-managed JSON file (`fleet-plan.json`) in
the current directory. It is easy to lose or overwrite, is not correlated with
the executions it produced, and cannot be referred to later ("run the plan I
approved yesterday").

The person affected is the operator running the worker and submitting work: they
have no durable, queryable memory of what the agent has done, and no stable home
for the plans they approve.

## Desired outcome

When this ships:

- Every meaningful command execution — `run`, `schedule`, `code`, `fleet` — is
  **durably recorded** the moment it starts and updated when it settles, in a
  store that outlives Temporal's retention and survives a Temporal state wipe.
- A recorded execution captures enough to be useful after the fact: what was
  asked, which command produced it, when it started and finished, how it ended
  (succeeded / failed / skipped), and its token cost.
- Fleet **plans live in that same durable store** and can be referred to by a
  stable handle for review and execution, instead of being a loose file the
  operator has to keep track of.
- The operator can look back over past executions and plans independently of
  whatever Temporal currently retains.

## Success signals

- After running several commands, the operator can list past executions with
  their command type, status, timing, and token usage — including executions
  Temporal has already aged out.
- Approving a plan and executing it later works by referring to the stored plan,
  with no JSON file to locate or pass around.
- Wiping Temporal's state does not erase the execution history or the stored
  plans.
- A recorded execution can be traced back to its Temporal workflow, so the
  durable record and the live system agree on what ran.

## Scope boundaries

Out of scope for this feature:

- Any multi-user, authentication, or access-control concern — this remains a
  single-operator local tool.
- A web UI or dashboard; surfacing history is through the existing CLI.
- Moving Temporal off its own datastore or otherwise changing how Temporal
  persists workflow state.
- Changing what the agent *does* — this only records what already happens.
- A hosted or shared database; the target is the operator's local environment.
- Retention/cleanup policy for the new store beyond noting it as an open
  question (see the implementation brief).
