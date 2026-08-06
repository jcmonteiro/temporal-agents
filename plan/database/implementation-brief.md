# Implementation brief — Durable execution history and plan store

Derived from `brief.md`. Describes the shape any valid solution must fit, not a
single prescribed design.

## Named constraints (stated requirements)

These are real requirements, not incidental choices:

- **Postgres is the store.** The durable execution history and the plan store
  are backed by Postgres. (Requested explicitly; this is the one technology the
  solution is not free to swap.)
- **Local-first.** Postgres runs alongside the existing local Temporal dev
  server (`docker-compose.yml` currently starts only Temporal). Bringing the
  stack up must not require manual database setup steps beyond what starting the
  compose stack already implies.
- **Hexagonal boundary.** Persistence is reached through a port (a repository
  interface) owned by the application core; the Postgres driver/SQL lives in a
  driven adapter under `internal/`. No SQL or driver types leak into workflow or
  domain code. This mirrors the existing split (see `codereview`, `fleet`).
  Placement (decided): a shared `internal/execstore` package owns the port
  interface, the Postgres adapter, and the record types. The six
  `Persist<Type>WorkflowState` activities are **methods on the existing per-domain
  activity bundles** — `codereview.Activities` (Develop/Review/Pilot),
  `fleet.Activities` (Fleet + FleetPlan), and a root bundle for
  `PromptWorkflow`'s `Run` —
  each depending on the `execstore` port, injected from `main` exactly like the
  existing `Git`/`PRs`/`Agent` adapters. The port + adapter + record types live
  once in `execstore`; the activities sit next to the workflows that call them.
  The adapter uses `pgx` v5 (`pgxpool`); no pgx/SQL types leak past the port.
- **Determinism.** Temporal workflow functions must stay deterministic, so all
  database I/O happens inside activities (or in the CLI process), never in
  workflow code — the same rule the notification activity already follows.

## Token accounting (decided)

Each row stores **only its own incremental** token usage — the tokens from that
workflow's own Pi session(s), not the inclusive `TokensSoFar` running total the
workflows propagate for their result text. `history` therefore sums all rows for
a true total with no double-counting across the fleet→node→review tree. This
means the persisted per-row token figure deliberately differs from the inclusive
total a workflow prints in its summary: persist the delta (e.g. `res.Tokens`),
not the accumulated `TokensSoFar`.

## Record shape (decided)

A single `executions` table holds every record: common columns (two correlation
columns — `workflow_id` for grouping/tree correlation and `run_id` (Temporal
per-continue-as-new run ID) as the unique per-row key — `kind` discriminator,
prompt/goal, start, end, status, token usage, nullable schedule-ID, nullable
`parent_workflow_id`) plus a `jsonb` `detail` column for type-specific fields (PR URL,
review convergence, per-node breakdown). The six `Persist<Type>WorkflowState`
activities each take a typed input at the port but write into this one table;
the type boundary lives in code, not schema.

## What must persist

- **Execution records** for the commands that matter: `run`, `schedule`-fired
  runs, `code` (develop / review / pilot / open-pr), and `fleet` (plan
  generation, the parent orchestration, and its per-node develop executions). A
  record must carry enough
  to satisfy the brief's success signals: the originating command/kind, the
  prompt or goal, start and end times, terminal status (succeeded / failed /
  skipped / still-running), token usage, and a correlation handle to the
  Temporal workflow.
- **Fleet plans**, replacing `fleet-plan.json` as the source of truth: a stored
  plan is referable by a generated-ID handle, reviewable, and executable by that
  handle. The JSON file and its flags are removed (slice 5).

## Seams and boundaries the work touches

- **`docker-compose.yml`** gains a Postgres service and a persistent volume,
  parallel to the Temporal service.
- **Worker wiring (`main.go`, `runWorker`)** must construct the Postgres
  `execstore` adapter and inject it into each activity bundle that persists
  (`codereview.Activities`, `fleet.Activities`, and the root bundle backing
  `PromptWorkflow`), the same way `notification.Activity` and the
  `codereview`/`fleet` activity bundles are wired today.
- **The recording point (decided).** All recording is worker-owned and happens
  *inside the workflows* via per-type persistence activities named
  `Persist<Type>WorkflowState`, where `<Type>` is one of `Run`, `FleetPlan`,
  `Fleet`, `Develop`, `Review`, `Pilot`. `FleetPlan` records `FleetPlanWorkflow`
  (the `fleet plan` agent run, workflow-ID prefix `fleet-plan-`), so its
  status/timing/token cost is captured distinctly from `Fleet` (`fleet execute`).
  Schedule-fired work is **not** a distinct type: a
  schedule fires `PromptWorkflow` (the same workflow `run` uses), so it persists
  as `Run` with a nullable schedule-ID field carrying the parent schedule.
  Open-pr is likewise **not** a distinct type: `OpenPRWorkflow` runs only inside
  the `--with-remote` develop pipeline, so its PR URL is folded into the
  `Develop` record as a field and its standalone execution is not recorded.
  Skipped fleet nodes have no child workflow, so they are recorded in the parent
  `Fleet` row's `jsonb` `detail`, not as their own rows.
  The CLI never writes execution state; it
  only reads. Each workflow calls its `Persist…` activity to record start and to
  record the terminal update, analogous to how `notification.Activity` is invoked
  from a workflow's terminal path. Because writes are activities, workflow code
  stays deterministic.
