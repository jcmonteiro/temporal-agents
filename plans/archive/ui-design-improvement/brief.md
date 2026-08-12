# UI design improvement — brief

## Problem

The application has an uneven visual experience. The Notifications menu and the
locations canvas feel deliberate and polished, while Settings, Runs, and the
steering modal feel substantially weaker. Operators must move between surfaces
that appear to belong to different products. This reduces clarity and trust,
especially when reading run state or making a steering decision.

## Desired outcome

Settings, all Runs pages, and the steering modal have a clear and high-quality
visual design. Existing strong surfaces remain strong. The whole application
feels cohesive because hierarchy, controls, feedback, density, motion, and
language follow one recognizable system.

## Success signals

- Operators can scan each target surface and identify its purpose, primary
  information, current state, and next action without avoidable effort.
- Settings forms have clear grouping, labels, inheritance or status information,
  actions, and feedback.
- Runs pages present operational detail with a strong hierarchy and do not feel
  visually overloaded or unfinished.
- The steering modal keeps attention on the decision and makes every available
  action understandable.
- Moving among the canvas, Notifications, Settings, Runs, and steering feels
  continuous rather than like moving among separate interfaces.
- The experience remains usable at narrow and wide sizes and with light and dark
  themes.
- Existing user behavior remains available after the visual changes.

## Scope

- The visual and interaction design of `/settings`.
- The visual and interaction design of all `/runs` pages.
- The visual and interaction design of the steering modal and its meaningful
  states.
- Shared visual rules needed to make these surfaces cohesive with Notifications
  and the locations canvas.
- Final visual, responsive, and accessibility review across all affected and
  reference surfaces.

## Out of scope

- New settings, run operations, or steering capabilities.
- Changes to workflow semantics, stored data, or public contracts.
- A redesign of the canvas model or Notification behavior for its own sake.
- A new brand identity unrelated to product usability and cohesion.
