# Roadmap and cross-plan decision record

Loaded on request, not by default (see `README.md`). Each feature under `active/`
owns its `brief.md` (why/what), `implementation-brief.md` (constraints), and
`slices/` (task-oriented, demoable units). This file holds what no single feature
owns: the order the features ship in, why that order is forced, and the decisions
that several features depend on.

## Feature order

| # | Plan | Outcome | Depends on |
|---|------|---------|-----------|
| 1 | [`locations`](./active/locations/) | every item reports where it runs; the overview groups work into planets per location, collapsing toward parents | — |
| 2 | [`authentication`](./active/authentication/) | the hub authenticates through an OIDC provider, SSO-ready, with server-held tokens | 1 (nothing hard, ships beside it) |
| 3 | [`prompts`](./active/prompts/) — foundation slices | prompts are stored, scoped per location, versioned, and every run records which prompt version it used | 1 |
| 4 | [`launching-work`](./active/launching-work/) | an operator starts and re-runs agent work from a dedicated page | 1, 2 |
| 5 | [`steering`](./active/steering/) | a review round waits for the operator, who guides it by text or by being grilled, then builds | 1, 2, 3 (foundation) |
| 6 | [`prompts`](./active/prompts/) — configuration slices | prompts are editable globally and per location, with resets to inherited and to factory | 3 |

Forcing reasons for the order:

- **Locations first.** Prompt scope, the launcher's working directory, and a
  notification's target all key off a location. Building them before locations
  exist would mean inventing a temporary grouping and deleting it later.
- **Authentication before any write.** The write surface (starting agent work)
  and the steering stream both need a credential. Retrofitting one into a live
  stream and a mutation surface costs more than doing it first.
- **Prompt foundation before steering.** Steering needs a scoped setting (is
  steering on for this location?) and a governed prompt (the grilling
  instruction). A second, throwaway resolution mechanism for those is waste.
- **The `locations` frontend slice runs after the recording slice**, not beside
  it, so the visualization is built against real recorded locations rather than
  a placeholder payload.
- **Prompt configuration UI last**, matching the original numbering: it changes
  nothing else depends on.

## Decision record (settled before planning)

Locations
1. A location is a **tagged union** — `unknown | directory | remote` — carried in
   a **flat per-response registry** and referenced by items through a
   server-issued id. No nested parent objects, no client-derived identity.
2. Every location carries a **server-computed label**, so no consumer parses a
   path.
3. **A project is a location.** Any per-project setting resolves
   `location -> parent -> ... -> base -> global -> factory`, per key, and a
   child inherits its ancestors' values.
4. Locations are only ever **probed facts**: the working directory and the git
   worktree-to-repository relation. Filesystem path prefixes never create a
   parent edge. A git ref is an **item** attribute, not a location; the `remote`
   kind is reserved for work with no local directory.
5. The overview draws **one planet per location**, folds locations deeper than the
   visible depth into their nearest visible ancestor (badge shows the fold
   count), overrides that fold for legibility when a planet is crowded, and
   offers a **collapse-all** option that folds every location into its base
   ancestor. The unknown planet is never folded.

Authentication
6. **OIDC with `serve` as a confidential client (BFF).** Tokens stay server-side;
   the browser holds only an `HttpOnly`, same-site session cookie. Rejected:
   public-client SPA — GitHub has no secret-less flow, browser refresh tokens are
   blocked by tracking protections, and per-provider quirks would land in the
   frontend.
7. The issuer is **configuration, not code**; a provider seam allows a non-OIDC
   provider later. **Dex** runs in `docker-compose.yml` for local use, and
   integration tests bring it up with testcontainers-go.
8. **Identity exists, authorization does not.** A principal is recorded for
   audit; no roles, no per-principal filtering of work. The static bearer token
   stays for programmatic clients.

Prompts
9. A prompt resolves **once per unit of work** (a chain for agent prompts, a
   session for the grilling prompt), in an activity, never in workflow code, and
   the resolved text travels in the workflow input across continue-as-new.
10. Prompt rows are **append-only versions**; an edit adds a version, a reset
    moves or removes a pointer. Each execution records `(key, scope, version,
    hash)` so "which prompt produced this" stays answerable.
11. Overridable prompts are **templates with declared required placeholders**,
    validated on save. Text a parser depends on is a **non-overridable system
    block** the server always appends.
12. Factory defaults live in Go source and are upserted at startup, so an
    upgrade reaches every location that has no override.

Writes and steering
13. A start request **never carries a path**: it references a registered
    location, and the server resolves the directory. Requests are idempotent on a
    client `requestId`, and the core refuses a second concurrent writer in one
    working tree.
14. Steering is **frontend only** (no CLI), enabled through the scoped setting,
    **off** by factory default.
15. A steering session is a **child workflow** (durable, signalled), and it does
    **not** appear as its own satellite — the parent develop/review item flips to
    `waiting-input`. The session records its own execution row for token
    accounting.
16. The **wait is unbounded**. Reminder notifications repeat **daily, with no
    cap**. Nothing on the wait path carries an execution timeout.
17. The session's artifact is **one guidance text**. Typing it and being grilled
    are **peer producers** of that text. `Build` requires non-empty text; `Skip`
    is the way to proceed without guidance; `Stop` ends the loop as
    `waiting-input`. The first decision signal wins.
18. Reaching the review pass cap becomes a **human checkpoint**: `Continue`
    resets the counter, `Accept` finishes the loop. A loop's ending is recorded
    as `converged | operator-accepted | operator-stopped | pass-cap`.
19. Steering **never writes prompts**. The transcript is context, not
    configuration.
20. Transcripts are **authoritative in Postgres**; only decisions and the final
    guidance text enter workflow history.

Platform
21. Schema migration becomes an **explicit step**; `worker` and `serve` verify
    the version and fail fast instead of racing to apply DDL.
22. Each bounded context owns its **own migrations**, and there are **no
    cross-context foreign keys**.
23. Server push uses **two SSE streams** (hub events, and one chat stream),
    resumable by sequence. List data keeps polling. No Web Push yet.

## Out of scope across every plan

- Roles, permissions, multi-tenancy, per-principal filtering of work.
- Starting fleets or creating schedules from the UI, and plan-approval UI.
- CLI support for steering.
- Web Push / service workers.
- Prompt write-back from steering conversations.
- Cross-location relations beyond the probed worktree-to-repository edge.
