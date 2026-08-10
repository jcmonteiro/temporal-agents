# Slice 2 — Recorded locations (probe once, derive the hierarchy)

**Discharges:** IB §2 (probed facts only, no prefix parents), IB §3 (probe at the
edge, no migration).

**Demo:** start a `develop` run in a checkout and a second one in a git worktree.
The API reports the first under its repository directory, and the second under the
worktree with the repository as its parent. Kill the git binary's ability to
answer (or run outside a repository) and the run reports `unknown` instead of a
guess.

## Tasks

- [ ] Add a **location probe** at the edge, over the existing git port: absolute
      working directory, the repository's common git directory, and whether the two
      differ. One quick activity, run once at workflow start, retried a bounded
      number of times, and **never fatal** — a failure records nothing and the
      item stays unknown.
- [ ] Record the probe's facts in the existing per-kind recorded detail, so **no
      schema migration** is needed.
- [ ] Wire the probe into every workflow that owns a working directory (run,
      develop, review, pilot, fleet node), without changing any existing step's
      behaviour or ordering guarantees.
- [ ] Derive locations in the application core from the recorded facts: a directory
      location for the working directory, whose parent is the repository's
      directory location **only when git says they differ**. No path-prefix edges.
- [ ] Represent directory-less work (a pilot loop acting on a pull request with no
      local checkout) as a `remote` location; surface the branch/ref as an **item**
      attribute, not a place.
- [ ] Replay tests stay green: add the probe such that recorded histories in
      `testdata` still replay, and add a new recorded history covering the probe.
- [ ] Unit tests: worktree-and-repository pair yields a two-level chain; identical
      paths yield one level; missing facts yield unknown; a ref never creates a
      place.
- [ ] Integration test with testcontainers: recorded runs project into the expected
      registry, closed under ancestry.

## Done when

Real work reports real places, a worktree's parent is its repository, a failed
probe degrades to unknown, and existing commands and replays are unaffected.
