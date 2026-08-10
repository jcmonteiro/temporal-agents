# Slice 1 — Routing and dedicated pages

**Discharges:** IB §3 (structural prerequisite), and the frontend shape the existing
frontend implementation brief mandates but which was never built.

**Demo:** navigation moves between the overview, a place page, a run page, a fleet
page, and a settings destination; a deliberately failing route shows the route error
boundary rather than a blank screen; a deep link opens the right page directly.

## Tasks

- [x] Introduce the router with lazily loaded route modules and a route-level error
      boundary; keep the existing shell (top bar, navigation) intact.
- [x] Add the route set: overview, place, run, fleet, and settings destinations, with
      the place page adopting the read-only page already built.
- [x] Make the existing navigation and the inert "view details" affordance navigate.
- [x] Preserve the intended destination through sign-in (authentication feature
      already redirects; assert it end to end here).
- [x] Add placeholder pages only where a later slice fills them, clearly marked and
      empty rather than fake.
- [x] Component tests: each route renders its page, deep links resolve, an error in a
      route is contained by the boundary, and navigation is keyboard reachable.

## Done when

Every destination is a real route, deep links work, failures are contained, and no
feature behaviour changed.
