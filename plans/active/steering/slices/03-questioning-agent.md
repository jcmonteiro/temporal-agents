# Slice 3 — Being questioned (optional agent turns)

**Discharges:** IB §3 (peer producers, no cost until asked, read-only agent, governed
instruction, conversation never reaches the implementing agent), IB §5 (never blocks
the loop).

**Demo:** an operator asks to be questioned; the agent asks one question at a time,
each answer produces the next question, and asking to finish produces a guidance draft
the operator can edit. With the agent unavailable, giving guidance directly, proceeding
without guidance, and stopping all still work.

## Tasks

- [x] Add one agent turn per exchange, bounded and heartbeating, sharing one
      conversational session for the whole steering unit; document the identity
      constraint that makes that possible.
- [x] Run the questioning agent **read-only**; it may read the repository and the review
      material and must not modify anything.
- [x] Add the questioning instruction as a governed key under the prompts feature,
      resolved once per session.
- [x] Append every turn's output to the durable conversation through a sink port, so a
      consumer can read it as it is produced.
- [x] Add the "finish" turn that condenses the exchange into a guidance draft, written
      into the session's editable guidance text.
- [x] Attribute each answer to the principal who gave it, and accumulate the session's
      own token usage.
- [x] Ensure a failed or unavailable turn leaves every non-conversational path
      functional and reports the failure to the operator.
- [x] Tests: workflow tests for the turn protocol and for turn failure; the sink
      receives output incrementally; the implementing agent's input contains the
      guidance text only, never the exchange; cost accumulates per session.

## Done when

An operator can be questioned into good guidance, the exchange is durable and
attributed, the loop cannot be blocked by the conversational layer, and no exchange
text leaks into the implementing agent's input.
