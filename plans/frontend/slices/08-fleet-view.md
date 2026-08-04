# Slice 8 — Fleet view (relationship graph)

**Discharges:** brief outcome (dedicated fleet view), IB §4b (fleet-view graph +
backend-gap honesty), IB §5 (graph-rendering decision).

**Demo:** the left-nav **Fleets** destination (and "View Details" on a fleet
satellite in Slice 4) opens a page titled "Fleets / Explore and manage your
fleets." showing a **node-link graph** of a fleet and its members: each node has
a monogram, label, and status dot; edges show relationships; the selected fleet
has a distinct ring. The right rail shows **Selected Fleet** (progress bar,
estimate, owner, description), **Connected Fleets**, and **Related Workflows**
with a "View All Workflows" affordance. Same top bar, nav, state legend, and
zoom/recenter controls as Overview. Fixtures-backed first, then live via Slice 7.

## Tasks

- [ ] Decide + record the graph rendering approach (hand-rolled SVG vs library),
      keeping the neutral-dependency stance (IB §5, Q1).
- [ ] Add route `fleets` (and `fleets/:id`) in `router.tsx`; lazy
      `src/pages/fleets/page.tsx`. Wire the left-nav Fleets link and Slice 4's
      "View Details" to it.
- [ ] Add `src/components/graph/` (or under the page): a deterministic node-link
      layout (pure function `(nodes, edges) → positions`) testable under jsdom;
      force-directed physics is optional polish.
- [ ] Render nodes (monogram + label + status dot from the shared status token),
      edges, and a selected-node ring; clicking a node updates the right rail.
- [ ] Add the right rail: `selected-fleet` (progress, estimate, owner,
      description), `connected-fleets`, `related-workflows` (+ "View All"),
      reusing the state-legend + canvas-controls components from Overview.
- [ ] Fixtures for a fleet graph covering all statuses (mirror the fleet-view
      concept image); fed through the `clients/agent-hub` boundary.
- [ ] Honesty rule (IB §4b): fields/edges with no backend source (cross-fleet
      "Connected Fleets", owner/estimate/description) are shown from fixtures but
      omitted/marked unavailable under live data — never fabricated.
- [ ] Test: graph renders every node with label + correct status; selecting a
      node updates the Selected Fleet panel; connected fleets + related workflows
      render; deterministic layout asserted.

## Done when

The Fleets route renders a selectable fleet relationship graph with its right
rail, reachable from the nav and from a fleet satellite's "View Details", backed
by fixtures and (via Slice 7) live fleet data with honest degradation.
