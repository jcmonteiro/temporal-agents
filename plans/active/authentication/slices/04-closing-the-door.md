# Slice 4 — Closing the door (no open surface left)

**Discharges:** IB §2 (mutation request rules, non-loopback refusal), IB §4
(explicit and loud opt-out only), brief outcome (no unauthenticated mode by
accident).

**Demo:** every API route refuses an unauthenticated request; a cross-site request
is refused even from the same machine; a non-loopback bind without a configured
credential refuses to start; the CLI and `list` still work with a token; the
explicit local opt-out prints a warning naming the risk.

## Tasks

- [ ] Require authentication on every route except sign-in, callback, and health.
- [ ] Enforce, for mutations: same-site request, non-simple content type, no
      credentialed cross-origin access, with a clear problem document per refusal.
- [ ] Refuse to start on a non-loopback bind without a configured credential;
      keep loopback the default.
- [ ] Make any authentication-off switch loopback-only, explicit, and loud at
      startup.
- [ ] Verify the CLI paths (`list`, and any store-backed command) against an
      authenticated server; update documentation.
- [ ] Tests: route matrix asserting refusal without a credential; cross-site
      refusal; non-loopback refusal without a credential; opt-out warning present;
      CLI token accepted.

## Done when

There is no reachable unauthenticated surface, cross-site requests cannot mutate,
exposure requires a credential, and automation is documented and green.
