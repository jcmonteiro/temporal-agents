# Slice 02 — Improve Settings

## Outcome

An operator can understand the Settings page structure, distinguish values and
states, make changes with confidence, and understand the result. The page feels
like the same product as the canvas and Notifications menu.

## Constraints discharged

C1 through C9.

## Work

- Create or complete stories for every meaningful existing Settings state,
  including realistic long content and failure or in-progress states.
- Redesign information hierarchy, grouping, form controls, actions, feedback,
  and responsive composition under the shared visual contract.
- Make inherited, overridden, saved, reset, disabled, loading, and error
  treatment clear wherever those behaviors already exist.
- Keep page behavior and data contracts unchanged.
- Exercise user-visible Settings behavior through the real component tree and
  the existing fetch-edge story/test boundary.
- Review all stories in both themes and at narrow and wide sizes.

## Demo

Use Storybook to show a realistic Settings page, make an existing change, show
feedback, and demonstrate at least one non-happy state. Repeat the visual review
at desktop and mobile widths and in both themes.

## Done when

- [ ] Existing Settings capabilities and meaningful states have stories.
- [ ] Page purpose, sections, labels, current values, and primary actions are easy to scan.
- [ ] Save, reset, disabled, loading, and error feedback is consistent and unambiguous.
- [ ] Keyboard order, focus, labels, and contrast pass accessibility review.
- [ ] No narrow-viewport clipping or accidental horizontal overflow remains.
- [ ] Storybook tests and relevant frontend tests pass.
