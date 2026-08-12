# Slice 05 — Validate the cohesive experience

## Outcome

A stakeholder can move through the canvas, Notifications, Settings, Runs, and
steering and recognize one coherent product. No target surface or strong
reference surface has a known visual, responsive, accessibility, or interaction
regression.

## Constraints discharged

C1 through C9.

## Work

- Build a Storybook review path that covers the redesigned surfaces and the two
  strong reference surfaces with consistent realistic data.
- Compare hierarchy, controls, status, feedback, spacing, depth, language, and
  motion across surfaces; remove unjustified one-off treatments.
- Review light and dark themes at representative narrow and wide viewports.
- Test transitions and entry points among Runs and steering, and check shared
  application chrome around each surface.
- Resolve clipping, overflow, contrast, focus, keyboard, and content-density
  defects found during the cross-surface review.
- Run the complete Storybook, frontend test, and production-build checks.
- Record only genuine future product work as follow-up; do not defer defects that
  violate this plan's constraints.

## Demo

Use Storybook to present one operator journey: inspect the location canvas, open
Notifications, review Settings, inspect runs in several states, and enter
steering. Repeat key views in dark theme and at a narrow viewport.

## Done when

- [x] The full review path feels governed by one design system.
- [x] Notifications and the locations canvas retain their quality and behavior.
- [x] All target surfaces pass light, dark, narrow, and wide review.
- [x] No known essential-content clipping or accidental horizontal overflow remains.
- [x] Storybook interactions and accessibility checks pass for the full review path.
- [x] All frontend tests and the production build pass.
