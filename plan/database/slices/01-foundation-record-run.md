# Slice 1 — Foundation + record `run` + `history` read

**Goal:** stand up Postgres and prove the end-to-end recording path with the
simplest command (`run`), plus a way to observe records. This slice carries the
foundation so it is demoable rather than a bare infrastructure layer.

## Tasks

- Add a Postgres service and a persistent volume to `docker-compose.yml`,
  parallel to the Temporal service: pinned `postgres:17` (by digest, matching the
  Temporal pin style), named volume for persistence, host port `15432:5432`,
  fixed dev creds (`postgres`/`postgres`, db `temporal_agents`), and a
  `pg_isready` healthcheck.
- Document the required `DATABASE_URL` export in the README's Run section (right
  after `docker compose up -d`), e.g.
  `export DATABASE_URL=postgres://postgres:postgres@localhost:15432/temporal_agents?sslmode=disable`.
- Establish the schema-migration mechanism: embedded SQL files
  (`internal/execstore/migrations/*.sql` via `embed.FS`) applied idempotently
  with a `schema_migrations` tracking table when the `execstore` adapter
  initializes at worker startup — no separate migrate binary or step. Create the
  single `executions` table: common columns — two
  correlation columns `workflow_id` (groups a chained run's continue-as-new
  iterations and correlates the tree) and `run_id` (the Temporal per-iteration
  run ID, unique per row / primary key) — `kind` discriminator, prompt/goal, start
  time, end time, terminal status, token usage, nullable schedule-ID (reserved
  for slice 4), nullable `parent_workflow_id` (a child workflow's parent, from
  `workflow.GetInfo().ParentWorkflowExecution`) so the fleet→node and
  develop→review trees are reconstructable — plus a `jsonb` `detail` column for
  type-specific fields (e.g. PR URL, converged, per-node breakdown) so new fields
  need no migration.
- The five `Persist<Type>WorkflowState` activities each take a typed input but
  write rows into this one table; the type boundary lives in code, not schema.
- Define the executions **port** (repository interface), the Postgres **adapter**
  (using `pgx` v5 / `pgxpool`), and the record types in a shared
  `internal/execstore` package. No SQL/driver types cross the port.
  `PersistRunWorkflowState` is a method on a root-level
  activity bundle (alongside `RunPiAgent`) that depends on the `execstore` port;
  slices 2–3 add the same to `codereview.Activities` and `fleet.Activities`.
- Wire connection config via a **required** `DATABASE_URL` env var (no default):
  missing/empty is a startup/config error in both worker and CLI, to prevent
  silent misconfiguration. Never print the DSN (follow the `worker` webhook
  precedent). Note: `DATABASE_URL` is required at startup; runtime writes are a
  hard dependency (below), not best-effort.
- Add the `PersistRunWorkflowState` activity and call it from inside
  `PromptWorkflow` to record a "started" state and a terminal update on
  completion/failure. A `--chain` run loops via continue-as-new: each iteration
  is its **own row** keyed on the Temporal run ID (same `workflow_id`, distinct
  `run_id`). Each write is an **idempotent upsert on `run_id`** (so a retried
  activity neither duplicates nor corrupts a row). Record
  only this workflow's **own incremental** tokens
  (`res.Tokens`), not the inclusive `TokensSoFar` running total — so summing rows
  gives a true total (see token accounting in the implementation brief). The
  `Persist…` activity **must succeed**: a failure fails `PromptWorkflow` (Temporal
  retries absorb transient outages). Recording is not best-effort.
- Add a `history` command that lists recorded executions newest-first, flat (one
  row each), default limit 20, columns `kind`/`status`/started/ended/tokens/
  `workflow_id`. Filters: `--kind <run|develop|review|pilot|fleet>`, `--limit`,
  and `--workflow-id <id>` (shows one run's whole tree via `parent_workflow_id`).
  In-flight `running` rows are included and distinguished by status. `list`
  (the live Temporal view) is left unchanged; `history` is the durable record.

## Demo (done state)

Start the stack, run `temporal-agents run "…"`, then `temporal-agents history`
shows the execution with its status and token cost. Stop Postgres and run again:
the workflow's `Persist…` activity retries and, if Postgres stays down, the
workflow fails (recording is a hard dependency, not best-effort). Recorded rows
survive `docker compose down -v` on the Temporal service.

## Testing

- Domain: pure unit tests for `history` filtering/formatting and record types.
- Workflow: Temporal Go test suite (`env.OnActivity`) asserts `PromptWorkflow`
  invokes `PersistRunWorkflowState` at start and terminal with the right record,
  and fails when the activity fails (must-succeed).
- Adapter: real-Postgres integration suite — upsert idempotency on `run_id`,
  `jsonb` round-trip, `history` filters. See the implementation brief's testing
  approach.
