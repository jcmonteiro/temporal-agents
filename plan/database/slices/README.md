# Vertical slices — Durable execution history and plan store

Each slice cuts end-to-end (infra → adapter → port → workflow/CLI → observable
result) and is demoable on its own. They are ordered by dependency: slice 1
lays the foundation *while* delivering the first real recording, so the
foundation is never a horizontal, un-demoable layer. Later slices widen coverage
and move plans into the store.

| # | Slice | Discharges |
|---|-------|-----------|
| 1 | Foundation + record `run` + `history` read | Postgres constraint, hexagonal boundary, determinism, execution record shape, must-succeed recording |
| 2 | Record `code` executions | execution coverage for develop/review/pilot/open-pr |
| 3 | Record `fleet` executions (parent + nodes) | execution coverage for fleet, parent/child correlation |
| 4 | Record `schedule`-fired executions | execution coverage for schedule |
| 5 | Fleet plans in the store | plan store replaces `fleet-plan.json`, authoritative-plan failure semantics |

See each `NN-*.md` for detail.
