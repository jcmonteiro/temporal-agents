# Slice 5 — The steering surface (one shared modal)

**Discharges:** brief outcome (guide, decline, or stop from the hub), IB §3 (one
artifact, mandatory text when guiding, optional questioning, bounded text), IB §6
(prominence of waiting work and visible cost).

**Demo:** from the overview and from a run page, the same modal opens on a waiting
session: it shows what the decision is about, a guidance field, an option to be
questioned, and the three decisions. Building with an empty field is impossible;
proceeding without guidance is one click; stopping ends the loop. The loop resumes and
the modal closes.

## Tasks

- [ ] Add one shared modal component, mounted once and opened from any surface, so there
      is a single implementation.
- [ ] Show the material under decision, the editable guidance field, the conversation
      when one exists, the session's cost, since when it has waited, and who has
      contributed.
- [ ] Make the three decisions available, with the guiding decision disabled while the
      field is empty and an explanation of why.
- [ ] Add the questioning affordance: start it, answer inline as text streams, finish it
      into the editable field; make clear that no cost is incurred until it is started.
- [ ] Show the guidance bound and refuse over-long text with the server's explanation.
- [ ] Make waiting work prominent on the overview and on the place page (state plus
      since when).
- [ ] Handle a session decided elsewhere: the modal reports the recorded decision and
      closes, rather than failing.
- [ ] Component tests: empty guidance cannot build; a decision is sent once per click
      burst; streamed text renders in order; a session decided elsewhere resolves
      gracefully; the modal is keyboard operable and focus-managed.

## Done when

An operator guides, declines, or stops a waiting loop from one shared surface reachable
from both the overview and the run page, with no way to submit empty guidance.
