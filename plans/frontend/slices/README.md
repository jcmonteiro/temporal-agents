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
| 6 | [Secondary destinations + top-bar affordances](./06-secondary-destinations.md) | Nav links resolve to placeholder pages; search + notifications present |

## Coverage of the implementation brief

- IB §1 (Go coexistence): slice 1.
- IB §2 (stack + structure mirror, auth dropped): slice 1, extended per-layer by 2–6.
- IB §3 (data seam): slice 2.
- IB §4 (orbit visualization): slices 3, 4, 5.
- IB §5 (open decisions — rendering, Faro, pkg manager): recorded in slices 1 and 3.
- IB §6 (seams not to cross): honored by every slice; asserted in slice 1's CI.
