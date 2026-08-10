# Slice 5 — Run page and repeat

**Discharges:** brief outcome (repeat a past run in one action), prompts feature
outcome (provenance visible), IB §2 (repeat invents nothing).

**Demo:** a run page shows the run's state, place, instruction, token usage,
iterations, who started it, and which instruction version it used. Choosing repeat
starts an identical run and navigates to it. Repeating a run whose place is gone fails
with a clear message rather than running somewhere else.

## Tasks

- [x] Fill the run page from the existing read path plus provenance: state, place
      with link, instruction, iterations, token usage, timestamps, initiator,
      instruction key/scope/version.
- [x] Add repeat: submit the recorded kind, instruction, options, and place, with a
      fresh request identity; invent nothing the record does not hold.
- [x] Refuse a repeat whose place is no longer registered, and say why.
- [x] Apply the same conflict refusal and idempotency behaviour as the launcher.
- [x] Component tests: page renders each fact including provenance, repeat submits the
      recorded values, a missing place refuses, a double click repeats once.

## Done when

A run explains itself on its own page and can be repeated in one action, with the
same guards as a fresh start.
