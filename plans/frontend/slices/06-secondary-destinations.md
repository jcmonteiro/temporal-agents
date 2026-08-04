# Slice 6 — Secondary destinations + top-bar affordances

**Discharges:** brief scope (non-overview destinations are navigable
placeholders; search + notifications presentational), IB §2 (routing, chrome).

**Demo:** clicking Fleets, Workflows, Templates, Insights, or Settings navigates
to a placeholder page that renders its title (and marks the nav item active).
The top-bar search field and notification bell are present and interactive at a
presentational level (search input accepts text; bell opens an empty/placeholder
popover). Help is reachable.

## Tasks

- [ ] Add lazy routes in `router.tsx` for each secondary destination, each to a
      minimal `src/pages/<name>/page.tsx` placeholder with a titled empty state.
- [ ] Wire left-nav links to the routes; active state reflects the current route
      (IB §2 routing).
- [ ] Make the top-bar search a controlled input (no backend search — brief
      scope); optionally filter/scroll to a matching orbit item as a nicety,
      but do not require it.
- [ ] Make the notification bell open a placeholder popover (no delivery — brief
      scope).
- [ ] Test: each nav link resolves to its placeholder page and sets active
      state; search input updates on type; bell toggles its popover.

## Done when

All six nav destinations resolve to real (if placeholder) routes with correct
active state, and the search + notification affordances are present and behave
at the presentational level defined by the brief.
