# Slice 3 — Orbit visualization

**Discharges:** IB §4.0 (shared canvas primitive — Q8=A), IB §4a (orbit), IB §5
(rendering decision).

**Demo:** the Overview replaces the slice-2 list with the orbital view — a
central body with work items distributed on concentric dashed orbits around it,
each item showing icon, label, and a status dot in its status color. Matches the
concept's core visual. A decorative starfield background may be included.

## Tasks

- [ ] Decide and record the rendering approach (inline SVG vs positioned DOM);
      justify against jsdom testability (IB §4, §5). Record in this file/PR.
- [ ] Add the **shared canvas primitive** `src/components/canvas/` (Q8=A, IB
      §4.0): owns pan/zoom view transform, node selection, and starfield;
      parameterized by a `layout` function + a `node` renderer. Slice 5 adds its
      zoom/recenter controls; Slice 8 reuses it with a `dag` layout.
- [ ] Add the `orbit` layout as a pure function
      `(items, center, config) → positioned items`, deterministic and
      reproducible (IB §4a), passed into the canvas primitive.
- [ ] Implement orbit assignment/spacing that distributes N items without
      status-hiding overlap and degrades gracefully as N grows (IB §4).
- [ ] Render the central body, the concentric orbit rings (dashed), and each
      orbiting item (icon + label + status dot using the shared status token).
- [ ] Add the greeting header ("Good morning, …" / "Here's what's orbiting your
      work today.") above/over the canvas as in the concept.
- [ ] Keep custom CSS contained to the canvas/orbit components; chrome stays on
      the local `ui/` primitives; all colors from tokens incl. status (IB §5).
- [ ] Test: layout function is deterministic (same input → same positions) and
      produces non-overlapping placements for representative N; component test
      asserts every item renders with label + correct status color under jsdom.

## Done when

The Overview shows all work items orbiting a center with legible statuses,
driven by the client data from slice 2; the reusable canvas primitive and the
deterministic orbit layout are covered by tests.
