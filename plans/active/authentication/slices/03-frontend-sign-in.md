# Slice 3 — Frontend sign-in and unauthenticated handling

**Discharges:** IB §6 (one place attaches credentials, one place handles
unauthenticated), brief outcome (only a signed-in person can use the hub).

**Demo:** opening the hub while signed out lands on a sign-in page; signing in
returns to the originally requested page; the top bar shows who is signed in and
offers sign-out; an expired session mid-use returns to sign-in rather than showing
a broken page.

## Tasks

- [ ] Attach the browser credential in exactly one place in the client boundary;
      no component sends credentials.
- [ ] Handle "unauthenticated" centrally: redirect to sign-in, preserve the intended
      destination, and clear any in-memory state that assumed a session.
- [ ] Add the sign-in route and the signed-in indicator with sign-out; show the
      principal's display name when the provider supplies one.
- [ ] Ensure any long-lived stream added later terminates cleanly and reconnects
      after sign-in, rather than hanging (assert the contract now, even with no
      stream yet).
- [ ] Add no identity library, no token in script storage, no hidden renew frame.
- [ ] Component tests: unauthenticated read redirects; the intended destination
      survives sign-in; sign-out clears the session and the view; an expired session
      during use redirects once, not in a loop.

## Done when

The hub is unusable while signed out, signing in returns the operator to where they
were going, and the signed-in identity is visible with a working sign-out.
