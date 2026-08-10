# Implementation brief — Agent Hub frontend

Derived from `brief.md`. Describes the constraints any valid implementation must
honor and the seams it touches. It does not prescribe one design; where a
concrete choice is mandated, it is called out as a **hard constraint** with its
reason.

## 1. Coexistence with the Go application (hard constraints)

The frontend must live in this same repository without colliding with the Go
module (`temporal-agents`), the CLI/worker at the repo root, the git hooks, or
CI. Facts that force this:

- The repo root is a Go `main` package (`go build .`, `go install .`). Go
  tooling walks packages, not arbitrary directories, but `internal/` is a Go
  keyword directory — **the frontend must not live under `internal/`**.
- The pre-commit hook (`.githooks/pre-commit`) only formats staged `*.go` files.
  It will not touch frontend files. Do not extend it to run frontend tooling in
  a way that blocks commits of Go-only changes.
- CI today is `.github/workflows/go.yml`. Frontend CI must be additive (its own
  workflow) and must not make the Go workflow depend on Node, nor vice versa.
- `.gitignore` currently ignores only the built binary. Frontend build/output
  and dependency directories must be added to `.gitignore` so they are never
  committed and never confused with Go artifacts.

**Constraint:** the frontend lives in a single top-level directory. Recommended
name: `web/` (unambiguously not Go, not `internal/`, not the existing binary
name). `frontend/` is an acceptable alternative. Everything frontend — package
manifest, build config, source, tests — is self-contained inside it.

