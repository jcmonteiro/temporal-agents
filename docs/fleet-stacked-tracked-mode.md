# Fleet stacked/tracked execution

Three-tier feature plan (brief → implementation brief → vertical slices) for
changing `fleet execute` so dependent slices build on the branches they depend
on and are kept in sync until their PRs merge.

Decisions in the implementation brief are the outcome of an explicit design
review; named concrete choices there are **stated requirements**, not incidental
suggestions.

---

## 1. Brief — why and what

### Problem

Originally a fleet run treated the dependency graph as *ordering only*: every
node developed on its own branch cut from a single pinned base and never
inherited the work of the nodes it depends on. A dependency edge only sequenced
work and skipped a dependent when a prerequisite failed. This forced every
node's prompt to be a fully standalone instruction and left a dependent unaware
of the code its prerequisite actually produced, so genuinely layered changes (a
foundation plus slices that build on it) could not be expressed — the fleet
coordinated *when* work ran but not *what it built on*. Slices 1 and 2 below
(seed-via-merge + seed-conflict handling) have since shipped, so seeding is now
as-is; the remaining problem this doc addresses is the run's short lifetime
(below) and the Phase 2 integration slices (3–8), which remain target behavior.

The run is also short-lived: it returns as soon as each node's develop step has
landed (or, with `--with-remote`, once each node's own pipeline converges). It
has no notion of the slices' pull requests being integrated over time, so once
opened the PRs drift apart and diverge from `main` with no help from the fleet.

### Desired outcome

- A dependent slice is developed and reviewed **on top of the work of the
  slices it depends on**, so layered changes are expressible and each slice sees
  its prerequisites' committed code.
- A fleet run **stays with its slices until they are integrated**: it keeps each
  slice's branch current with the slices (and mainline) it depends on and holds
  open until every pull request has merged, so the set of slices lands as a
  coherent whole rather than a pile of diverging branches.

### Success signals

- For a plan `A → B`, B's branch/PR contains A's reviewed work, and B is
  developed and reviewed only after A has been developed and reviewed.
- As dependencies merge, each dependent PR's readiness and target move toward
  "ready, against `main`" without manual branch surgery, and the run ends only
  once every PR has merged.
- A dependency that is abandoned (its PR closed unmerged) visibly stops all work
  on the slices that depended on it rather than leaving them running against a
  dead branch.

### Out of scope

- The fleet **merging** pull requests itself. Merges remain a human/GitHub
  decision; the fleet only observes and reacts.
- Changing the *planning* step (`fleet plan` / `FleetPlanWorkflow`).
- Re-decomposing or re-writing node prompts to exploit the new inheritance;
  prompt authoring guidance is a separate concern.
- Any hard run-lifetime cap or auto-closing of unmerged PRs.

---

## 2. Implementation brief — how-shaped constraints

### Execution shape: two graph-ordered phases

- **Phase 1 (local, both modes).** For every node: develop, then run the local
  review loop **to convergence, awaited**. A node's Phase 1 begins only after
  *all* its prerequisites have finished develop **and** review. This applies to
  plain `fleet execute` as well as `--with-remote`; it supersedes today's
  behaviour of returning after the develop step with review abandoned.
- **Global barrier.** No pull request is opened until **every** node has finished
  Phase 1 — independent branches wait for the slowest node's local review to
  converge.
- **Phase 2 (remote, `--with-remote` only).** After the barrier, open PRs and
  keep each node in a tracking loop until its PR merges.

Consequence to honour: the fleet can no longer reuse
`codereview.DevelopWorkflow` wholesale. "Develop → await local review" must be a
graph-gated unit; PR-open / pilot / tracking must be separable into Phase 2.

### Branch model (both phases)

- Each node's branch is cut from a **single base pinned once at run start**
  (repo HEAD via the existing `ResolveBase`), then **each dependency branch is
  merged in**. A node with multiple dependencies merges all of them; this is the
  same operation used for initial seeding and every later re-sync.
- Integration uses **`git merge`, never rebase** — node branches are published
  PR branches; rebasing would force-push, rewrite history, and orphan review
  comments.
- **Merge-conflict policy (seed and re-sync alike):** attempt agent resolution,
  bounded; on repeated failure `git merge --abort` (leave the branch clean, no
  conflict markers pushed), mark that node's sync **blocked**, notify, and retry
  on a later tick.
- Skip semantics carry over: if a prerequisite's develop fails (no usable branch
  to seed from), its dependents are **skipped**.

