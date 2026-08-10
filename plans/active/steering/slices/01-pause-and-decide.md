# Slice 1 — Pause and decide (the durable spine)

**Discharges:** IB §1 (durable wait, unbounded, no second satellite, own execution
row, stable identity), IB §2 (pause points, three decisions, first decision wins,
named endings, opt-in setting).

**Demo:** with steering switched on for a place, a local review round pauses instead of
implementing; the run reports that it needs input; sending each of the three decisions
by hand (through orchestration tooling, before any interface exists) resumes the loop
correspondingly; with steering off, the loop behaves exactly as before.

## Tasks

- [x] Add the steering unit as a durable child of the review pass: it awaits a decision
      with **no timeout**, returns the decision and the guidance text, and never
      restarts itself in a way that would change its identity.
- [x] Consume the scoped enablement setting, resolved once at the start of the work;
      when off, no steering unit is created and no behaviour changes.
- [x] Insert the two pause points, each immediately before the agent acts on review
      material.
- [x] Accept the three decisions as signals; **ignore all but the first** and make a
      repeat observable as the recorded decision rather than an error.
- [x] Compose the guidance into the agent's input as an additive fenced block, after
      the instruction and before the material, through the existing pure composition
      function; no existing section changes.
- [x] Refuse a decision that claims guidance but carries none.
- [x] Replace the loop's single convergence flag with a **named ending** —
      converged, accepted by a human, stopped by a human, limit reached — keeping the
      existing flag written for compatibility.
- [x] Flip the parent run's reported state to "needs input" while a session waits, with
      no new item appearing anywhere.
- [x] Record the steering unit's own execution row for token attribution, and exclude
      its class from overview items.
- [x] Tests: workflow tests for each decision path; a repeated decision starts one
      implementation pass; steering-off is byte-identical to today; composition table
      test for the fenced block; replay tests for the new histories.

## Done when

A review round can wait indefinitely for a human, resumes correctly on any of the
three decisions, records why it ended, appears as one item that needs input, and is
inert when steering is off.