- **Correlation key.** The Temporal workflow ID is the natural join between a
  record and its execution; ID prefixes already classify kind (see
  `classifyWorkflow`). Workflows nest — `FleetWorkflow` spawns per-node
  `DevelopWorkflow` children, and every `DevelopWorkflow` spawns a
  `ReviewWorkflow` child — and each child self-records via its own `Persist…`
  activity. A nullable `parent_workflow_id` column (from
  `workflow.GetInfo().ParentWorkflowExecution`) links each child to its parent so
  the fleet→node and develop→review trees are reconstructable and a child review
  is distinguishable from a standalone `code review`.
- **Plan read/write path.** `fleet plan` currently marshals a `fleet.FleetPlan`
  to `fleet-plan.json`; `fleet execute` reads and strictly decodes it. Moving to
  the store (decided): `fleet plan` persists to Postgres and prints a **generated
  ID** handle (optional `--name`); `fleet execute` resolves a plan **by handle**.
  The `--out <file>` / `--plan <file>` flags, the `defaultPlanFile` constant, and
  `fleet-plan.json` are **removed entirely** — the store is the sole source. The
  existing `ValidatePlan` gate must still run before execution.
- **Schema migrations (decided).** Embedded SQL files
  (`internal/execstore/migrations/*.sql` via `embed.FS`) applied idempotently
  against a `schema_migrations` tracking table when the `execstore` adapter
  initializes at worker startup. No separate migrate binary or manual psql step;
  an explicit `migrate` command could later call the same applier.

## Non-functional constraints and tensions

- **The start write is a hard dependency; the terminal write is not
  (decided).** Unlike notifications, a `Persist<Type>WorkflowState` write is
  never best-effort-and-forgotten, and Temporal's retry policy absorbs a
  transient Postgres outage either way. The two writes then differ in what an
  exhausted policy costs:
  - **Start write:** the workflow **fails at that point**. Nothing has happened
    yet, so refusing to run unrecorded costs nothing and keeps the history
    complete. Postgres is therefore a hard runtime dependency for *starting* any
    recorded workflow.
  - **Terminal write:** the workflow **keeps its outcome**. By then an agent has
    done up to an hour of work and the write has already retried for about two
    minutes; failing there would convert a bookkeeping outage into lost agent
    work, and on a continue-as-new path it would additionally strand the loop,
    because the workflow's error *is* the control signal. The failure is reported
    instead — logged with the result, and delivered as a best-effort
    notification carrying it — and the row is left at `running`, which is the
    same abandoned-looking state `history --help` already documents. The policy
    lives in one place (`wfrecord.TerminalWriteFailed`) so every workflow behaves
    identically.
- **Recorded free text is redacted and capped (decided).** A failure text can
  echo a token-authenticated git remote, a prompt or goal is operator-written (or
  agent-generated, for a fleet node), and a fleet node's detail is a whole agent
  output, so **every** recorded free-text field passes through one funnel
  (`wfrecord.Sanitize`) that removes URL credentials and GitHub token shapes and
  caps the length. The funnel is applied at the port boundary of each persistence
  activity, so no field can reach a column around it: the failure text (via
  `wfrecord.FailureText`), the prompt/goal of every kind, the fleet's per-node
  detail (`nodeOutcomes`), and the stored plan's goal. The record is long-lived and
  local, so an unredacted token would sit in it indefinitely and an uncapped field
  would grow a row without bound.
  The one exception is the stored plan's **document**, which must stay decodable
  and therefore can be neither redacted nor trimmed: it is size-guarded instead
  (`execstore.MaxPlanDocument`), and an oversized plan is refused non-retryably
  rather than stored.
- **Idempotent writes under retry.** Because Temporal may re-run an activity that
  already committed (result lost after a partial success), every `Persist…` write
  must be idempotent — an upsert keyed on `run_id` (`INSERT … ON CONFLICT
  (run_id) DO UPDATE`) — so a retried start or terminal write neither duplicates
  rows nor corrupts an existing one.
- **Configuration.** `DATABASE_URL` is a *required* env var (no default) — an
  unset/empty value is a fail-fast startup error in both worker and CLI, to
  prevent silent misconfiguration.
