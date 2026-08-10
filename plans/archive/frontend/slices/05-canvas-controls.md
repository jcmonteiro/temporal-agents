# Slice 5 — Canvas controls (zoom + recenter)

**Discharges:** IB §4 (zoom as view transform, recenter), IB §2 (local `ui/`
primitives).

**Demo:** the bottom-left control cluster from the concept appears — zoom out
(`−`), a readable zoom **percentage** (e.g. `100%`), zoom in (`+`), and a
recenter/target button. Zooming scales the orbit view; recenter restores the
default zoom and pan. This keeps the overview legible as work grows (a brief
success signal).

## Tasks

- [ ] Add a `canvas-controls` component (local `ui/` icon buttons) fixed at the
      bottom-left, as part of the **shared canvas primitive** (Q8=A) so the
      fleet view (Slice 8) reuses it.
- [ ] Implement zoom/pan as the canvas primitive's **view transform**
      (scale/translate), not a change to the underlying layout/data (IB §4).
- [ ] Bound zoom to a sensible min/max; display the current percentage.
- [ ] Implement recenter to reset zoom to 100% and pan to origin.
- [ ] Ensure selection (slice 4) and status legibility (slice 3) survive zoom.
- [ ] Test: zoom-in/out change the displayed percentage and the view transform
      within bounds; recenter restores defaults; underlying item data/positions
      are unchanged by zoom.

## Done when

Zoom and recenter operate on the orbit as a view transform with a live
percentage, without mutating the work data or breaking selection.