**Constraint (mandated by the brief's "discard the monorepo structure"):** the
chosen directory is the frontend package root directly (manifest at
`web/package.json`, source at `web/src/`). Do **not** reproduce the reference's
nested `frontend/frontend` wrapper.

## 2. Tech stack and structure to mirror (hard constraints)

The brief requires following the tech stack and structure of the reference
frontend (`~/Documents/DesignHub/frontend/frontend/`), minus auth and minus its
monorepo nesting. The following are therefore requirements, not suggestions:

- **Build/tooling:** Vite with `root: src` and `outDir: ../dist`; TypeScript
  (`tsc --build`); Biome for lint+format (`biome check`); the same script names
  where they apply (`dev`, `build`, `lint`, `test`).
- **Runtime libraries:** React 19 + `react-dom`; `react-router` (browser router
  with lazy-loaded route modules and a router error boundary).
- **UI layer (diverges from reference — see decision below):** the `@lego/*`
  CONNECT packages are **dropped entirely**. They resolve from LEGO's private
  GitHub Packages registry (org membership + `read:packages` token) and would
  make this repo unbuildable outside LEGO and complicate CI, with no benefit to
  the Orbit concept. Replace them with a **neutral, public** UI layer: a small
  set of local primitive components (`src/components/ui/`) styled with plain
  CSS / CSS custom properties. **No third-party design-system runtime dep.**
- **Design conventions kept as house rules** (they were the valuable part of the
  reference, independent of CONNECT): **no layout primitives** — layout is
  native HTML elements + inline `style`, no shorthand props.
- **Icons (Q9 = A):** one **public icon library** (Lucide-style React icons) is
  the source for satellite glyphs and chrome icons; `vite-plugin-svgr` is kept
  for bespoke marks (logo / planet). The `WorkItem` icon field maps to library
  icon names.
- **Theming (hard constraint):** ship a **dark theme by default**. There is
  **no theme switcher** now, but all color/spacing/typography values must be
  **design tokens as CSS custom properties** on a single root scope (e.g.
  `:root[data-theme="dark"]`), never hard-coded in components. Adding a light
  theme (and a switcher) later must be a matter of defining a second token set
  and toggling the scope attribute — no component changes. Status colors
  (IB §3) are part of this token set.
- **Error handling:** `Result<T, E>` from `utils/result.ts` for fallible
  operations; async wrapped in try/catch; route-level `RouterErrorBoundary`.
- **Testing:** Vitest + jsdom + `@testing-library/react` for unit/component
  tests (`describe`/`it`/`expect`/`vi`, `render`/`screen`/`waitFor`/`within`,
  `userEvent`); fixtures in `test/fixtures.ts`; a `renderWithRouter` helper for
  components containing `<Link>`. **No Playwright / e2e for now (Q21)** — unit
  and component tests only.
- **Package manager (Q12 = pnpm):** `pnpm`, for parity with the reference.
  Lockfile committed; `node_modules` git-ignored.
- **No personalization (Q10 = B):** auth is gone and there is **no operator
  identity**. The greeting line is **removed entirely**; the avatar is static;
  item-level `owner` is not part of the model (backend does not expose it — see
  Q6). No greeting, no configured operator name.
- **React patterns:** functional components with explicit `(): ReactNode`
  return type; no class components.
- **Directory layout inside the package** mirrors the reference layers:
  `src/{clients,components,config,domain,hooks,navigation,pages,styles,utils,test}`
  plus `app.tsx`, `index.tsx`, `router.tsx`, `index.html`.

### Explicitly dropped from the reference (per brief scope + Q1 decision)

- All of `auth/` (MSAL, providers, login, require-auth/role) — removed, not
  stubbed. `router.tsx` has no auth guards; `proxyFetch` does not attach tokens.
- Auth-derived `Config` fields (authority, clientId, redirect URIs, proxy auth).
- All `@lego/*` packages and the private-registry `.npmrc` scope, the CONNECT
  theme CSS imports, and CONNECT-specific test quirks.
- Observability (`observability/faro/`, Grafana Faro deps) — **dropped** with
  `@lego`. Errors surface via the `ErrorBoundary` only.
- Playwright/e2e harness (Q21) — not carried over.
- Domain-specific reference code (SKU/dispensation/inventory tables, TanStack
  table/virtual, OData) — not carried over; Agent Hub has its own domain.

## 3. The data seam (constraints, design left open)

No HTTP API exists in this repo today — the Go side is a Temporal worker + CLI.
The brief now requires a live read-only feed (Q2 = B), delivered by a Go read
adapter (Slice 7). Development still proceeds fixtures-first behind the same
boundary.

**The plan/fleet concept is real (Q4):** PR #18 (`internal/fleet/`) adds
`FleetPlanWorkflow` (goal → `FleetPlan` DAG, written to `fleet-plan.json`) and
`FleetWorkflow` (executes the DAG in topological layers, one child
`codereview.DevelopWorkflow` per node, child workflow IDs `<fleetID>-<nodeID>`).
Domain: `FleetPlan{Goal, Nodes[]}`, `FleetNode{ID, Prompt, DependsOn[]}`,
`NodeStatus{succeeded, failed, blocked, skipped}`. There are **no query
handlers**.

**Plan source (Q4/GC1 = A):** the approved `FleetPlan` is **carried in each
`FleetWorkflow`'s start input** (that is how `fleet execute` runs it). The read
adapter recovers each fleet's plan from its **workflow start input/history**,
keyed by fleet ID — **not** from an ambient `fleet-plan.json` (which is a
user-chosen, `--out`-configurable review artifact that may be absent, stale,
overwritten, or belong to a different fleet). This lookup sits behind a
**"plan store" port** (`PlanFor(fleetID) → FleetPlan`); the first implementation
decodes the workflow start input, and a future **Postgres-backed plan store**
(see GC5a) swaps in without changing callers.

Live-status sources are the Temporal executions: parent `FleetWorkflow` + child
`<fleetID>-<nodeID>` + standalone `PromptWorkflow`/develop runs + schedules. The
hand-authored manifest option is dropped.


- **Constraint:** UI components and pages must not read fixtures directly. Work
  data is reached through a **client boundary** under `src/clients/` (mirroring
  the reference's client + `proxyFetch` seam) exposing typed functions that
  return `Result<T, E>`. The current implementation of that boundary reads
  in-repo fixtures; a future implementation can call a real endpoint without
  changing callers.
- **Constraint:** the proxy/fetch layer keeps the reference's
  `/{service}/{path}` shape and the Vite dev proxy (`/api/v1` → backend target)
  so the Go read adapter plugs in without reshaping the client.
- **Constraint (REST resources, Q19):** the API exposes **resource endpoints**
  `GET /api/v1/fleets`, `GET /api/v1/runs`, `GET /api/v1/schedules` (and
  `GET /api/v1/fleets/:id` for a fleet's node DAG). The Overview composes its
  satellites from these (fleets + runs + schedules). **Payloads must be
  portable** — defined by the backend contract, DB-agnostic — so a future switch
  from workflow-id reconstruction to a real database changes only the adapter,
  not the contract or the frontend.
- **Constraint (`/runs` visibility + chain identity, GC5):** `/runs` shows all
  **running** runs plus **terminal** runs that have **not been dismissed**
  (there is **no time-based window**). A continue-as-new chain collapses to
  **one satellite** with a **stable per-chain identity** (the chain's original
  workflow ID), showing the latest iteration's status — never one satellite per
  retained execution. Results are server-capped; not a full history browser. A
  terminal `done` (and `failed`) satellite persists until the operator
  **explicitly dismisses** it (dismissal store, GC5a).
- **Constraint (`/schedules` identity + status, GC2 = A):** one satellite
  **per schedule** (identity = schedule ID). Status: `paused` when the schedule
  is paused; `in-progress` when an action is currently running; else the outcome
  of the most recent completed action (`done`/`failed`); `todo` when it has
  never run. **No progress** for schedules. This mapping is part of the portable
  contract.
- **Constraint (Go read adapter, in scope — Slice 7):** read-only HTTP endpoints
  served by a **new, additive** Go package (e.g. `internal/httpapi/`) via a
  `serve` CLI subcommand (Q17). Hexagonal: it depends on a **driven port** that
  abstracts the query (list fleets/runs/schedules → map to work items), with the
  Temporal SDK client as the adapter; the **first implementation uses the
  workflow-id convention** (Q19), replaceable by a DB-backed adapter later. It
  must **not** change worker/CLI behaviour and must be startable independently.
- **Constraint (asset hosting, Q18):** the SPA is built to **independently
  hostable static assets**. The API serves only JSON under `/api/v1` (no
  coupling to the assets); `serve` may serve the static bundle locally for
  convenience, but the architecture must allow the same bundle to be fronted by
  **S3 + a CDN** later without changing the API. Configurable base path; no
  hard embed that couples assets to the API binary lifecycle.
- **Constraint (network binding, GC4):** because the API is unauthenticated and
  exposes workflow goals/prompts, `serve` **binds to loopback (`127.0.0.1`) by
  default**; any non-loopback bind is an **explicit opt-in** (e.g. `--addr`).
  This preserves the trusted-local-operator boundary by construction.
- **Constraint (dismissal persistence, GC5a):** dismissing a terminal satellite
  is a **write** persisted server-side in **Postgres** (added to
  `docker-compose.yml`). A `POST`/`DELETE` dismissals endpoint under `/api/v1`
  and a **driven "dismissal store" port** (Postgres adapter) own it; dismissed
  items are keyed by the stable per-chain / fleet / schedule identity. This is
  the first mutation in an otherwise read surface; keep reads and this write in
  separate ports. The same Postgres instance is available to back the plan store
  (GC1) later.
- **Status mapping (Q3 = A honest subset, enriched by the real fleet domain):**
  the adapter emits only statuses reconstructable from (plan DAG + executions),
  never fabricated by instrumenting workflows. Per fleet node:
  - no child execution, a dependency not yet succeeded → `waiting`
  - no child execution, all dependencies succeeded (runnable) → `todo`
  - no child execution, a dependency failed/skipped → `paused` (≈ `skipped`)
  - child Running/ContinuedAsNew → `in-progress`
  - child Completed (succeeded) → `done`
  - child returned `SeedConflictBlocked` (recoverable, needs a human) →
    `waiting-input` (≈ `blocked`); requires reading the child failure detail
  - child Failed/TimedOut/Terminated/**Canceled** → `failed`
  Standalone `PromptWorkflow`/develop runs (no fleet) map by native status:
  Running/ContinuedAsNew → `in-progress`; Completed → `done`;
  Failed/TimedOut/Terminated/Canceled → `failed`.
- **"Up Next"** = the `todo`/`waiting` nodes. Match plan node ↔ execution by the
  `<fleetID>-<nodeID>` workflow-ID convention (Q19 first implementation).
- `waiting-input` (≈ `blocked`) reconstruction is **deferred** in the first pass
  (Q16) — it needs child-failure-detail inspection.
- **Domain types** (`src/domain/`) are the single source of truth for a work
  item, its status enum, and groupings ("fleets"). Fixtures and the API both
  conform. Fields the backend does not expose (e.g. `owner`) are not modelled.
- **Item kinds (Q5/Q19):** an overview satellite has a `kind` discriminator —
  `fleet`, `run`, or `schedule` (matching the three resource endpoints). Only
  `fleet` items are navigable to the fleet view (§4b, Q6=A).
- **Fleet status aggregation is the backend's job (Q15), with an exact rule
  (GC3):** the API returns a fleet's already-aggregated status and progress; the
  frontend does **not** compute them. The rule is a **fixed precedence**
  (first match wins), defined in the portable contract and unit-tested in the Go
  adapter (Slice 7):
  1. no nodes → `todo`
  2. any `failed` → `failed`
  3. any `waiting-input` (blocked) → `waiting-input`
  4. any `in-progress` → `in-progress`
  5. any `paused` (skipped) → `paused`
  6. all `done` → `done`
  7. otherwise → `in-progress` if any node is `done`, else `todo`
  **Progress** = `done / total` over all plan nodes (`skipped`/`blocked` count
  in the denominator, not the numerator).
- The status vocabulary is fixed by the concept: `todo`, `in-progress`,
  `paused`, `waiting-input`, `waiting`, `done`, `failed` (the concept's
  "Blocked" maps to `failed`). Each status has one color, defined once as a
  token and consumed by both the legend and the orbit dots.

## 4. The visualizations (constraints, rendering left open)

Two custom views, no reference precedent.

### 4.0 Shared canvas primitive (Q8 = A, hard constraint)

Both views share **one reusable canvas/viewport primitive** rather than two
implementations. It owns the cross-cutting behaviour — pan/zoom view transform,
node selection, starfield background, and the zoom/recenter controls — and is
parameterized by (a) a **layout function** (`orbit` for Overview, `dag` for the
fleet view) and (b) a **node renderer**. View-specific code supplies only the
layout, the node/edge rendering, and the right-rail wiring. The layout functions
stay pure and deterministic (§4a/§4b). The legend, status-dot, and status token
are likewise shared, not duplicated per view.

### 4a. Overview — orbit

Constraints:

- The layout is a **pure function of the work-item list**: given N items and a
  center, item positions are deterministic and reproducible (same input →
  same layout), so tests can assert placement and the view is stable across
  renders.
- Items distribute around one or more concentric orbits around a central body;
  **no two items overlap** such that a status becomes unreadable (a brief
  success signal). Orbit assignment/spacing must degrade gracefully as N grows.
- Each orbiting item shows an icon, a label, and its status indicator using the
  shared status color token.
- An item is **selectable**; selection is observable (drives the detail rail)
  and has an accessible affordance (keyboard-focusable, name exposed).
- The canvas supports **zoom** (with a readable percentage) and **recenter**,
  matching the concept's bottom-left controls. Zoom is a view transform, not a
  data change.
- Rendering technology is **deferred to the slice implementation (Q13)**. The
  definition of done for the chosen approach: **reactive, clickable,
  (future) draggable, animated, and low host-resource usage**, and testable
  under jsdom (assert item presence/labels/status and selection).
- Selecting a satellite fills the right rail (State Legend, Selected, Up Next).
  For a `fleet` item, "View Details" navigates to the **fleet view** route; for
  a `workflow` item it is inert (no dedicated view in scope).

### 4b. Fleet view — the fleet's node DAG (Q6 = A)

A dedicated route (the left-nav "Fleets" destination) rendering a **node-link
graph of one fleet's own node DAG**: nodes are the fleet's `FleetNode`s
(features), edges are their `DependsOn` dependencies. **No cross-fleet concept**
— "Connected Fleets" from the concept image is dropped, because PR #18 models no
relationship between fleets. Constraints:

- Nodes render with a short monogram + label + status dot (shared status token);
  edges are `DependsOn`; selection drives the right rail.
- Same chrome as Overview (top bar, nav, legend, zoom/recenter controls).
- Layout is deterministic enough to test node/edge presence and selection under
  jsdom. Rendering technology is **deferred to the slice (Q14, same DoD as
  Q13):** reactive, clickable, future-draggable, animated, low host-resource
  usage. Force-directed physics is optional polish, not required for the demo.
- **Right rail (honest fields only):** the fleet's goal/name, its status
  (aggregated §3), and **derived progress** (done nodes / total). A selected
  node shows its status and its child workflow execution. `owner`, `estimate`,
  and free-text `description` have **no source in PR #18** and are **not** part
  of the live model — they may appear in fixtures for design fidelity but are
  omitted under live data (never fabricated).
- Inter-fleet relationships and richer fleet metadata are a documented future
  backend feature (own brief), not built here.

## 5. Known risks, unknowns, open decisions

**Resolved (recorded here, implemented in slices):** icons = public library
(Q9); no personalization / no greeting (Q10); dir = `web/` (Q11); pnpm (Q12);
rendering tech deferred to slices with the reactive/clickable/future-draggable/
animated/low-resource DoD (Q13/Q14); fleet status aggregation owned by the
backend API (Q15); `waiting-input`/`blocked` deferred (Q16); `serve` subcommand
(Q17); assets independently hostable for future S3+CDN (Q18); resource endpoints
`/api/v1/fleets|runs|schedules` with portable, DB-agnostic payloads, workflow-id
convention first (Q19); search + notifications present but disconnected
(Q20/Q22); no Playwright (Q21).

**Resolved from Copilot review (GC1–GC5a):** plan recovered from `FleetWorkflow`
start input behind a plan-store port (GC1); `/schedules` = schedule identity +
latest-action status, no progress (GC2); exact fleet aggregation precedence +
progress (GC3); `serve` binds **loopback by default**, non-loopback is opt-in
(GC4); `/runs` = one satellite per continue-as-new chain, no time window,
terminal satellites persist until explicitly dismissed (GC5); **dismissals are
persisted in Postgres** via a backend write endpoint, with Postgres added to
`docker-compose.yml` (GC5a).

Remaining genuinely open:

- **Custom UI layer scope** — the local `ui/` primitives (Button, Tag/Badge,
  Popover, Card, ProgressBar, Icon) must stay minimal and purpose-built; avoid
  growing a general design system. Contain visualization CSS to the canvas.
- **Responsive behaviour** at small widths for a fundamentally spatial view is
  unspecified by the concept; treat desktop-first, degrade rather than reflow.

## 6. Seams this work must not cross

- May **add** Go source (the read adapter, the dismissal write endpoint, their
  ports/adapters/tests) and add **Postgres** to `docker-compose.yml`, but must
  not change the behaviour of the existing worker, CLI commands, or workflows.
- Must not couple the Go build/test to Node tooling or vice versa (the read
  adapter is pure Go; the SPA bundle is independently hostable static assets —
  Q18 — not a Node dependency of the Go binary).
- Must not read fixtures outside the `clients/` boundary.
- Must not introduce auth or token handling.
