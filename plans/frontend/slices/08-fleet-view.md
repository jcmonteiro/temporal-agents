# Slice 8 — Fleet view (fleet node DAG)

**Discharges:** brief outcome (dedicated fleet view), IB §4.0 (shared canvas
primitive — Q8=A, second consumer), IB §4b (fleet's node DAG, no cross-fleet,
honest fields — Q6=A), IB §5 (graph-rendering decision).

**Demo:** the left-nav **Fleets** destination (and "View Details" on a fleet
satellite in Slice 4) opens a page titled "Fleets / Explore and manage your
fleets." showing a **node-link graph of one fleet's own node DAG**: each node
(a `FleetNode` feature) has a monogram, label, and status dot; edges are
`DependsOn` dependencies; the selected node has a distinct ring. The right rail
shows **Selected Fleet** (name/goal, aggregated status, derived progress bar)
and the fleet's **nodes / child workflows**. Same top bar, nav, state legend,
and zoom/recenter controls as Overview. Fixtures-backed first, then live via
Slice 7. **No cross-fleet "Connected Fleets" concept.**

## Tasks

- [ ] Decide + record the graph rendering approach (hand-rolled SVG vs library),
      keeping the neutral-dependency stance (IB §5, Q1).
- [ ] Add route `fleets` (and `fleets/:id`) in `router.tsx`; lazy
      `src/pages/fleets/page.tsx`. Wire the left-nav Fleets link and Slice 4's
      "View Details" to it.
- [ ] Reuse the **shared canvas primitive** (Slices 3+5, Q8=A) with a new `dag`
      layout: a deterministic pure function `(nodes, edges) → positions` over the
      fleet's `FleetNode`/`DependsOn` DAG, testable under jsdom; force-directed
      physics is optional polish. Pan/zoom/select/starfield/controls come from
      the primitive, not re-implemented. If the second consumer reveals gaps in
      the primitive's interface, refactor it here (IB §4.0).
- [ ] Render nodes (monogram + label + status dot from the shared status token),
      `DependsOn` edges, and a selected-node ring; clicking a node updates the
      right rail with that node's status + its child workflow execution.
- [ ] Add the right rail: `selected-fleet` (name/goal, aggregated status,
      derived progress = done/total) and the fleet's node/child-workflow list,
      reusing the state-legend + canvas-controls components from Overview.
      **No `connected-fleets` panel.** `owner`/`estimate`/`description` are not
      in the live model (Q6=A) — omit, or render only when present in fixtures.
- [ ] Fixtures for a fleet's node DAG covering all statuses; fed through the
      `clients/agent-hub` boundary.
- [ ] Test: graph renders every node with label + correct status; `DependsOn`
      edges present; selecting a node updates the panel; derived progress
      correct; deterministic layout asserted.

## Done when

The Fleets route renders a selectable graph of a fleet's node DAG with its right
rail, reachable from the nav and from a fleet satellite's "View Details", backed
by fixtures and (via Slice 7) live fleet data — with no cross-fleet concept and
no fabricated metadata.
