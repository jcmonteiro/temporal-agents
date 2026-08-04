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
- **Design system:** LEGO CONNECT — `@lego/connect-components-react`,
  `@lego/connect-theme-enterprise` (theme is a **CSS import**, not a React
  provider), `@lego/connect-utilities`, `@lego/icons`. **Light theme only.**
  **No layout primitives** — layout is native HTML elements + inline `style`;
  no Chakra-style shorthand props.
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

### Explicitly dropped from the reference (per brief scope)

- All of `auth/` (MSAL, providers, login, require-auth/role) — removed, not
  stubbed. `router.tsx` has no auth guards; `proxyFetch` does not attach tokens.
- Auth-derived `Config` fields (authority, clientId, redirect URIs, proxy auth).
- Observability (`observability/faro/`, Grafana Faro deps) is **optional**;
  default to leaving it out to reduce surface, but mirroring it is acceptable if
  kept behind an enable flag. This is an open decision (see §5).
- Domain-specific reference code (SKU/dispensation/inventory tables, TanStack
  table/virtual, OData) — not carried over; Agent Hub has its own domain.

## 3. The data seam (constraints, design left open)

No HTTP API exists in this repo today — the Go side is a Temporal worker + CLI.
The brief allows a stand-in data source, so:

- **Constraint:** UI components and pages must not read fixtures directly. Work
  data is reached through a **client boundary** under `src/clients/` (mirroring
  the reference's client + `proxyFetch` seam) exposing typed functions that
  return `Result<T, E>`. The current implementation of that boundary reads
  in-repo fixtures; a future implementation can call a real endpoint without
  changing callers.
- **Constraint:** the proxy/fetch layer keeps the reference's
  `/{service}/{path}` shape and the Vite dev proxy (`/api/v1` → backend target)
  so a later Go read-adapter can be introduced without reshaping the client.
  (Providing that Go adapter is **out of scope** here.)
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
- **CONNECT + custom visualization fit** — the design system has no orbit/graph
  primitive and no layout primitives. Risk that heavy custom CSS is needed;
  contain it to the visualization component and keep chrome (top bar, nav, rail,
  cards) on CONNECT components.
- **Observability** — keep Faro or drop it (§2). Decide in the scaffold slice;
  default drop.
- **Package manager** — the reference uses `pnpm`. Not load-bearing here; pick
  one (`pnpm` recommended for parity) and use it consistently. Lockfile is
  committed; `node_modules` is git-ignored.
- **Responsive behaviour** at small widths for a fundamentally spatial view is
  unspecified by the concept; treat desktop-first, degrade rather than reflow.

## 6. Seams this work must not cross

- Must not modify Go source, the worker, the CLI, or their behaviour.
- Must not couple the Go build/test to Node tooling or vice versa.
- Must not read fixtures outside the `clients/` boundary.
- Must not introduce auth or token handling.
