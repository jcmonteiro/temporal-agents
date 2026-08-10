# Slice 6 — Notifications and the inbox

**Discharges:** brief outcome (told a decision is waiting, reminded until acted on,
found waiting hours later), IB §4 (addressing, per-principal read state, daily unlimited
reminders).

**Demo:** a review round starts waiting: the existing channels notify, the hub's inbox
shows the pending decision, and the browser raises a native notification when the
operator has enabled it. A day later, a reminder arrives. Opening the hub after hours
away shows the decision still waiting, with its age. Marking it read in one browser does
not clear it for another principal.

## Tasks

- [ ] Persist notifications (own migrations) with addressing: directed to the initiating
      principal when known, broadcast when not, and **read state per principal**.
- [ ] Emit a notification when a session starts waiting, through the existing
      notification port so the current channels are reached unchanged.
- [ ] Add the daily reminder with **no cap**, driven by the waiting unit's own timer,
      which never ends the wait; stop reminding when the decision is made.
- [ ] Add the inbox surface on the existing bell: newest first, paginated, unread count,
      mark read, clear read, and a link to the deciding surface.
- [ ] Add native browser notifications gated behind an explicit operator action to
      enable them; never request permission on load.
- [ ] Keep host channels global and unchanged.
- [ ] Tests: addressing matrix (initiator known, unknown, scheduled work); read state is
      per principal; reminders repeat daily and stop on decision; the bell count matches
      unread; permission is never requested without a gesture.

## Done when

A waiting decision reaches the operator wherever they are, keeps reminding them daily,
and is still discoverable in the hub with its age when they return.
