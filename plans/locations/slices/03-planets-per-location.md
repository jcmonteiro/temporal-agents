# Slice 3 — Planets per location (grouping, folding, collapse-all)

**Discharges:** IB §4 (layout purity, derived folding, legibility override,
collapse-all, stable ordering), brief outcome (grouping and folding).

**Runs after slice 2**, against real recorded locations.

**Demo:** the overview shows one planet per location with that location's work
around it; zooming out folds worktrees into their repositories with a badge
naming how many places were absorbed; one action collapses everything to one
planet per repository; the unknown planet is always present and never folded.

## Tasks

- [ ] Extend the client boundary to read the registry and expose it as a keyed
      structure; components never parse paths and never re-derive grouping.
- [ ] Model the scene as a **pure function** of (items, registry, view state) →
      bodies and placements. No randomness, no time, no mutation of inputs.
- [ ] Implement **derived folding**: a visible depth follows from the view state;
      deeper locations render inside their nearest visible ancestor, which reports
      the fold count.
- [ ] Implement the **legibility override**: a place whose work cannot be drawn
      without overlap folds its children regardless of depth and says so.
- [ ] Implement **collapse-all**, forcing the visible depth to the base ancestor.
- [ ] Draw a place holding exactly one place and no work of its own **once**.
- [ ] Order places **stably** and independently of item counts, so the five-second
      refresh does not reshuffle the picture.
- [ ] Keep selection behaviour for work items; add selection of a **place**, which
      fills the detail rail with the place, its work counts by state, and its
      children.
- [ ] Keep the center a neutral mark that holds no work.
- [ ] Accessibility: places are focusable and named, folds announce their count.
- [ ] Component tests: given a fixed registry, assert placements, fold counts at
      each depth, collapse-all, the single-child rule, the crowded-place override,
      stability across two identical loads, and that the unknown place renders.

## Done when

The overview groups real work by place, folding is deterministic and legible,
collapse-all gives one planet per repository, and the picture is stable across
refreshes.
