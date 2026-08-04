# Slice 7 — Live read-path (Go read adapter → real Temporal data)

**Discharges:** brief outcome (overview reflects **real** agent work), IB §3
(data seam, Go read adapter in scope), IB §6 (additive Go only).

**Assumes (Q7):** PR #18 (`internal/fleet/`) is merged to the base branch, so
`FleetPlan`/`FleetNode`/`NodeStatus` and `fleet-plan.json` are available to
reference and reuse directly. Slices 1–6 + 8 do not depend on this.

**Demo:** with the Temporal dev server and worker running, the Overview shows
**real** workflow executions orbiting the center with their live statuses —
kicked-off runs appear, completed ones show `done`, failed ones show `failed`.
Swapping the frontend client from fixtures to the live endpoint requires no
component changes.

## Tasks

### Go side (hexagonal, additive)

- [ ] Add `internal/httpapi/` (or `internal/overview/`) with a **driven port**
      abstracting "list current work items" — not the Temporal SDK directly
      (IB §3).
- [ ] Implement the driven adapter over the Temporal client
      (`ListWorkflowExecutions` / `DescribeWorkflowExecution`), mapping native
      execution status to `WorkStatus` (Q3 = A): Running/ContinuedAsNew →
      `in-progress`, Completed → `done`, Failed/TimedOut/Terminated/Canceled →
      `failed`. Do **not** emit `waiting-input`/`paused` (no source) and do
      **not** instrument the existing workflows (IB §3 mapping constraint).
- [ ] Read the fleet **plan** DAG (`fleet-plan.json` written by
      `FleetPlanWorkflow` — Q4) and reconcile against live child executions
      matched by the `<fleetID>-<nodeID>` convention. Derive per-node status per
      IB §3: no-exec+dep-pending → `waiting`; no-exec+deps-done → `todo`;
      no-exec+dep-failed → `paused` (≈ skipped); Running → `in-progress`;
      Completed → `done`; Failed → `failed`; `SeedConflictBlocked` →
      `waiting-input` (≈ blocked, optional in first pass). "Up Next" =
      `todo`/`waiting` nodes. Reuse `internal/fleet` domain types where possible.
- [ ] Serve **resource endpoints** (Q19): `GET /api/v1/fleets` (fleets with
      backend-**aggregated** status + derived progress — Q15), `GET /api/v1/runs`
      (`run`/`code develop` executions), `GET /api/v1/schedules` (`schedule`s),
      and `GET /api/v1/fleets/:id` (a fleet's `FleetNode` DAG — nodes +
      `DependsOn` edges + per-node status + child workflow). JSON matches
      `src/domain/` types, **payloads portable / DB-agnostic** so a future DB
      swaps only the adapter. **No cross-fleet edges**; `owner`/`estimate`/
      `description` are not modelled (Q6=A, Q10). The Overview satellites are
      composed by the frontend from fleets + runs + schedules (each tagged with
      its `kind`).
- [ ] Implement the fleet **status aggregation** in the Go adapter (Q15), not
      the frontend.
- [ ] Expose via a **new `serve` CLI subcommand** (Q17) that does not alter
      existing commands; reads `TEMPORAL_ADDRESS` like the rest of the CLI.
- [ ] Serve the built SPA (`web/dist`) as **independently hostable static
      assets** (Q18): `serve` serves the bundle locally for convenience, but the
      API (JSON under `/api/v1`) stays decoupled from asset hosting so the same
      bundle can later sit behind **S3 + a CDN** unchanged. Configurable base
      path. Dev uses the Vite `/api/v1` proxy against this API.
- [ ] Tests: port has a fake/stub in unit tests for the handler (assert JSON
      contract + status mapping); the Temporal adapter is covered per the repo's
      existing adapter-testing approach. `go build .` and existing Go tests/CI
      stay green.

### Frontend side

- [ ] Add the live implementation of the `clients/agent-hub` boundary that calls
      `/api/v1/fleets`, `/api/v1/runs`, `/api/v1/schedules`, and
      `/api/v1/fleets/:id` via `proxyFetch`, returning `Result<T, E>`.
- [ ] Select fixtures vs live via config/env (`Config` flag) so tests and
      offline dev keep using fixtures; no component changes (IB §3).
- [ ] Test: live client maps a sample payload to domain types and surfaces
      HTTP/parse failures through the `Result` failure branch.

## Done when

Running the `serve` command with a live worker shows real fleets (aggregated)
and standalone workflows in the Orbit with correct statuses, the fleet view
renders a real fleet's nodes, the JSON contract matches the domain types, the
fixture path still works for offline/test, and no existing Go command changed
behaviour.
