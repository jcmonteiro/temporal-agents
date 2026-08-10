# Slice 2 — Sign-in through a provider (server-side exchange)

**Discharges:** IB §1 (confidential client, strict verification, provider as
configuration), IB §2 (session records, cookie rules), IB §3 (principal recorded),
IB §4 (local provider in the compose stack).

**Demo:** with the local provider container running, hitting the sign-in route
redirects to the provider, signing in returns to the hub with a session cookie,
and an API read succeeds. Ending the session immediately makes the same read fail.

## Tasks

- [x] Add the identity-provider seam in the core with a metadata-discovery adapter:
      discovery, key rotation, and checks of issuer, audience, expiry, request
      binding, and replay protection.
- [x] Add the authentication port with **two adapters**: server-side session, and
      the existing static token. The transport asks the port, never the adapter.
- [x] Add the session store (own migrations): create, look up, refresh the stored
      provider tokens, end, expire. Ending a session takes effect on the next
      request.
- [x] Add the principal record (stable subject, display fields when provided),
      created or updated at sign-in.
- [x] Add the sign-in, callback, sign-out, and "who am I" routes. Set the cookie
      script-inaccessible, same-site at the weakest level the provider redirect
      requires, and secure when served over TLS.
- [x] Add attempt limiting and constant-time comparisons on the credential-accepting
      routes.
- [x] Add the local provider to `docker-compose.yml` with static local users and
      document the configuration values.
- [x] Unit tests: rejected callbacks (bad binding, replayed request, wrong audience,
      expired token, unsigned token); session lookup and expiry; port selects the
      right adapter per credential.
- [x] Integration tests with the provider in a container: a real code exchange, a
      refresh, and a revoked session.

## Done when

A browser signs in against a real provider, the API accepts the resulting session,
tokens never reach the browser, ending a session is immediate, and the CLI's token
path still authenticates.

## Delivered

- Core `internal/identity`: principal, session, pending sign-in, the provider seam,
  and the sign-in sequence (binding, single use, expiry, refresh, revocation).
- `internal/identity/oidcprovider`: discovery, key rotation, and the checks of
  signature, issuer, audience, expiry and nonce, as a confidential client.
- `internal/identity/identitypg`: the context's own schema and migrations; a pending
  sign-in is taken with a deleting read, so a callback is single-use under a race.
- Transport: `GET|DELETE /api/v1/auth/session`, `GET /api/v1/auth/sign-in`,
  `GET /api/v1/auth/callback`; `HttpOnly`, `SameSite=Lax`, `Secure` under TLS; one
  authentication port with the session and the static token behind it; a tighter
  attempt limit on the credential-accepting routes.
- `deploy/dex/config.yaml` and the compose `dex` service, so a contributor signs in
  with no external account. `internal/dextest` runs the same provider in the suites.

Deferred to slice 4 (deliberately): the API is still open when no credential is
configured, and the same-site/non-simple-request rule for mutations is not enforced
yet.
