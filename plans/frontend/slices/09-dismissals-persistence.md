# Slice 9 — Dismissible terminal satellites + Postgres persistence

**Discharges:** GC5 (terminal satellites persist until explicitly dismissed),
GC5a (dismissal is a durable server-side write, Postgres in `docker-compose.yml`),
IB §6 (additive Go; first mutation kept in its own port).

**Demo:** a `done` (or `failed`) satellite shows a **Dismiss** affordance;
dismissing it removes it from the Overview and the dismissal **survives a reload
and a different browser** (persisted in Postgres). Running/active satellites
never offer dismiss. `/runs` stops returning dismissed chains.

## Tasks

### Infra

- [ ] Add a **Postgres** service to `docker-compose.yml` (named volume for
      durability), alongside the existing Temporal service. Document env
      (`DATABASE_URL` / discrete vars) for `serve`.
- [ ] Add a minimal schema + migration for `dismissals` (identity key, kind,
      dismissed_at). Migrations run on `serve` startup or via a documented step.

### Go side (hexagonal, additive)

- [ ] Define a **driven `DismissalStore` port** (`IsDismissed`, `Dismiss`,
      `Undismiss`/list) separate from the read ports (GC5a — keep the single
      write isolated).
- [ ] Implement the **Postgres adapter** for the port; wire it into `serve`.
- [ ] Add write endpoints under `/api/v1`: `POST /api/v1/dismissals`
      (body: item identity + kind) and `DELETE /api/v1/dismissals/:id`. These
      are the **only** mutations; still loopback-bound by default (GC4).
- [ ] `/runs` (Slice 7) filters out dismissed chains via `DismissalStore`;
      dismissal is keyed by the **stable per-chain identity** (GC5). Fleets and
      schedules may use the same store keyed by fleet/schedule ID if dismissible.
- [ ] Tests: port has an in-memory fake for handler tests; Postgres adapter
      covered per the repo's adapter-testing approach; assert dismissed items are
      excluded from `/runs`. `go build .` + existing Go tests/CI stay green.

### Frontend side

- [ ] Add a **Dismiss** action on terminal (`done`/`failed`) satellites (and/or
      the Selected panel) that calls `POST /api/v1/dismissals` via the
      `clients/agent-hub` boundary, returning `Result<T, E>`; re-fetch/refresh on
      success. No dismiss affordance for non-terminal satellites.
- [ ] Fixtures/offline path: a fixture-backed dismissal store so the affordance
      demos without the API (no component changes when switching to live).
- [ ] Test: dismissing a terminal satellite removes it; non-terminal satellites
      expose no dismiss; failure surfaces through the `Result` branch.

## Done when

Dismissing a terminal satellite durably hides it via a Postgres-backed write
endpoint, Postgres runs in compose, running/active satellites are never
dismissible, and no existing Go command changed behaviour.
