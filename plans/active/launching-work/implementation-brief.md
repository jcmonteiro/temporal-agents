# Implementation brief — Launching work from the hub

Derived from `brief.md`. Constraints and seams; concrete choices are marked **hard
constraint** with the reason.

## 1. The write seam (hard constraints)

- **This is the first mutation of agent work**, and it is kept in a **port of its
  own**, separate from every read port. *Reason:* the read surface must stay
  read-only by construction, as the dismissal write already demonstrates.
- **A request never carries a filesystem path.** It references a known place, and
  the server resolves the directory from the registry. *Reason:* the registry is the
  allowlist; a caller must not be able to name an arbitrary directory on the
  operator's machine.
- **Requests are idempotent on a caller-supplied request identity**, and a repeat
  returns the existing run rather than starting another or failing. *Reason:* a
  double click, a retried fetch, and a reload are all normal.
- **Concurrent conflicting work is refused in the application core**, not in the
  interface, so every caller inherits the rule. *Reason:* loops that stash and
  commit in one working tree corrupt each other; this is correctness, not politeness.
- **A refusal is an explanation.** Problem responses name the conflict and the
  conflicting run.
- **Attribution is recorded at creation** — who started the run — using the identity
  feature's principal.

## 2. What may be started (constraint)

- The first pass covers the **single-execution commands** the CLI already offers for
  a working directory, plus repeating a recorded run. **Fleets and schedules are
  excluded**, because a fleet start requires plan approval and a schedule requires a
  recurrence editor; both are features of their own.
- The exposed options must be the ones that change the *shape* of the work, not
  every flag: the interface must not become a form-shaped copy of the CLI.
- Repeating a run reuses that run's recorded instruction and place, and must not
  invent values the record does not hold.

## 3. Structural prerequisite (hard constraint)

- The hub has no routing and no dedicated pages today, while the mandated frontend
  shape requires a router with lazily loaded routes and a route-level error
  boundary. That structure is a **prerequisite**, delivered before any launching
  interface, and delivered on its own so it is reviewable without feature noise.
- Starting work lives on **dedicated pages**; the overview keeps its watching role.

## 4. Places without work (constraint)

- A place with no recorded work cannot be discovered by observation, so the feature
  needs a way to **register a place explicitly**, validated server-side (it exists,
  it is a repository, it is absolute) and rejected clearly when it is not.
- Explicit registration is also the only way an operator asserts a relation the
  probe cannot see; it must not let an operator invent a hierarchy that contradicts
  the probe.

## 5. Security constraints

- Every mutation requires authentication and the request rules the authentication
  feature established (same-site, non-simple request), because a start request runs
  code on the operator's machine.
- The registry is the boundary of what can be run in; adding to it is itself a
  mutation subject to the same rules.

## 6. Risks and open decisions

- **The interface can drift into a CLI clone.** Keep the option surface minimal and
  justified per option.
- **Concurrency rule granularity** (per place, per kind, or per place-and-kind) is
  open; it must at least prevent two writing loops in one working tree.
- **Repeat semantics** for a run whose place has since disappeared must fail
  visibly rather than fall back to another place.
- **Feedback latency:** a start returns before the work is observable through the
  read path, so the interface must not present an empty page as a failure.

## 7. Seams this work must not cross

- No new deployment topology: the API process gains the ability to submit work to
  the existing orchestrator; the worker keeps executing it.
- No change to what existing commands do, and no duplication of workflow logic in
  the API.
- No path-taking request parameters anywhere.
