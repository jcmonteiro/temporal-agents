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

3. Apply the database schema. This is a deliberate step of its own, and it comes
   **before** starting anything:

   ```sh
   temporal-agents migrate
   ```

4. Start the worker (executes workflows + notifications):

   ```sh
   temporal-agents worker
   ```

5. In another terminal, submit work:

   ```sh
   temporal-agents run "summarize the README"
   temporal-agents watch <workflow-id>
   temporal-agents history
   ```

6. Start the Agent Hub REST API before using `list`. It also serves a built
   `web/dist` bundle when one exists:

   ```sh
   temporal-agents serve
   # API entry point: http://127.0.0.1:8973/api/v1
   # OpenAPI:        http://127.0.0.1:8973/api/v1/openapi.json
   ```

### Schema migration: migrate, then start

Applying the schema and starting a process are separate operations, and the order
is fixed: **`migrate` first, then `worker` and `serve`**.

- `temporal-agents migrate` applies every bounded context's own migrations and
  prints the version each context ends at. It is idempotent, so running it against
  an up-to-date database applies nothing, and two invocations at once serialize on
  a database lock instead of racing.
- `worker` and `serve` **verify** the schema at startup and apply nothing. A
  database older than the build refuses the process immediately, naming the
  context, the version the database is at, the version the build requires, and the
  command that fixes it.
- Each context owns its migrations inside its own adapter package
  (`internal/execstore/execpg/migrations`, `internal/agenthub/hubpg/migrations`,
  `internal/identity/identitypg/migrations`,
  `internal/scoped/scopedpg/migrations`),
  recorded under its own namespace, with no foreign keys between contexts. A
  process is only stopped by the contexts it actually uses: a stale dismissal
  schema stops `serve`, not `worker`.
- For local iteration only, `TEMPORAL_AGENTS_DEV_AUTO_MIGRATE=1` lets `worker` and
  `serve` apply migrations at startup instead of refusing. It is opt-in, warns
  through the process's log on every start, and must never be set outside
  development.
- `history` only reads the record, so it verifies nothing: run against a database
  the migrate step has not reached, it reports the database's own error instead of
  a schema failure.

```sh
$ temporal-agents migrate
execution-store  brought 4 migration(s) up to date  schema 0004_execution_first_run_id.sql
agent-hub        brought 1 migration(s) up to date  schema 0001_dismissals.sql
identity         brought 1 migration(s) up to date  schema 0001_identity.sql
scoped-config    brought 1 migration(s) up to date  schema 0001_scoped_values.sql
```

**Upgrading a running installation to this workflow:** a build from before the
split applied its own migrations while starting; this one refuses to start until
the schema is current. Run `temporal-agents migrate` once against the deployment's
database before rolling the new build out, or the first `worker` and `serve` of
the rollout stop with a stale-schema failure. Migrating an already up-to-date
database applies nothing, so the step is safe to run ahead of time.

Workflow submission and `watch` connect to `localhost:17233` by default. Override
that address with `TEMPORAL_ADDRESS`. The `list` command reads
`http://127.0.0.1:8973/api/v1` instead. Override its versioned endpoint with
`AGENT_HUB_API_URL`; when the API requires authentication, `list` sends the token
from `AGENT_HUB_AUTH_TOKEN`. A non-loopback endpoint must use HTTPS.

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

Recording is not best-effort: a write retries until it succeeds or Postgres has
clearly stayed down. What an exhausted retry costs then depends on which write it
was — a workflow that cannot record its *start* does not run at all, while one that
cannot record its *outcome* keeps its result and reports the bookkeeping failure
(the row is left at `running`, so treat an hours-old `running` row as abandoned).
`list` remains the live Agent Hub overview; `history` is the durable execution
record.

### Stored instructions

What the agent is told when it reviews a branch, acts on a review, or addresses
pull request comments is stored, not compiled in. The worker publishes the
instructions this build ships into Postgres when it starts, so an upgrade that
improves one reaches every place that has not overridden it, and each unit of work
resolves the instructions it needs **once**, at its start:

- Resolution walks the place the work runs in, then the repository that place
  belongs to, then the installation, then the shipped default — **per instruction**,
  so a place can override one and inherit the rest.
- A loop resolves at its first pass and carries the result through every
  continue-as-new, so an instruction edited while it runs cannot change what a later
  pass did.
- Every settled execution records which instruction it used — the key, where the
  value came from, its version and the hash of its text — and `history` prints it,
  so a past result stays explainable. Versions are append-only: an edit adds one and
  never rewrites what an earlier run referenced.
- A store that cannot answer fails the unit of work rather than quietly falling back
  to a default, because a silent substitution would change agent behaviour with
  nothing in the record to say so.

Configuring an instruction from the hub arrives with the prompt configuration
surface; today the shipped defaults are what every place resolves to, so behaviour
is unchanged for anyone who configures nothing.

