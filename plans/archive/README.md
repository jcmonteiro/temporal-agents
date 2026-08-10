# Archived plans

Delivered work, kept for provenance. Not context: read only when asked by name
(see `AGENTS.md` in this directory).

| Plan | Outcome delivered | Notes |
|---|---|---|
| [`database`](./database/) | every run, code, fleet, and scheduled execution records itself durably; `history` reads it back; fleet plans live in the store | fully delivered |
| [`frontend`](./frontend/) | the hub: app shell, orbit overview, selection and right rail, canvas controls, live read path, dismissals persisted | slices 06 (secondary destinations) and 08 (fleet node graph) were **not** delivered; their remainder is carried by `active/launching-work` slice 1 (routing and dedicated pages), and the fleet graph page stays an open placeholder there |
| [`locations`](./locations/) | every item reports where it runs; the overview draws one planet per place, folds by depth, collapses to one planet per repository, and a place has a page of its own | fully delivered |
