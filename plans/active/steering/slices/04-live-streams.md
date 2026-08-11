# Slice 4 — Live streams (conversation and hub events)

**Discharges:** IB §4 (resumable streaming by sequence, push carries events not data),
authentication feature's stream constraint.

**Demo:** the questioning agent's text appears in the browser as it is produced; a
reload resumes from where it stopped with nothing missing and nothing duplicated; a
second tab shows the same conversation; a run that starts waiting announces itself
without a page refresh; an expired session ends the stream cleanly.

## Tasks

- [x] Add a conversation stream for one session, resumable from a client-supplied
      sequence position, reading the append-only conversation.
- [x] Add a hub event stream carrying small events only (a session started waiting, a
      session was decided elsewhere, an item changed state) — never payloads and never
      list data.
- [x] Keep list reads on polling; an event may trigger an immediate refetch, but push is
      not the source of truth.
- [x] Handle an expired credential by ending the stream with a clear signal, so the
      client redirects to sign-in and reconnects afterwards rather than hanging.
- [x] Bound the number of concurrent streams per session and per credential, and release
      resources on disconnect.
- [x] Tests: resume from a sequence yields no gaps or duplicates; two concurrent readers
      see the same sequence; an expired credential terminates the stream; an event
      triggers exactly one refetch.

## Done when

The conversation and hub events reach the browser live, survive reloads and second
tabs, and never become an alternative source of truth for list state.