## Commands

| Command | What it does |
|---|---|
| `migrate` | Apply every bounded context's schema, then print the version each ends at. Run it before `worker` and `serve`. |
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
| `list` | Read the Agent Hub HTTP API and list active top-level fleets, runs, and schedules. |
| `history [--kind <k>] [--limit <n>] [--workflow-id <id>] [--schedule-id <id>]` | List durably recorded executions, newest first. |
| `serve [--addr <host:port>] [--web-dir <path>] [--allow-host <host>]... [--allow-origin <origin>]...` | Serve the versioned Agent Hub REST API and, optionally, an independently built SPA. |

Common flags: `--save <name>` stores the invocation as a reusable template; `--chain` re-triggers `run`/`schedule` on each success (`code pilot` chains by default, disable with `--no-chain`); `--summary` (code subcommands only) sends a Pi-generated summary as the webhook body, and on `fleet execute` propagates that behavior to each node's develop workflow.

Run any command with `--help` for details, e.g. `temporal-agents code develop --help`.

### Agent Hub REST API

`serve` adds a local API that is also the read boundary for the CLI's `list`
command. It exposes portable resources under `/api/v1`:

- `GET /api/v1/active-work` (bounded cursor pages used by `list`)
- `GET /api/v1/fleets` and `GET /api/v1/fleets/{id}`
- `GET|POST /api/v1/runs` and `GET /api/v1/runs/{id}`
- `GET /api/v1/schedules`
- `GET|POST /api/v1/dismissals` and `DELETE /api/v1/dismissals/{id}`
- `GET|POST /api/v1/places`

Work is also *started* here, which is the one thing the API does that changes
anything outside itself. A start names a place and never a path: the server resolves
the working tree from the places it knows — the ones an operator registered through
`/api/v1/places`, and the ones it has watched work run in — so the registry is the
allowlist of where an agent may run. One caller-supplied request identity always
names one execution, so a double click, a retried request or a reload ends with one
run, and a start into a place something is already running in is refused naming the
work in the way, because two loops in one working tree commit over each other.

The server joins live Temporal state with the durable execution and plan record.
Finished items remain visible until dismissed. A dismissal is Postgres-backed view
state and never changes workflow state. Continue-as-new iterations are one run
resource identified by workflow ID.

Every route needs a credential, except signing in, the provider's callback and the
health probe. A person signs in with an identity provider: the local compose stack
runs one, and a loopback listener uses it by default, so `docker compose up -d` plus
`serve` plus one click is a working sign-in. Point the hub elsewhere with
`AGENT_HUB_OIDC_ISSUER`, `AGENT_HUB_OIDC_CLIENT_ID` and
`AGENT_HUB_OIDC_CLIENT_SECRET`. The API is the confidential client: the browser
receives a script-inaccessible session cookie and never a token.

Scripts and the CLI authenticate with `AGENT_HUB_AUTH_TOKEN` and no browser; on a
loopback listener `serve` mints that token and `list` reads it, so neither needs
configuring. An open API is possible only by asking for it
(`AGENT_HUB_ALLOW_UNAUTHENTICATED=1`), only on loopback, and the process says so on
every start.

The API binds to `127.0.0.1:8973` by default and accepts only configured HTTP Host
names. A non-loopback `--addr` requires `--tls-cert`, `--tls-key`, and a strong
`AGENT_HUB_AUTH_TOKEN`; remote clients send the token only over HTTPS. Additional Host
names use `--allow-host`. Each cross-origin browser
origin requires an exact `--allow-origin`; other supplied origins are rejected. The
contract is OpenAPI 3.1; versioned standalone JSON Schemas and resolvable problem types
are served with it. See [the REST API guide](docs/rest-api.md).

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

`list`, `ls` and `show` are read as subcommands, so a goal cannot be worded as
exactly one of them; say `"plan a listing of …"` instead.

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
make test   # every test, integration suites included
```

The `execstore` and Agent Hub dismissal adapters are tested against a real
Postgres, since the database and its schema are the out-of-process dependency under
test, and the migration step itself is tested the same way (fresh database, stale
database refused, repeated migrate, concurrent migrate). Those suites start
throwaway Postgres instances with
[testcontainers-go](https://golang.testcontainers.org/),
so it needs a running Docker daemon but no setup and no environment variable — and
it never touches the `temporal_agents` database you work in. Each test gets a fresh
database inside the container, so no test can see another's rows.

The target runs `go test -race -shuffle=on ./...`, which is exactly what CI runs, so
a green run locally means what a green run in CI means.

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
the recorded execution history, stored fleet plans, and Agent Hub dismissals along
with Temporal's state. To reset Temporal only, recreate that one service:

```sh
docker compose rm -sv temporal    # wipe Temporal state, keep the Postgres volume
docker compose up -d temporal
```
