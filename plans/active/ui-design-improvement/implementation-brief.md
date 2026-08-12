# UI design improvement — implementation brief

## Required constraints

- **C1 — One system.** All changed surfaces must use a cohesive design system.
  Shared choices for color, typography, spacing, shape, elevation, controls,
  state, and motion must produce a recognizable application-wide experience.
- **C2 — Design freedom.** The implementation agent is free to decide on the
  better design. The plan does not prescribe one layout or aesthetic. Any choice
  is valid when it improves the target surface, satisfies accessibility and
  responsiveness needs, and supports one cohesive experience.
- **C3 — Storybook-first work.** Changes must be visualized and worked on using
  the existing Storybook setup. Important states must be reproducible without a
  live backend. Storybook is the review surface throughout each slice, not only
  a final catalogue.
- **C4 — Preserve strong references.** The Notifications menu and the
  locations/planets/satellites canvas are visual quality references. Their useful
  clarity, depth, and character must be retained. Shared changes must not cause
  visual or interaction regressions in them.
- **C5 — Preserve behavior.** This initiative changes presentation and
  interaction quality, not product, domain, API, or workflow semantics.
- **C6 — Complete state design.** Each affected surface must cover its relevant
  default, loading, empty, populated, success, error, disabled, and in-progress
  states. The exact set depends on behavior that already exists.
- **C7 — Responsive and themed.** Narrow and wide viewports and both light and
  dark themes must be designed and reviewed. Content must remain visible and
  actions usable without accidental page overflow.
- **C8 — Accessible interaction.** Semantic structure, readable contrast,
  keyboard use, visible focus, focus containment for modal UI, and clear labels
  are acceptance constraints.
- **C9 — Existing test boundaries.** Pure visual logic stays testable without
  doubles. Page and component behavior continues to use the real component tree
  with HTTP stubbed only at the fetch edge.

## Seams and boundaries

The work can change frontend design tokens, global visual foundations, shared UI
components, page composition, and Storybook stories. It touches the Settings and
Runs page boundaries, the steering overlay boundary, and shared application
chrome where cohesion requires it.

The browser client remains behind its existing client interfaces. No visual
component takes ownership of domain decisions or transport behavior. Shared
presentation primitives must stay independent of page-specific business rules.

## Storybook workflow

Each slice follows this review loop:

1. Capture the current surface and all meaningful states as stories.
2. Compare it with the Notifications menu and locations canvas reference
   stories in light and dark themes.
3. Explore and implement the selected design in Storybook at narrow and wide
   viewports.
4. Exercise interactions through story play functions where behavior matters.
5. Run Storybook tests and accessibility checks before accepting the slice.
6. Verify the same composition in the application without changing its behavior.

Storybook stories must show enough realistic data to reveal hierarchy, wrapping,
scrolling, long labels, empty areas, and action density. A single idealized happy
path is not sufficient.

## Design-system direction

The exact page composition remains open. The resulting system must provide:

- a clear hierarchy from application chrome to page, section, item, and metadata;
- a consistent family of controls and interaction states;
- consistent status, feedback, and destructive-action treatment;
- deliberate content width, density, whitespace, borders, surfaces, and depth;
- reusable patterns for operational data, forms, overlays, and actions;
- shared theme behavior without one-off light or dark values.

Existing tokens and components can be evolved, replaced, or extended. A new
primitive is justified only when it solves a repeated presentation need. Page
components must not duplicate a common visual rule when a shared rule is
sufficient.

### Established visual contract

Slice 01 selected these shared constraints for later surface work:

- Calm neutral surfaces, precise borders, orbital depth, and one blue accent
  connect new work to the canvas and Notifications references.
- Page intent leads the hierarchy; sections group related content; borders
  separate layers. Strong shadows are reserved for overlays and focused,
  transient surfaces.
- Controls use a minimum 40-pixel target, shared priority styles, and one visible
  focus-ring treatment. State always has a text label and never relies on color.
- Default density is compact without crowding. Wide layouts use a bounded content
  width, and narrow layouts collapse grids before they can cause page overflow.
- Success, warning, and error feedback use shared semantic tokens in both themes.

These rules constrain visual language and interaction quality. They do not fix a
page composition for Settings, Runs, or steering.

## Risks and unknowns

- Shared token changes can silently regress the canvas or Notifications menu.
- Dense run data can force a false choice between visual calm and operational
  detail; hierarchy and progressive disclosure may require exploration.
- The steering modal has long-lived and asynchronous states that can be missed by
  a happy-path story.
- Narrow application chrome can constrain right-aligned overlays and create
  clipping or horizontal overflow.
- The current visual strengths may not yet be expressed as reusable rules.
  Extract only proven common patterns; do not copy incidental implementation.

## Open design choices

The implementation agent can choose among alternatives such as cards or bounded
sections, side or top page navigation, and modal or responsive full-screen
steering treatment. Selection must be based on
content clarity, consistency, responsiveness, and Storybook review rather than
on this plan prescribing one option.

## Validation

A slice is acceptable when its Storybook stories are reviewable in light and
dark themes and at narrow and wide sizes, interaction and accessibility checks
pass, relevant frontend tests pass, and no behavior or reference-surface
regression is found.
