# Vertical slices — Agent Hub frontend

Each slice closes a thin end-to-end path and is demoable on its own. Ordered by
dependency. Every slice names the implementation-brief constraints it discharges
(referenced as IB §n).

| # | Slice | Demo |
|---|-------|------|
| 1 | [Scaffold + app shell](./01-scaffold-and-shell.md) | App boots at `web/`, top bar + left nav render, Overview route active, Go build/CI unaffected |
| 2 | [Domain + data client + fixture-fed list](./02-domain-and-data-client.md) | Overview lists real work items (from client boundary) with status colors |
| 3 | [Orbit visualization](./03-orbit-visualization.md) | Work items render orbiting a center, status readable at a glance |
| 4 | [Selection + right rail](./04-selection-and-right-rail.md) | Click an item → legend, selected details, and "up next" populate |
| 5 | [Canvas controls](./05-canvas-controls.md) | Zoom %, zoom in/out, and recenter work on the orbit |
| 6 | [Secondary destinations + top-bar affordances](./06-secondary-destinations.md) | Workflows/Templates/Insights/Settings placeholders; search + notifications present |
| 7 | [Live read-path](./07-live-read-path.md) | Go read adapter serves real fleets + workflows; Orbit + fleet view show live data |
| 8 | [Fleet view (node DAG)](./08-fleet-view.md) | Fleets route renders a selectable graph of one fleet's node DAG + right rail; reached via nav and "View Details" |

Slices 1–6 and 8 are pure-frontend and fixtures-backed — each demoable offline.
Slice 7 adds the Go read adapter and swaps the client to live data behind the
same boundary (feeding both Overview and the fleet view).

Satellites (Q5) are top-level works: a **fleet** (aggregated status, navigable
to the fleet view) or a **standalone workflow** (`run`/`schedule`/`code
develop`). The Overview is an orbit; the fleet view is one fleet's node DAG
(no cross-fleet concept — Q6=A).

## Coverage of the implementation brief

- IB §1 (Go coexistence): slice 1.
- IB §2 (stack + structure mirror, auth + @lego dropped, themeable dark): slice 1, extended per-layer by 2–6.
- IB §3 (data seam): slice 2 (boundary + fixtures), slice 7 (Go read adapter + live client).
- IB §4.0 (shared canvas primitive, Q8=A): built in slice 3+5, reused/refined in slice 8.
- IB §4a (orbit): slices 3, 4, 5. IB §4b (fleet node DAG, no cross-fleet — Q6=A): slice 8.
- Q5 (satellite kinds, aggregation, fleet-view navigation): slices 2, 4, 7, 8.
- Q7 (PR #18 assumed merged): slice 7 only; slices 1–6 + 8 are independent of it.
- IB §5 (open decisions — rendering, pkg manager): recorded in slices 1 and 3.
- IB §6 (seams not to cross): honored by every slice; asserted in slice 1's CI and slice 7 (additive Go only).
