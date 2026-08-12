# Slice 01 — Establish the Storybook visual contract

## Outcome

A stakeholder can review one intentional visual direction before route-specific
redesign starts. The direction explains how new work will feel cohesive with the
Notifications menu and locations canvas without forcing later surfaces into one
fixed composition.

## Constraints discharged

C1, C2, C3, C4, C7, C8.

## Work

- Add or refine Storybook coverage that displays the application foundations and
  representative shared controls in realistic combinations.
- Include the Notifications menu and locations canvas in the visual review as
  quality references.
- Define the shared visual rules needed for hierarchy, typography, spacing,
  surfaces, borders, depth, controls, focus, feedback, and status.
- Show representative content in light and dark themes and at narrow and wide
  sizes.
- Correct shared-foundation defects found during review only when the correction
  does not expand this slice into a target-page redesign.
- Record any design choice that constrains later slices in the implementation
  brief; leave page composition choices open.

## Demo

In Storybook, move through the visual contract and the two reference surfaces.
Show the same system in light and dark themes and at desktop and mobile widths.
Demonstrate hover, focus, active, disabled, success, warning, and error treatment
where those states exist.

## Done when

- [x] The visual direction is reviewable in Storybook without a live backend.
- [x] Shared rules form one system and do not merely catalogue unrelated styles.
- [x] Both quality-reference surfaces still render and behave correctly.
- [x] Narrow stories have no clipped essential content or accidental horizontal overflow.
- [x] Storybook interaction and accessibility checks pass.
