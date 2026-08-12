# Slice 04 — Improve the steering modal

## Outcome

An operator entering steering stays oriented to the run, understands why input is
needed, can work through the available guidance flow, and can choose the correct
action with confidence. The overlay feels native to the redesigned Runs
experience.

## Constraints discharged

C1 through C9.

## Work

- Add Storybook stories for each meaningful existing steering state, including
  long conversation content, pending work, errors, empty guidance, and all final
  decision choices.
- Redesign overlay hierarchy, context, conversation or guidance content, input,
  actions, feedback, scrolling, and responsive treatment under the shared visual
  contract.
- Preserve clear distinctions among existing primary, secondary, skip, stop, and
  destructive or irreversible actions.
- Preserve all steering semantics, validation, streaming behavior, and workflow
  decisions.
- Verify keyboard entry and exit, focus containment and restoration, accessible
  naming, live updates, and reduced available space.
- Decide through Storybook review whether narrow steering remains modal or uses a
  more suitable responsive overlay treatment.

## Demo

Open steering from a representative run story. In Storybook, show initial,
active, pending, error, long-content, and decision-ready states. Complete an
existing interaction with the keyboard, then show the narrow-screen treatment.

## Done when

- [ ] Every meaningful steering state is reproducible without a live backend.
- [ ] Run context, current steering state, input, and available decisions are clear.
- [ ] Long and streaming content scrolls without losing essential controls or context.
- [ ] Focus is contained and restored correctly, and keyboard interaction is complete.
- [ ] Narrow and wide designs are intentional in both themes.
- [ ] Storybook interaction, accessibility, and relevant frontend tests pass.
