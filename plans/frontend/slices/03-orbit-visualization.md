# Slice 3 — Orbit visualization

**Discharges:** IB §4 (orbit visualization), IB §5 (rendering decision).

**Demo:** the Overview replaces the slice-2 list with the orbital view — a
central body with work items distributed on concentric dashed orbits around it,
each item showing icon, label, and a status dot in its status color. Matches the
concept's core visual. A decorative starfield background may be included.

## Tasks

- [ ] Decide and record the rendering approach (inline SVG vs positioned DOM);
      justify against jsdom testability (IB §4, §5). Record in this file/PR.
- [ ] Add `src/components/orbit/` (or `src/pages/overview/orbit/`) with a pure
      layout function: `(items, center, config) → positioned items` that is
      deterministic and reproducible (IB §4).
- [ ] Implement orbit assignment/spacing that distributes N items without
      status-hiding overlap and degrades gracefully as N grows (IB §4).
- [ ] Render the central body, the concentric orbit rings (dashed), and each
      orbiting item (icon + label + status dot using the shared status token).
- [ ] Add the greeting header ("Good morning, …" / "Here's what's orbiting your
      work today.") above/over the canvas as in the concept.
- [ ] Keep custom CSS contained to the orbit component; chrome stays on the
      local `ui/` primitives; all colors from tokens incl. status colors (IB §5).
- [ ] Test: layout function is deterministic (same input → same positions) and
      produces non-overlapping placements for representative N; component test
      asserts every item renders with label + correct status color under jsdom.

## Done when

The Overview shows all work items orbiting a center with legible statuses,
driven by the client data from slice 2, and the layout is covered by
deterministic tests.
