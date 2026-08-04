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
boundary, so:

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
- **Mapping constraint (Q3 = A, honest subset):** the live adapter emits only
  the statuses the backend can actually source. Native Temporal execution
  status maps as: Running / ContinuedAsNew → `in-progress`; Completed → `done`;
  Failed / TimedOut / Terminated / Canceled → `failed`. `waiting-input` and
  `paused` have **no source today** and are **not** emitted by the live adapter
  (they remain in the enum/legend/fixtures for completeness and future use). Do
  not invent statuses the backend cannot produce, and do **not** instrument the
  existing workflows to fabricate them (that is a separate future feature).
- **Plan-derived `todo` / `waiting` (Q3 refinement):** a **fleet** carries a
  **plan** — the ordered set of workflows it intends to run. The overview is the
  reconciliation of a fleet's plan against live Temporal executions: a planned
  workflow with no execution yet is `todo` if it is ready to start (its plan
  predecessors are `done`) or `waiting` if it is still blocked by an unfinished
  predecessor; planned workflows that have executions take their mapped live
  status. "Up Next" is the set of `todo`/`waiting` planned workflows. The
  **source of the plan/fleet definition is an open decision (Q4).**
- Matching a planned workflow to its Temporal execution needs a stable key
  (workflow ID convention or a plan-step identifier). The chosen key must be
  named in Slice 7.
- **Domain types** (`src/domain/`) are the single source of truth for a work
  item, its status enum, groupings ("fleets"), progress, estimate, owner, and
  the "up next" queue. Fixtures and any future API both conform to these types.
- The status vocabulary is fixed by the concept: `todo`, `in-progress`,
  `paused`, `waiting-input`, `waiting`, `done`, `failed` (the concept's
  "Blocked" maps to `failed`). Each status has one color, defined once as a
  token and consumed by both the legend and the orbit dots.

## 4. The orbit visualization (constraints, rendering left open)

This is the novel piece with no reference precedent. Constraints:

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

## 5. Known risks, unknowns, open decisions

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
