# Slice 03 — Improve Runs pages

## Outcome

An operator can scan run state, understand progress and history, find important
metadata, and identify available actions without visual overload. Every route
under `/runs` uses the same design language as Settings and the strong reference
surfaces.

## Constraints discharged

C1 through C9.

## Work

- Inventory all current `/runs` routes and meaningful states, then represent
  them with realistic Storybook stories.
- Redesign run-page hierarchy, status treatment, metadata, timelines or activity,
  content regions, actions, and responsive behavior under the visual contract.
- Cover long output, sparse output, loading, active, waiting, completed, failed,
  and unavailable states wherever current behavior supports them.
- Preserve operational detail while making primary state and next actions
  immediately visible.
- Keep run behavior, workflow meaning, navigation targets, and data contracts
  unchanged.
- Exercise existing interactions through story play functions and the real
  component tree where useful.

## Demo

In Storybook, compare active, waiting, completed, and failed run examples. Show
how long operational content remains readable, how actions are found, and how the
composition adapts across themes and viewport sizes.

## Done when

- [x] Every current `/runs` page and meaningful existing state has reviewable coverage.
- [x] Primary state, progress, important details, and next actions have clear hierarchy.
- [x] Dense and long content remains readable without hiding required information.
- [x] Shared status and action treatments match Settings and the design system.
- [x] Narrow and wide compositions avoid clipping and accidental page overflow.
- [x] Storybook interaction, accessibility, and relevant frontend tests pass.
