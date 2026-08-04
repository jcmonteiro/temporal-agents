# Slice 7 — Live read-path (Go read adapter → real Temporal data)

**Discharges:** brief outcome (overview reflects **real** agent work), IB §3
(data seam, Go read adapter in scope), IB §6 (additive Go only).

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
- [ ] Compose the **overview** satellites (Q5): one item per **fleet**
      (aggregated status via the domain rule, derived progress = done/total
      nodes) and one per **standalone workflow** (`run`/`schedule`/`code
      develop` executions that are not fleet children), each tagged with its
      `kind`.
- [ ] Add an HTTP handler serving `GET /api/v1/overview` (items + up-next) and
      `GET /api/v1/fleets/:id` (fleet detail: its `FleetNode` DAG — nodes +
      `DependsOn` edges + per-node status + child workflow) returning JSON
      matching `src/domain/` types. **No cross-fleet edges.** `owner`/`estimate`/
      `description` are not in the live model — omit them (Q6=A, IB §4b).
- [ ] Expose it via a **new** entrypoint that does not alter existing commands:
      a `serve`/`web` CLI subcommand (preferred) or a `--http` flag. Reads
      `TEMPORAL_ADDRESS` like the rest of the CLI.
- [ ] Serve the built frontend (`web/dist`) as static files from the same
      binary (embed or static dir), so `serve` gives a single local URL. Dev
      still uses the Vite `/api/v1` proxy against this endpoint.
- [ ] Tests: port has a fake/stub in unit tests for the handler (assert JSON
      contract + status mapping); the Temporal adapter is covered per the repo's
      existing adapter-testing approach. `go build .` and existing Go tests/CI
      stay green.

### Frontend side

- [ ] Add the live implementation of the `clients/agent-hub` boundary that calls
      `GET /api/v1/overview` and `GET /api/v1/fleets/:id` via `proxyFetch`,
      returning `Result<T, E>`.
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
