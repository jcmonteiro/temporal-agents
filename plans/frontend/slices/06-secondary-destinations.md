# Slice 6 — Secondary destinations + top-bar affordances

**Discharges:** brief scope (non-overview destinations are navigable
placeholders; search + notifications presentational), IB §2 (routing, chrome).

**Demo:** clicking Workflows, Templates, Insights, or Settings navigates to a
placeholder page that renders its title (and marks the nav item active).
(Overview is Slices 3–5; Fleets is the real fleet view in Slice 8.) The top-bar
search field and notification bell are **present but disconnected** (Q20/Q22) —
no search and no notifications behaviour. Help is reachable.

## Tasks

- [ ] Add lazy routes in `router.tsx` for each **placeholder** destination
      (Workflows, Templates, Insights, Settings), each a minimal
      `src/pages/<name>/page.tsx` with a titled empty state. (Fleets route is
      Slice 8.)
- [ ] Wire left-nav links to the routes; active state reflects the current route
      (IB §2 routing).
- [ ] Add the top-bar search field but leave it **disconnected — no search
      implemented** (Q20): present, visibly inert (e.g. disabled/no-op input).
- [ ] Add the notification bell as a **disconnected placeholder** (Q22), same
      treatment as search: present but non-functional.
- [ ] Test: each placeholder nav link resolves to its page and sets active
      state; search + bell render as inert placeholders.

## Done when

All nav destinations resolve to routes with correct active state (Overview,
Fleets, and four placeholders), and the search + notification affordances are
present but disconnected (Q20/Q22).
