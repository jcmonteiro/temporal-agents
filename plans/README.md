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
| [`active/prompts`](./active/prompts/) | stored, scoped, versioned agent instructions with provenance | foundation slices delivered; configuration surface planned |
| [`active/steering`](./active/steering/) | a review round waits for the operator, who guides it | planned |

Order and dependencies live in `ROADMAP.md`.

## Archived plans

Delivered work, kept for provenance only: see [`archive/README.md`](./archive/README.md).
