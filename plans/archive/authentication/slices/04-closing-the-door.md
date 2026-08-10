# Slice 4 — Closing the door (no open surface left)

**Discharges:** IB §2 (mutation request rules, non-loopback refusal), IB §4
(explicit and loud opt-out only), brief outcome (no unauthenticated mode by
accident).

**Demo:** every API route refuses an unauthenticated request; a cross-site request
is refused even from the same machine; a non-loopback bind without a configured
credential refuses to start; the CLI and `list` still work with a token; the
explicit local opt-out prints a warning naming the risk.

## Tasks

- [x] Require authentication on every route except sign-in, callback, and health.
- [x] Enforce, for mutations: same-site request, non-simple content type, no
      credentialed cross-origin access, with a clear problem document per refusal.
- [x] Refuse to start on a non-loopback bind without a configured credential;
      keep loopback the default.
- [x] Make any authentication-off switch loopback-only, explicit, and loud at
      startup.
- [x] Verify the CLI paths (`list`, and any store-backed command) against an
      authenticated server; update documentation.
- [x] Tests: route matrix asserting refusal without a credential; cross-site
      refusal; non-loopback refusal without a credential; opt-out warning present;
      CLI token accepted.

## Done when

There is no reachable unauthenticated surface, cross-site requests cannot mutate,
exposure requires a credential, and automation is documented and green.

## Delivered

- `httpapi.New` refuses to build a server that neither authenticates nor was asked
  not to; the open mode is `AGENT_HUB_ALLOW_UNAUTHENTICATED`, loopback-only and
  announced on every start.
- Everything under the API's path needs a credential except sign-in, the callback
  and health. The application bundle and the well-known catalogue stay public: the
  page that offers the sign-in has to load, and neither carries data.
- A change (`POST`, `DELETE`) is refused when the browser reports it as cross-site;
  a client that declares nothing is unaffected, so automation keeps working.
- An unconfigured loopback hub signs in against the local compose provider, and mints
  the automation token itself (`<user config dir>/temporal-agents/api-token`, 0600),
  so both a browser and `list` work with no configuration and no open port.
