# Slice 3 — Frontend sign-in and unauthenticated handling

**Discharges:** IB §6 (one place attaches credentials, one place handles
unauthenticated), brief outcome (only a signed-in person can use the hub).

**Demo:** opening the hub while signed out lands on a sign-in page; signing in
returns to the originally requested page; the top bar shows who is signed in and
offers sign-out; an expired session mid-use returns to sign-in rather than showing
a broken page.

## Tasks

- [x] Attach the browser credential in exactly one place in the client boundary;
      no component sends credentials.
- [x] Handle "unauthenticated" centrally: redirect to sign-in, preserve the intended
      destination, and clear any in-memory state that assumed a session.
- [x] Add the sign-in route and the signed-in indicator with sign-out; show the
      principal's display name when the provider supplies one.
- [x] Ensure any long-lived stream added later terminates cleanly and reconnects
      after sign-in, rather than hanging (assert the contract now, even with no
      stream yet).
- [x] Add no identity library, no token in script storage, no hidden renew frame.
- [x] Component tests: unauthenticated read redirects; the intended destination
      survives sign-in; sign-out clears the session and the view; an expired session
      during use redirects once, not in a loop.

## Done when

The hub is unusable while signed out, signing in returns the operator to where they
were going, and the signed-in identity is visible with a working sign-out.

## Delivered

- `clients/http.ts` is the one place a credential is attached (the same-origin
  cookie, nothing else) and the one place a refused credential is noticed; it
  publishes `onUnauthenticated`, which a long-lived stream will subscribe to in
  order to end cleanly rather than hang.
- `platform/session.tsx` holds the answer for the whole application, with four
  states: an outage is `unavailable`, never `signed-out`.
- The shell gates every page, so no component asks whether it may render.
- `pages/SignIn` offers the way in; the top bar shows the provider's display name
  and signs out.
- The frontend has no identity library, no token in script storage, and no renew
  frame; a test asserts the storages stay empty.
