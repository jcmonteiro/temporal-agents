# Slice 5 — Fleet plans in the store (replacing the JSON file)

**Goal:** make Postgres the sole source of truth for fleet plans, removing
`fleet-plan.json` entirely, with authoritative (not best-effort) failure
semantics.

## Tasks

- Add a `plans` table and a plans **port** + Postgres adapter in `internal/execstore`,
  storing an approved `fleet.FleetPlan` under a **generated ID** handle, with an
  optional operator-chosen `--name` recorded as **display-only metadata** (shown
  in `fleet plan list`, never an execution selector — no uniqueness rule, so it
  cannot resolve a plan deterministically).
- `fleet plan` persists the generated plan to the store and prints its handle.
  Remove the `--out <file>` flag and stop writing `fleet-plan.json`.
- `fleet execute` resolves a plan **by generated handle only**
  (`--plan-id <handle>`), still running `ValidatePlan` before starting any child
  workflow. Remove the `--plan <file>` flag, the
  `defaultPlanFile` constant, and the file read/strict-decode path.
- Record `fleet plan` generation as an execution: add a distinct **`FleetPlan`**
  kind and a `PersistFleetPlanWorkflowState` activity called from inside
  `FleetPlanWorkflow` (workflow-ID prefix `fleet-plan-`), so planning
  status/timing/token cost appear in `history` separately from `fleet execute`.
  Same must-succeed, idempotent-upsert semantics as every other `Persist…`.
- Add read commands to review stored plans: `fleet plan list` and
  `fleet plan show <handle>`.
- Apply **authoritative** failure semantics: a plan write (`fleet plan`) or read
  (`fleet execute`) that fails must error loudly and abort. This matches
  execution recording, which is likewise a hard dependency (never best-effort
  swallowed). The store is required for fleet to function.
- Correlate a stored plan with the fleet executions it produced (link to
  slice 3's `Fleet` records via the plan handle) where practical.

## Demo (done state)

`temporal-agents fleet plan "…"` stores a plan and prints its handle;
`temporal-agents fleet plan list` shows it; `temporal-agents fleet execute
--plan-id <handle>` runs it with no JSON file anywhere. A store outage on plan
read/write reports a clear error and aborts rather than proceeding.

## Depends on

Slice 1 (Postgres foundation, migration mechanism, `execstore` package,
connection wiring); benefits from slice 3 (fleet execution records to correlate
against).

## Testing

Adapter integration suite (real Postgres): plan store/load round-trip by handle,
and a read/write failure surfaces an error (authoritative). Temporal Go test
suite: assert `FleetPlanWorkflow` invokes `PersistFleetPlanWorkflowState` with a
`FleetPlan`-kind record (status/timing/tokens). Domain: `ValidatePlan` still
gates execute (existing tests) plus handle resolution unit tests.