### Phase 2 per-node tracking (until the PR is `MERGED`)

- **The fleet observes merges only; it never merges a PR.**
- **Dynamic PR base + draft state**, keyed on the node's count of still-*unmerged*
  direct dependencies and re-evaluated as dependencies merge:

  | Unmerged direct deps | PR state | PR base |
  |---|---|---|
  | 0 (all merged, or none) | Ready for review | `main` |
  | exactly 1 | Ready for review | that dependency's branch |
  | ≥ 2 | Draft | `main` |

- **Re-sync triggers** (evaluated each poll tick per dependent): a still-unmerged
  dependency branch's HEAD advanced → merge that branch in; a dependency's PR
  merged to `main` → merge `main` in, then re-apply the base/state rule.
- **Pilot re-triggers** whenever unresolved Copilot/human comments exist,
  throughout Phase 2, until the PR merges.
- **Re-sync merges never re-run the Phase 1 local review** — they are integration
  only; correctness is covered by CI, the Copilot pilot, and human PR review.

### Lifecycle and long-run mechanics

- Per-node terminal states: `MERGED`; `SKIPPED`/`FAILED` (own or prerequisite
  develop failed); `BLOCKED`.
- **Dependency PR closed unmerged** → transitively mark dependents `BLOCKED`,
  **cancel every still-running workflow for those dependents** (their in-flight
  develop/review/pilot children and tracking loops must stop; supervised — not
  abandoned — children so cancellation propagates), notify, and **leave their
  PRs open** for a human to salvage or close.
- The run **completes when every node is terminal**. No timeout; stoppable via
  cancellation (surface `ctx.Err()` as canceled, as today).
- **Bound Temporal history with continue-as-new**, carrying durable run state
  (per-node status, branch name, PR number/state/base, last-synced dependency
  HEADs), mirroring the existing pilot loop.
- Poll cadence is **coarser than the 1-minute review poll** (multi-minute),
  reused for merge-status and dependency-HEAD-drift checks.
- **Each node's worktree persists until that node is terminal**, then is removed;
  branches stay visible across worktrees via the shared `.git`, so a dependent
  merges a dependency's branch locally without the network.

### Seams and boundaries (hexagonal)

- New/extended **driven ports** (thin adapters over `git`/`gh`), kept behind
  interfaces in `ports.go`: probe a branch's HEAD; merge one branch into another
  in a worktree and push; query a PR's merged/closed state; retarget a PR's base
  branch; toggle a PR draft/ready.
- Domain/core (`domain.go`) owns the pure logic: layer ordering (existing
  `TopoLayers`), the unmerged-dependency → (state, base) mapping, run-state
  transitions, and terminal/summary aggregation — no SDK or I/O.
- Orchestration (`workflow.go`) owns phase sequencing, the barrier, the tracking
  poll loop, continue-as-new, and child-workflow supervision/cancellation.

### Open questions / risks

- Diff noise: until a dependency merges, a dependent PR against `main` shows the
  dependency's commits; accepted, self-heals as dependencies land and re-sync.
- Repeated integration merge commits add branch noise (accepted cost of
  merge-not-rebase).
- Long-lived runs depend on continue-as-new state staying small and
  deterministic; the last-synced-HEAD map must be carried forward exactly.

---

## 3. Vertical slices

Each slice closes an end-to-end, demonstrable path and references the
constraints it discharges. Ordered by dependency.

### Slice 1 — Branch-from-dependency + awaited local review (Phase 1) ✅ shipped