- **Plans are authoritative (decided).** Like execution recording, a fleet plan
  write/read must not be silently dropped. `fleet plan` failing to persist must
  error loudly and `fleet execute` failing to read the plan must abort. The store
  is required for fleet to function.
- **No secrets in output.** `DATABASE_URL` can carry credentials; never print it
  (follow the `worker` webhook precedent of enabled/disabled or host-only).

## Known risks, unknowns, and open decisions

- Where the "started" record is written: **decided** — worker-owned, inside each
  workflow via `Persist<Type>WorkflowState` activities.
- Migration mechanism: **decided** — embedded SQL applied idempotently at worker
  startup (see the plan read/write and migrations seams).
- Connection configuration: **decided** — required `DATABASE_URL` env var, no
  default, fail-fast at startup in worker and CLI; never logged.
- Plan handle: **decided** — generated ID (canonical) with optional `--name`.
- Retention/cleanup of records over time (brief lists this out of scope to
  decide now, but the schema should not preclude it). Related follow-up, not
  covered by this work: every write is made by the workflow itself, so a
  terminated workflow or a worker that never returns leaves its row at `running`
  for good. Nothing reconciles the store against Temporal and nothing prunes old
  rows, so a `history prune` command, or a reconciliation pass in `cleanup`, is
  still needed. `history --help` documents the effect in the meantime, and the
  terminal-write policy above adds a second way a row can be left at `running`
  (a store that was down only for the terminal write), which the same
  reconciliation pass would settle.
- Replay coverage of the recording version gate (`wfrecord.Enabled`):
  **closed** — every recorded workflow (`PromptWorkflow`, `DevelopWorkflow`,
  `ReviewWorkflow`, `PilotWorkflow`, `FleetWorkflow`, `FleetPlanWorkflow`) is
  replayed against a real history captured from the pre-recording code
  (`testdata/*_before_recording.json`), so the upgrade path of an in-flight
  execution is asserted per workflow, not assumed from the two that were covered
  first. The `FleetPlanWorkflow` fixture carries no plan handle, which pins the
  second gate on that path too (the `in.PlanID != ""` guard around `StorePlan`).
- Whether `templates.json` remains the store for `run`/`schedule` templates or
  also moves to Postgres (out of scope here). `fleet-plan.json` is removed
  (decided, slice 5).
- Failure semantics: **decided** — recording is a hard dependency (`Persist…`
  must succeed or the workflow fails; Temporal retries absorb transient outages);
  plans are authoritative. Writes are idempotent upserts on `run_id`.
- (Resolved) Schedule is not a persistence type: each fired run is a `Run`
  record with a nullable schedule-ID field. The schedule itself is not persisted
  (it has no workflow; it lives in Temporal, shown by `list`).

## Testing approach (Khorikov / hexagonal, per AGENTS.md)

Three layers, each tested for behavior:

- **Domain/core** (record types, `history` filtering/formatting, sum-of-root
  token logic): pure unit tests, no mocks — matches `fleet/domain_test.go` and
  the `codereview` tests.
- **Workflows/activities**: use Temporal's Go testing suite
  (`testsuite.WorkflowTestSuite` / `TestWorkflowEnvironment`,
  https://docs.temporal.io/develop/go/best-practices/testing-suite). The store is a
  managed dependency, so the real `Persist<Type>WorkflowState` activities run
  against one in-memory fake of the port (`execstoretest.Store`) rather than being
  mocked: the assertion is then the *record* that was written (kind,
  own-incremental tokens, `parent_workflow_id`, status, detail) instead of the fact
  that a call happened. `execstoretest.Failing` and `FailingAfter` inject the two
  outages that matter — a start write that cannot land (the workflow must fail) and
  a store that goes down between the start and terminal writes (the workflow must
  keep its outcome).
- **Replay**: each recorded workflow is replayed against a genuine history
  captured from the pre-recording code, because the recording version gate is code
  whose mistakes surface only at replay time, in production.
- **`execstore` Postgres adapter**: a **real-Postgres integration suite** on
  `testcontainers-go` (per `AGENTS.md`), since the DB and schema are owned
  out-of-process dependencies a mock would not exercise. It starts its own
  throwaway Postgres and gives each test a database of its own, so it cannot skip
  itself, needs no environment variable, and shares no state between tests.
  Covers upsert idempotency on `run_id`, concurrent `Migrate` from several
  workers, `jsonb` round-trip, and `history` query filters.

## Gate check

The major shaping decisions are now settled by the grilling session (recording
ownership, record shape, keying, token rule, package placement, migrations,
connection config, plan handling). What remains genuinely open — and where two
competent implementers could still diverge — is narrower: the exact `jsonb`
`detail` payload per type, index choices on the `executions` table, and whether
templates later move to Postgres. The brief's outcome is satisfied regardless of
those, so the space stays bounded without over-prescribing the SQL.
