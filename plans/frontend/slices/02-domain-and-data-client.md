# Slice 2 — Domain model + data client + fixture-fed list

**Discharges:** IB §3 (data seam), IB §2 (Result-based error handling, testing).

**Demo:** the Overview page loads work items **through the client boundary**
(not by importing fixtures) and renders them as a simple list (**no greeting** —
Q10), each row showing its label and a status indicator in the correct status
color. Loading and error states are visible.

## Tasks

- [ ] Define `src/domain/types.ts`: `WorkItem` (id, label, `kind: 'fleet' |
      'run' | 'schedule'` per Q5/Q19, icon kind, status, fleet, progress), the
      `WorkStatus` union
      (`todo | in-progress | paused | waiting-input | waiting | done | failed`),
      `Fleet` (incl. its **plan**: the node DAG — nodes with predecessor links,
      the source of derived `todo`/`waiting`, "Up Next", and the fleet-view
      graph, per Q3/Q5), and the "up next" item shape (IB §3). Mirror the
      `internal/fleet` shapes (`FleetPlan`/`FleetNode`/`NodeStatus`).
- [ ] The full seven-status enum stays in the domain even though the live feed
      only emits a subset (Q3=A). **Fleet status aggregation is owned by the
      backend API (Q15)** — the client consumes the already-aggregated fleet
      status/progress, it does not compute them. No `owner` field (Q10).
- [ ] Define status color tokens once in `src/styles/theme.ts` keyed by
      `WorkStatus`, consumed by list + (later) legend + orbit (IB §3).
- [ ] Add `src/clients/proxy-fetch.ts` mirroring the reference `/{service}/{path}`
      shape and dev proxy, **without** token/auth headers (IB §2 dropped, §3).
- [ ] Add `src/clients/agent-hub/` client exposing typed functions returning
      `Result<T, E>` for the resource endpoints `/api/v1/fleets`, `/api/v1/runs`,
      `/api/v1/schedules` (Q19), composing the overview (items + up-next) from
      them. Payload types are DB-agnostic/portable (Q19). Current implementation
      reads in-repo fixtures; the seam is shaped so the real endpoints replace it
      without changing callers (IB §3).
- [ ] Add fixtures in `src/test/fixtures.ts` (and/or `pages/overview/mock-data.ts`
      matching the reference's `mock-data.ts` convention) covering all seven
      statuses, including the concept's items (Q2 Launch Campaign, Design System
      v2, User Research, Analytics Pipeline, Data Migration, Website Redesign).
- [ ] Add `src/pages/overview/` with `page.tsx`, a `use-overview-data.ts` hook
      that calls the client and exposes loading/error/data, `types.ts`, and
      `page.test.tsx`.
- [ ] Render a temporary list of items with status dots (visualization comes in
      slice 3). Show a loading indicator and an error message via the `Result`
      failure branch.
- [ ] Test: hook returns mapped items on success and surfaces failure; page
      shows all items with correct status colors and renders the error branch.

## Done when

The Overview page displays fixture-backed work items via the client boundary,
with correct per-status colors and observable loading/error states, and no
component imports fixtures directly.
