# Slice 7 — Limit checkpoint (continue or accept)

**Discharges:** brief outcome (decide at the pass limit; endings recorded honestly),
IB §2 (limit becomes a decision point, unlimited human-gated resets, visible cost).

**Demo:** a loop reaches its pass limit with steering on: instead of stopping, it waits
for a decision; choosing to continue resets the counter and the loop proceeds with
accumulated cost still shown; choosing to accept ends the loop as finished by a human.
History states which happened. With steering off, the limit behaves exactly as today.

## Tasks

- [ ] Turn the pass limit into a decision point when steering is on, reusing the same
      waiting unit and the same surface in a decision mode.
- [ ] Offer continue (counter reset, accumulated cost preserved and displayed), accept
      (loop finished by a human), and stop.
- [ ] Keep questioning available in this mode, and keep guidance optional here because
      the decision is not about the next implementation pass.
- [ ] Record the named ending, and make the reset count and cumulative cost visible both
      in the surface and in history.
- [ ] Keep steering-off behaviour identical to today's limit behaviour.
- [ ] Tests: workflow tests for continue (counter reset, work proceeds), accept (ends as
      accepted), and stop; cost accumulates across resets without double counting;
      steering-off path unchanged; history reports the ending.

## Done when

The pass limit becomes a human decision rather than a dead end, resets are unlimited but
informed, and every ending is named in the record.
