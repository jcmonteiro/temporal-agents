# temporal-agents

A thin [Temporal](https://temporal.io) worker that runs a [Pi](https://pi.dev/) coding agent. Kick off one-off prompts, schedule recurring runs, or drive full code workflows (develop → review → PR → review w/ Copilot) against the current repo — all durable and observable via Temporal.

## Requirements

- Go 1.25.4+
- `pi` CLI on your `PATH` (the agent the worker runs)
- Docker (for the local Temporal server and Postgres)

## Install

```sh
make install        # go install → $GOBIN (or $GOPATH/bin); ensure it's on PATH
# or
make build          # build ./temporal-agents in the current dir
make setup          # optional: enable git hooks (gofmt on commit)
```

## Run

1. Start the Temporal dev server (gRPC on `17233`, web UI on http://localhost:18233) and Postgres (host port `15432`):

   ```sh
   docker compose up -d
   ```

2. Point the worker and CLI at Postgres. `DATABASE_URL` is **required** (there is
   no default): the worker and the store-backed commands fail fast when it is
   unset, so a misconfiguration can never silently drop recorded history.

   ```sh
   export DATABASE_URL=postgres://postgres:postgres@localhost:15432/temporal_agents?sslmode=disable
   ```

3. Start the worker (executes workflows + notifications; applies the store's
   schema migrations at startup):

   ```sh
   temporal-agents worker
   ```

4. In another terminal, submit work:

   ```sh
   temporal-agents run "summarize the README"
   temporal-agents watch <workflow-id>
   temporal-agents history
   ```

The CLI connects to `localhost:17233` by default. Override with `TEMPORAL_ADDRESS`.

### Durable execution history

Temporal ages out completed executions, so its event history is not a lasting
record. Every `run`, `code`, and `fleet` workflow therefore records itself into
Postgres — one row per Temporal run ID, written when it starts and updated when
it settles — and `history` reads that durable record back:

```sh
temporal-agents history                          # newest 20 executions
temporal-agents history --kind develop           # one command type
temporal-agents history --workflow-id fleet-…    # one execution and its children
temporal-agents history --schedule-id schedule-… # the runs one schedule fired
```

Each row reports only its **own** token usage, so the printed total sums a fleet
run's parent, its nodes and their reviews without counting the same tokens twice.
A skipped fleet node never starts a workflow of its own, so it is listed from its
parent's per-node breakdown.

Recording is a hard dependency, not best-effort: a workflow that cannot write
its record retries and, if Postgres stays down, fails. `list` remains the live
Temporal view; `history` is the durable one.

## Commands

| Command | What it does |
|---|---|
| `worker [--no-desktop] [--webhook <url>]` | Start the worker. Desktop notifications on by default (macOS); `--webhook` POSTs completion JSON. |
| `run "<prompt>" [--save <name>] [--chain]` | Start a workflow and return immediately. |
| `schedule "<interval\|cron>" "<prompt>" [--save <name>] [--chain]` | Run a workflow on a schedule (overlaps skipped). Interval = Go duration (`1h`, `30m`); or a 5-field cron. |
| `template <list\|show\|run\|delete> [name]` | Manage/run prompts saved via `--save`. |
| `code pilot [--append\|--replace <prompt>] [--no-chain] [--summary]` | Address unresolved review comments on the current branch's PR (loops until none remain; `--no-chain` runs a single pass). |
| `code review [--summary]` | Review the current branch locally, then implement + re-review in a loop. |
| `code develop "<prompt>" [--branch <name>] [--worktree] [--summary] [--with-remote]` | Create a branch, implement the prompt, then run the review loop (and PR + pilot with `--with-remote`). |
| `fleet plan "<prompt>" [--name <name>]` | Have the agent decompose a change into a dependency graph, stored under a printed handle. |
| `fleet plan <list\|show>` | Review the stored plans. |
| `fleet execute --plan-id <handle> [--summary]` | Orchestrate a stored plan: a develop workflow per node, run in dependency order. |
| `watch <workflow-id>` | Stream a workflow's live Pi progress, then its result. |
| `list` | List running workflows and schedules (fleet parents and per-node children included). |
| `history [--kind <k>] [--limit <n>] [--workflow-id <id>] [--schedule-id <id>]` | List durably recorded executions, newest first. |

Common flags: `--save <name>` stores the invocation as a reusable template; `--chain` re-triggers `run`/`schedule` on each success (`code pilot` chains by default, disable with `--no-chain`); `--summary` (code subcommands only) sends a Pi-generated summary as the webhook body, and on `fleet execute` propagates that behavior to each node's develop workflow.

Run any command with `--help` for details, e.g. `temporal-agents code develop --help`.

### Fleet fan-out

Larger changes are better delivered as several small, independently reviewable
slices with explicit ordering (e.g. a horizontal slice implementing a domain
core, followed by two parallel vertical slices exposing it via REST and gRPC).
The `fleet` command orchestrates exactly that:

```sh
# 1. Decompose the change into a dependency graph, stored under a printed handle.
temporal-agents fleet plan "expose the pricing domain via REST and gRPC"

# 2. Review the stored plan, then run it by its handle.
temporal-agents fleet plan show <handle>
temporal-agents fleet execute --plan-id <handle>
```

Plans live in Postgres, not in a file: `fleet plan list` shows the newest stored
plans (20 by default, `--limit <n>` for more) and `fleet plan show <handle>` prints
one. Like execution recording, the plan
store is authoritative — a plan that cannot be written or read aborts loudly
rather than proceeding.

`fleet execute` runs a `code develop` workflow per node, processing the graph in
dependency layers: independent nodes run in parallel, and a node starts only
once every node it depends on has succeeded (a node whose dependency did not
succeed is skipped). Each node develops in its own git worktree so parallel
nodes never share a working tree. When every node settles, a single summary
notification aggregates each node's status and develop-step token usage.

Dependencies control both execution **order and code layering**: each node
develops on its own branch, seeded with the committed work of the nodes it
depends on (its branch is the pinned base plus a merge of each dependency's
branch), so a dependent is developed on top of its prerequisites' reviewed code.
A node counts as "succeeded" once its develop step
lands **and** its local review loop converges, so a dependent starts only after
every node it depends on has been both developed and reviewed.

## Tests

```sh
make test              # unit + workflow tests (the Postgres adapter suite skips itself)
make test-integration  # also runs the adapter suite against the compose Postgres
```

The `execstore` adapter is tested against a real Postgres, since the database and
its schema are the out-of-process dependency under test. That suite skips unless
`TEST_DATABASE_URL` is set, and it truncates the tables it uses, so it refuses any
database whose name does not end in `_test`. `make test-integration` creates and
uses `temporal_agents_test` on the compose Postgres — never the `temporal_agents`
database you work in, whose recorded history and stored plans would be deleted.
CI runs the same suite against a throwaway Postgres service container.

## Docker

Temporal and Postgres run in Docker (see `docker-compose.yml`); the worker and CLI run on the host so the agent can operate on your local repo.

```sh
docker compose up -d      # start Temporal + Postgres (each persists in a named volume)
docker compose down       # stop, keeping both volumes
```

Postgres is published on `127.0.0.1:15432` only. The credentials are fixed local
dev values, and the store holds prompts, agent output, PR links and verbatim
failure text, so the loopback binding is what keeps it off the network.

Avoid `docker compose down -v`: it removes **every** named volume, so it wipes
the recorded execution history and stored fleet plans along with Temporal's
state. To reset Temporal only, recreate that one service:

```sh
docker compose rm -sv temporal    # wipe Temporal state, keep the Postgres volume
docker compose up -d temporal
```
