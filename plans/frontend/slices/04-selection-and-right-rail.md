# Slice 4 — Selection + right rail

**Discharges:** IB §4 (selectable items, observable selection), IB §2 (local
`ui/` primitives, token styling, testing).

**Demo:** the right rail from the concept appears with three sections — **State
Legend** (all seven statuses named with their color), **Selected** (details of
the highlighted item: icon, label, status, fleet, progress bar, estimate, owner,
and a "View Details" affordance), and **Up Next** (the queued items with their
statuses). Clicking or keyboard-selecting an orbiting item updates the Selected
panel; a sensible default selection is shown on load.

## Tasks

- [ ] Add `src/pages/overview/right-rail/` (or `src/components/`) with three
      sub-components: `state-legend`, `selected-panel`, `up-next`.
- [ ] Render the State Legend from the single status-token source (IB §3) so it
      cannot drift from the orbit dots.
- [ ] Make orbiting items selectable with an accessible affordance
      (focusable, name exposed, activ-on-Enter/click); lift selection state to
      the Overview page (IB §4).
- [ ] Selected panel reads the selected `WorkItem` and renders label, status,
      fleet, progress (as a bar), estimate, owner, and a "View Details" button.
      For a `fleet` item it navigates to the fleet view route (Slice 8); for a
      `workflow` item it is inert/hidden (Q5). Mutations remain out of scope.
- [ ] Up Next renders the queued items (label + status) from the client data.
- [ ] Default selection on load (e.g. first in-progress item) so the panel is
      never empty.
- [ ] Test: selecting an item (click and keyboard) updates the Selected panel;
      legend lists all seven statuses; Up Next lists the queued items.

## Done when

Selecting any orbiting item drives a populated Selected panel, and the legend +
up-next sections render from the same domain data, matching the concept's rail.
