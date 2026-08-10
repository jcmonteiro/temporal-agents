# Vertical slices — Locations

Ordered by dependency. Each closes a thin end-to-end path and is demoable on its
own. References are to the implementation brief (IB §n).

| # | Slice | Demo |
|---|-------|------|
| 1 | [Location contract](./01-location-contract.md) | every API item reports a location reference and every response carries the registry — all `unknown`, published in the schema |
| 2 | [Recorded locations](./02-recorded-locations.md) | a fresh run reports its real directory, and a worktree run reports its repository as parent |
| 3 | [Planets per location](./03-planets-per-location.md) | the overview groups work into planets per location, folds by depth, and collapses to one planet per repository |
| 4 | [Location page](./04-location-page.md) | opening one planet shows just that location's work |

Slice 1 ships the shape with no new facts (IB §1), so nothing downstream needs a
breaking change later. Slice 2 makes the facts real (IB §2, §3). Slice 3 is the
visualization (IB §4) and runs **after** slice 2, on real data. Slice 4 gives the
place a page, which later features extend.