Replace the fleet's wholesale reuse of `DevelopWorkflow` with a graph-gated
"develop → await local review" unit. Seed each node's branch as base + merge of
each dependency branch; start a node only after all prerequisites have developed
**and** reviewed.

- **Demo:** `fleet execute` (no remote) on `A → B` — B's branch contains A's
  reviewed commits and B's develop starts only after A's review converged; a
  failed A skips B.
- **Discharges:** two-phase Phase 1; branch model (seed via merge, no rebase);
  skip-on-failed-prerequisite; the "cannot reuse DevelopWorkflow wholesale" seam.

### Slice 2 — Seed-time conflict handling ✅ shipped

When merging dependency branches while seeding a node conflicts, resolve with
the agent (bounded); on failure abort, park the node blocked, notify, retry.

- **Demo:** two dependencies that touch the same lines; the dependent seeds via
  agent-resolved merge, or parks blocked with a notification and a clean branch
  (no conflict markers) when resolution fails.
- **Discharges:** merge-conflict policy (seed side).

### Slice 3 — Global barrier + PR open with dynamic base/state (Phase 2 entry)

Under `--with-remote`, hold until every node finishes Phase 1, then open each
node's PR applying the unmerged-dependency → (state, base) table for the initial
(all-unmerged) case.

- **Demo:** `fleet execute --with-remote` on `A,C → B` — after all local reviews
  converge, PRs open together; B opens **Draft against `main`** (2 unmerged
  deps), a single-dependency node opens **Ready against its dependency branch**,
  a no-dependency node opens **Ready against `main`**.
- **Discharges:** global barrier; Phase 2 gating; dynamic base/state (initial).

### Slice 4 — Observe merges → retarget/redraft → terminate

Poll PR merged-state; as each dependency merges, re-evaluate every dependent's
base/state per the table; complete the run when all nodes are `MERGED`.

- **Demo:** merge A's PR by hand — B flips to single-unmerged (Ready, targets the
  C branch); merge C — B flips to Ready/`main`; merge B — run completes.
- **Discharges:** observe-only merges; dynamic base/state (ongoing);
  terminal-state completion.

### Slice 5 — Re-sync dependents on dependency updates

Each tick, merge into a dependent any still-unmerged dependency branch whose HEAD
advanced, and merge `main` when a dependency merged; apply the re-sync conflict
policy. Track last-synced HEADs in run state.

- **Demo:** push a commit onto A's still-open branch — B's branch gains a merge
  of A; a conflicting change parks B blocked and notifies; after A merges, B
  gains a merge of `main`.
- **Discharges:** re-sync triggers; merge-not-rebase; re-sync conflict policy;
  "re-sync never re-runs local review".

### Slice 6 — Pilot re-trigger through Phase 2

Keep addressing new unresolved Copilot/human comments on each open PR until it
merges, alongside re-sync and merge checks.

- **Demo:** add an unresolved comment to an open tracked PR — the pilot addresses
  it; the loop keeps running until the PR merges.
- **Discharges:** pilot re-trigger throughout Phase 2.

### Slice 7 — Dependency-closed cascade + cancellation

When a dependency PR is closed unmerged, transitively mark dependents `BLOCKED`,
cancel their running workflows, notify, and leave their PRs open.

- **Demo:** close A's PR without merging — B (and any transitive dependents) go
  `BLOCKED`, their in-flight develop/review/pilot/tracking workflows stop, their
  PRs stay open, and a notification names the closed dependency.
- **Discharges:** dependency-PR-closed handling; supervised-child cancellation.

### Slice 8 — Long-run robustness

Bound history with continue-as-new (carrying run state), set the multi-minute
poll cadence, ensure cancellation surfaces as canceled, and persist/clean up
per-node worktrees on terminal transitions.

- **Demo:** a long run keeps a bounded Temporal history; cancelling it stops
  cleanly; worktrees for terminal nodes are removed while active ones remain.
- **Discharges:** continue-as-new; poll cadence; cancellation; worktree lifetime.
