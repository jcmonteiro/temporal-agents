# Slice 2 — Sign-in through a provider (server-side exchange)

**Discharges:** IB §1 (confidential client, strict verification, provider as
configuration), IB §2 (session records, cookie rules), IB §3 (principal recorded),
IB §4 (local provider in the compose stack).

**Demo:** with the local provider container running, hitting the sign-in route
redirects to the provider, signing in returns to the hub with a session cookie,
and an API read succeeds. Ending the session immediately makes the same read fail.

## Tasks

- [ ] Add the identity-provider seam in the core with a metadata-discovery adapter:
      discovery, key rotation, and checks of issuer, audience, expiry, request
      binding, and replay protection.
- [ ] Add the authentication port with **two adapters**: server-side session, and
      the existing static token. The transport asks the port, never the adapter.
- [ ] Add the session store (own migrations): create, look up, refresh the stored
      provider tokens, end, expire. Ending a session takes effect on the next
      request.
- [ ] Add the principal record (stable subject, display fields when provided),
      created or updated at sign-in.
- [ ] Add the sign-in, callback, sign-out, and "who am I" routes. Set the cookie
      script-inaccessible, same-site at the weakest level the provider redirect
      requires, and secure when served over TLS.
- [ ] Add attempt limiting and constant-time comparisons on the credential-accepting
      routes.
- [ ] Add the local provider to `docker-compose.yml` with static local users and
      document the configuration values.
- [ ] Unit tests: rejected callbacks (bad binding, replayed request, wrong audience,
      expired token, unsigned token); session lookup and expiry; port selects the
      right adapter per credential.
- [ ] Integration tests with the provider in a container: a real code exchange, a
      refresh, and a revoked session.

## Done when

A browser signs in against a real provider, the API accepts the resulting session,
tokens never reach the browser, ending a session is immediate, and the CLI's token
path still authenticates.
