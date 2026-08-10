# Implementation brief — Locations

Derived from `brief.md`. Names the constraints any valid implementation must
honor, and the seams it touches. Where a concrete choice is mandated it is marked
**hard constraint** with the reason that forces it.

## 1. The published contract (hard constraints)

- **A location is a tagged union**, one of `unknown`, `directory`, `remote`. A
  variant carries exactly its own fields: a directory location has an absolute,
  cleaned path and no remote; a remote location has a ref and no directory; the
  unknown location has neither and no parent. *Reason:* three independently
  nullable fields admit contradictory combinations, and every consumer would have
  to re-derive which combination means what.
- **Locations travel in a flat registry per response**, and items reference a
  location by a **server-issued id**. The registry is **closed under ancestry**
  (every referenced location plus all of its ancestors) and ordered
  **parents-first**. *Reason:* a client builds the tree in one pass, a cycle
  cannot be expressed, and the same location cannot appear in two conflicting
  copies inside one payload.
- **Identity is never derived by a client.** The id is opaque; the natural key
  (the path, or the ref) stays a field.
- **Every location carries a server-computed label.** *Reason:* a consumer that
  derives a display name from a path duplicates server logic and diverges from it.
- **The registry serialises deterministically** (stable ordering, no map
  iteration order). *Reason:* the API computes entity tags over response bytes;
  unstable ordering silently breaks conditional requests.
- **The addition is additive only.** No existing field is removed, renamed, or
  made required, and the paged active-work contract the CLI reads gains the
  reference as an optional field. *Reason:* `list` and `hubclient` must keep
  working untouched.
- Item kinds that gain a location reference: fleets, runs, schedules, and **fleet
  nodes** — a node can develop in its own worktree, so nodes genuinely differ.

## 2. Honesty constraints (hard constraints)

- A location is only ever built from **facts the system recorded**: the working
  directory a workflow was given, and the git worktree-to-repository relation.
- **Filesystem path containment never creates a parent edge.** *Reason:* git
  places a worktree outside its repository by default, so prefix logic produces
  wrong parents.
- A **git ref is an item attribute**, not a location. The `remote` variant is for
  work with **no local directory**.
- A failed or absent probe yields the **unknown** location. The unknown location
  is a real, rendered place, not a null branch.

## 3. Seams

- The application core owns the location model, the registry construction, and
  the parent derivation. It must not learn about git or SQL: the probe's result
  arrives as recorded facts, exactly as execution outcomes already do.
- The probe belongs at the **edge** (an activity over the existing git port),
  runs **once per workflow start**, and must not change what any existing command
  does or block a workflow when it fails.
- Recorded per-kind facts have an existing home that needs **no schema
  migration**; use it rather than adding columns.
- The frontend reaches locations through the existing client boundary. Components
  never fetch, and never re-derive grouping the server already published.

## 4. Visualization constraints

- **Layout stays a pure function** of (items, registry, view state). Same input,
  same picture; tests assert placement and folding.
- **Folding is derived, not stored**: a visible depth follows from the view state,
  and any location deeper than it renders inside its nearest visible ancestor,
  which reports how many places it absorbed.
- **Legibility overrides fidelity:** a place whose work cannot be drawn without
  overlap folds its children regardless of depth, and says so.
- **A collapse-all option** forces the visible depth to the base ancestor.
- **Ordering is stable across refreshes** and must not depend on item counts.
- A place holding exactly one place and no work of its own **draws once**, not
  twice.

## 5. Open decisions and risks

- Rendering technique for the multi-body scene inherits the existing definition
  of done: reactive, clickable, future-draggable, animated, low host cost,
  assertable under jsdom.
- The hierarchy is **shallow in practice** (worktree to repository, plus
  directory-less remotes). The layout must not assume depth, and must not break if
  a deeper chain appears later.
- Probing git at every workflow start adds one quick activity per run; it must be
  cheap and must degrade to unknown instead of retrying forever.
- Whether a place page eventually becomes the home of per-place settings and the
  launcher is decided by later features; this one must not close that door.

## 6. Seams this work must not cross

- No change to existing worker or CLI command behaviour, and no new required
  request or response field.
- No authentication, no mutation of agent work.
- No client-side path parsing, no client-derived location identity, no
  client-side aggregation the server already publishes.
