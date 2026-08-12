# Plans — index

Small on purpose. This file is the only plan document worth reading without a
reason; everything else is loaded on demand.

## Loading rules

| Need | Read |
|---|---|
| orientation | this file |
| what ships in which order, and why; cross-feature decisions | `ROADMAP.md` |
| working on one feature | that feature's `brief.md`, then `implementation-brief.md`, then the one slice in hand |
| history of a delivered feature | `archive/<plan>/` — **only when explicitly asked** |

Never load a whole plan tree "for context". A slice file states what to build; the
implementation brief states the constraints it must honor. Reading more than the
feature in hand adds noise, not accuracy.

## Active plans

| Plan | Covers | Status |
|---|---|---|
| [`active/steering`](./active/steering/) | a review round waits for the operator, who guides it | all slices delivered |
| [`active/ui-design-improvement`](./active/ui-design-improvement/) | a cohesive redesign of Settings, Runs, and steering | all slices delivered |

The UI improvement order and cross-feature decisions live in
[`active/ROADMAP_02.md`](./active/ROADMAP_02.md).

## Archived plans

Delivered work, kept for provenance only: see [`archive/README.md`](./archive/README.md).
