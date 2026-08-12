# UI design improvement roadmap

This roadmap orders the visual improvement work and correlates every feature with
a demoable slice in [`ui-design-improvement`](./ui-design-improvement/). The plan
owns the reasons, constraints, and detailed acceptance checks. This file owns the
shipping order and cross-slice design decisions.

## Feature order

| # | Status | Feature / todo | Plan slice | Outcome | Depends on |
|---|---|---|---|---|---|
| 1 | DONE | Establish the visual contract | [`01-storybook-visual-contract`](./ui-design-improvement/slices/01-storybook-visual-contract.md) | one reviewable design direction connects the strong existing surfaces to all later work | — |
| 2 | DONE | Improve `/settings` | [`02-settings`](./ui-design-improvement/slices/02-settings.md) | settings become clear, calm, and consistent across forms and states | 1 |
| 3 | DONE | Improve `/runs` pages | [`03-runs`](./ui-design-improvement/slices/03-runs.md) | run information and actions become easy to scan and understand | 1, 2 |
| 4 | IN-PROGRESS | Improve the steering modal | [`04-steering-modal`](./ui-design-improvement/slices/04-steering-modal.md) | steering feels focused, trustworthy, and part of the run experience | 1, 3 |
| 5 | TODO | Validate the cohesive experience | [`05-cohesive-validation`](./ui-design-improvement/slices/05-cohesive-validation.md) | the redesigned and existing strong surfaces work as one responsive, accessible product | 2, 3, 4 |

## Why this order is required

- **Set direction before redesign.** A shared visual contract prevents each weak
  surface from receiving a separate local style.
- **Settings first.** Settings provide a bounded surface for proving hierarchy,
  form controls, feedback, spacing, and responsive behavior.
- **Runs next.** Runs add denser operational information and actions. They can
  reuse the proven visual language instead of inventing another one.
- **Steering follows runs.** The modal is entered from run work. Its hierarchy
  and controls must fit the redesigned run context.
- **Cross-surface validation last.** Final review can find discontinuities only
  after every target surface uses the same visual contract.

## Cross-plan decisions

1. The product must feel like one cohesive experience governed by a design
   system, not as separately styled pages and overlays.
2. Storybook is the required visualization and working environment. Every slice
   must expose its important states there before it is accepted.
3. The Notifications menu and the locations/planets/satellites canvas are the
   current quality references. Their clarity and character must inform the new
   direction, and they must not regress during the redesign.
4. The implementation agent is free to choose the better design. No exact
   composition, component shape, or aesthetic solution is prescribed, provided
   that the result follows the shared design system and meets the plan outcomes.
5. Visual work must preserve user behavior, API contracts, and domain behavior
   unless a separate approved feature changes them.
6. Light and dark themes, narrow and wide viewports, keyboard use, focus states,
   loading, empty, error, and populated states are part of the design rather than
   follow-up polish.
7. Work ships by complete surface. A slice can change shared foundations, but it
   must end with a user-visible, reviewable improvement.

## Out of scope

- New product capabilities or workflow behavior.
- Backend, persistence, or API redesign.
- A replacement for the locations canvas interaction model.
- A replacement for the Notifications menu behavior.
- Branding work that is not needed for a cohesive application UI.
