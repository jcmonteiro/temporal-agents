# Slice 4 — Location page (one place, its work)

**Discharges:** brief outcome (open one place and see just its work), IB §3
(client boundary), IB §5 (leaves room for later per-place features).

**Demo:** selecting a planet and choosing to open it shows a page listing that
location's fleets, runs, and schedules with their states, its parent and children,
and a link back to the overview. A location with no work says so.

## Tasks

- [x] Add a read of one location and its work through the client boundary,
      returning a failure result the page renders as an error state (not a blank
      page).
- [x] Add the page: the location's label and natural key, its ancestry path, its
      children, and its work grouped by state.
- [x] Reach the page from a planet, and reach a work item's own detail from the
      page.
- [x] Empty state: a registered-but-idle place reads as idle, not broken.
- [x] Deep-link safety: an unknown or stale location id renders a clear
      not-found state.
- [x] Leave an explicit extension point for later per-place features (launcher,
      settings), without building them.
- [x] Component tests: work is listed under the right place, ancestry renders,
      empty and not-found states render, and a failed read surfaces as an error.

## Done when

One place has a page of its own, reachable from the overview, showing exactly
that place's work, with honest empty and error states.
