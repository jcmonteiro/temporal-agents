# Slice 2 — Sessions and decisions through the API

**Discharges:** IB §4 (durable store authoritative), IB §5 (visible failure on a
failed decision), authentication feature's mutation rules.

**Demo:** while a run waits, the API lists the waiting session, returns the material the
decision is about, and accepts a decision; a second decision returns the first one;
an unauthenticated or cross-site attempt is refused; restarting the API loses nothing.

## Tasks

- [ ] Add the steering store behind its own port, with its own migrations and **no
      cross-context foreign keys**: sessions with their state, material reference, and
      outcome; append-only messages with a monotonic sequence.
- [ ] Add reads: the sessions currently waiting, and one session with its material,
      guidance text, conversation so far, cost so far, and who has contributed.
- [ ] Add the decision write: proceed with guidance, proceed without guidance, stop.
      Validate guidance presence and bound; refuse over-long guidance with an
      explanation; record the deciding principal.
- [ ] Make the write idempotent: a repeat returns the recorded decision.
- [ ] Fail visibly if the decision cannot be durably recorded; never report success
      optimistically.
- [ ] Reflect the waiting state in the existing item read path (needs input, since when,
      which session).
- [ ] Enforce authentication and the mutation request rules.
- [ ] Tests: unit tests for validation and idempotency with a fake store; integration
      tests with containers for durability and sequence monotonicity; contract tests for
      the waiting-state field.

## Done when

The hub can discover a waiting decision, read what it is about, and decide it exactly
once, with the record surviving a restart.
