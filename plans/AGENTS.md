## Reading plans

- Load `plans/README.md` for orientation. Load `plans/ROADMAP.md` only when the
  order of work or a cross-feature decision is in question.
- When working on a feature, load **only** that feature's `brief.md`,
  `implementation-brief.md`, and the slice in hand. Do not read sibling plans.
- **Never read `plans/archive/**` unless the user asks for it by name.** Archived
  plans describe shipped work; the code is the truth about that work, and the
  documents only add stale detail.
- A plan is a tiered artifact. Respect the tiers when editing:
  `brief.md` = why/what (no mechanisms), `implementation-brief.md` = constraints
  and seams (not one mandated design), `slices/*.md` = demoable units of work.
  A change that names a mechanism belongs one tier down.
- When a slice is delivered, tick it in that plan's `slices/README.md`. When every
  slice of a plan is delivered, move the plan to `plans/archive/` and add its row
  to `plans/archive/README.md`.
