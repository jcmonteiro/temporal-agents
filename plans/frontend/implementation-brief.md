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
  native HTML elements + inline `style`, no shorthand props; icons are local
  SVGs imported via `vite-plugin-svgr`.
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
  components containing `<Link>`. Playwright for e2e is **optional** for this
  work and may be deferred.
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
- Observability (`observability/faro/`, Grafana Faro deps) is **optional**;
  default to leaving it out to reduce surface, but mirroring it is acceptable if
  kept behind an enable flag. This is an open decision (see §5).
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
handlers**, so the read adapter's sources are (a) the plan file(s) for the DAG
and (b) Temporal executions (parent `FleetWorkflow` + child `<fleetID>-<nodeID>`
+ standalone `PromptWorkflow`/develop runs) for live status. The hand-authored
manifest option is dropped — the plan file authored by `FleetPlanWorkflow` is the
source of intent.


- **Constraint:** UI components and pages must not read fixtures directly. Work
  data is reached through a **client boundary** under `src/clients/` (mirroring
  the reference's client + `proxyFetch` seam) exposing typed functions that
  return `Result<T, E>`. The current implementation of that boundary reads
  in-repo fixtures; a future implementation can call a real endpoint without
  changing callers.
- **Constraint:** the proxy/fetch layer keeps the reference's
  `/{service}/{path}` shape and the Vite dev proxy (`/api/v1` → backend target)
  so the Go read adapter plugs in without reshaping the client.
- **Constraint (Go read adapter, in scope — Slice 7):** a read-only HTTP
  endpoint (e.g. `GET /api/v1/overview`) served by a **new, additive** Go
  package (e.g. `internal/httpapi/` + a `serve`/`web` CLI subcommand or a flag
  on the worker). Hexagonal: it depends on a **driven port** that abstracts the
  Temporal query (list workflow executions → map to work items), with the
  Temporal SDK client as the adapter. It must **not** change worker/CLI
  behaviour, must be startable independently, and must serve the built frontend
  or run alongside the Vite dev proxy. The JSON contract mirrors `src/domain/`
  types so fixtures and live data are interchangeable.
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
  - child Failed/TimedOut/Terminated → `failed`
  Standalone `PromptWorkflow`/develop runs (no fleet) map by native status only
  (`in-progress`/`done`/`failed`).
- **"Up Next"** = the `todo`/`waiting` nodes. Match plan node ↔ execution by the
  `<fleetID>-<nodeID>` workflow-ID convention (named here per IB).
- Which reconstructions the first live slice implements vs defers (e.g.
  `waiting-input` needs failure-detail inspection) is a Slice 7 scoping call.
- **Domain types** (`src/domain/`) are the single source of truth for a work
  item, its status enum, groupings ("fleets"), progress, estimate, owner, and
  the "up next" queue. Fixtures and any future API both conform to these types.
- **Item kinds (Q5):** an overview satellite is one of two kinds — `fleet` or
  `workflow` (a standalone run from `run`/`schedule`/`code develop`). The type
  carries a `kind` discriminator. A `fleet` item's single status is an
  **aggregation** of its node statuses (aggregation rule is a stated decision —
  see §5); a `workflow` item's status is its native execution status. Only
  `fleet` items are navigable to the fleet view (which shows that fleet's node
  DAG — §4b, Q6=A).
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
- Rendering technology is **open**: inline SVG, absolutely-positioned DOM
  elements, or a mix are all acceptable. It must be testable under jsdom
  (assert item presence/labels/status and selection), which argues against a
  `<canvas>`-only approach — but the choice is the implementer's to justify.
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
  jsdom. Force-directed physics is optional polish, not required for the demo.
- **Right rail (honest fields only):** the fleet's goal/name, its status
  (aggregated §3), and **derived progress** (done nodes / total). A selected
  node shows its status and its child workflow execution. `owner`, `estimate`,
  and free-text `description` have **no source in PR #18** and are **not** part
  of the live model — they may appear in fixtures for design fidelity but are
  omitted under live data (never fabricated).
- Inter-fleet relationships and richer fleet metadata are a documented future
  backend feature (own brief), not built here.

## 5. Known risks, unknowns, open decisions

- **Fleet → single status aggregation rule (Q5)** — how a fleet's node statuses
  collapse to one satellite status. Suggested precedence: any `failed` →
  `failed`; else any `waiting-input`/`blocked` → `waiting-input`; else any
  `in-progress` → `in-progress`; else any `waiting`/`todo` → `in-progress` if
  some node done else `todo`; all `done` → `done`. Decide and record in Slice 2.
- **Graph rendering (fleet view)** — hand-rolled SVG vs a graph/force library
  for the fleet's node DAG. Trade-off: a library eases layout but adds a dep and
  jsdom-test friction; hand-rolled keeps the neutral-dependency stance (Q1).
  Decide in the fleet-view slice.
- **Orbit rendering approach** — SVG vs positioned DOM vs library. Trade-off:
  SVG gives precise geometry and easy dashed orbits; DOM gives easier CONNECT
  component embedding and accessibility. No mandate; pick and record the choice
  in the first orbit slice.
- **Custom UI layer scope** — with CONNECT dropped, the local `ui/` primitives
  (Button, Tag/Badge, Popover, Card, ProgressBar, Icon) must stay minimal and
  purpose-built for this app; avoid growing a general design system. Contain
  visualization CSS to the orbit component.
- **Observability** — Faro is LEGO/Grafana-tied; **dropped** along with `@lego`
  (confirm in scaffold slice). If lightweight error reporting is wanted later,
  add a neutral one behind the `ErrorBoundary`.
- **Package manager** — the reference uses `pnpm`. Not load-bearing here; pick
  one (`pnpm` recommended for parity) and use it consistently. Lockfile is
  committed; `node_modules` is git-ignored.
- **Responsive behaviour** at small widths for a fundamentally spatial view is
  unspecified by the concept; treat desktop-first, degrade rather than reflow.

## 6. Seams this work must not cross

- May **add** Go source (the read adapter + its port/tests) but must not change
  the behaviour of the existing worker, CLI commands, or workflows.
- Must not couple the Go build/test to Node tooling or vice versa (the read
  adapter is pure Go; serving the built assets is via embed or static dir, not a
  Node dependency).
- Must not read fixtures outside the `clients/` boundary.
- Must not introduce auth or token handling.
