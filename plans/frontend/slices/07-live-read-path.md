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
      (`ListWorkflowExecutions` / `DescribeWorkflowExecution`), mapping
      execution status + the chosen status source (search attribute, memo, or a
      workflow query — **decide and document here**) to the seven-value
      `WorkStatus`. Name how `waiting-input`, `paused`, `waiting` are derived
      (IB §3 mapping constraint).
- [ ] Add an HTTP handler serving `GET /api/v1/overview` returning JSON that
      matches the frontend `src/domain/` types (items + up-next).
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
      `GET /api/v1/overview` via `proxyFetch`, returning `Result<T, E>`.
- [ ] Select fixtures vs live via config/env (`Config` flag) so tests and
      offline dev keep using fixtures; no component changes (IB §3).
- [ ] Test: live client maps a sample payload to domain types and surfaces
      HTTP/parse failures through the `Result` failure branch.

## Done when

Running the `serve` command with a live worker shows real workflow executions in
the Orbit with correct statuses, the JSON contract matches the domain types, the
fixture path still works for offline/test, and no existing Go command changed
behaviour.
