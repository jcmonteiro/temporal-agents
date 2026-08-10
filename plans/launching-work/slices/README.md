# Vertical slices — Launching work from the hub

| # | Slice | Demo |
|---|-------|------|
| 1 | [Routing and dedicated pages](./01-routing-and-pages.md) | the hub has real routes: overview, place, run, fleet, settings, with a route error boundary and working navigation |
| 2 | [Registering a place](./02-registering-a-place.md) | an operator registers a repository that has no recorded work, and it appears as a place |
| 3 | [Starting work](./03-starting-work.md) | a start request submits real work for a registered place, is idempotent, and refuses a conflicting second loop |
| 4 | [Launcher on the place page](./04-launcher.md) | an operator starts a develop run from a place page and lands on its run page |
| 5 | [Run page and repeat](./05-run-page-and-repeat.md) | a run page shows the run's facts and provenance, and repeats it in one action |

Slice 1 is the structural prerequisite (IB §3). Slices 2–3 make the backend able to
accept work honestly (IB §1, §4, §5). Slices 4–5 are the operator's path.
