# Implementation brief — Authentication

Derived from `brief.md`. Constraints and seams; concrete choices are marked
**hard constraint** with the reason that forces them.

## 1. Topology (hard constraint)

- **The API is the confidential client; the browser holds no token.** The
  authorization-code exchange, the refresh, and the token storage happen
  server-side; the browser receives only a session cookie that scripts cannot
  read and that is not sent on cross-site requests.
  *Reasons, each independently sufficient:* one named target provider has no
  secret-less flow at all; browser-held refresh tokens are defeated by tracking
  protections and must then live in reachable script storage; a long-lived
  server-push stream must outlive an access token without a client-side dance;
  and per-provider differences stay in one Go adapter instead of in the frontend,
  which the existing frontend brief deliberately keeps free of identity libraries.
- **The provider is configuration, not code:** issuer, client credentials, scopes,
  and claim mapping come from configuration. A provider seam must allow a
  non-standard provider to be added as a second adapter without touching session,
  cookie, or route code.
- **Verification is strict from the first commit:** provider metadata discovery,
  signing-key rotation, and checks of issuer, audience, expiry, request binding,
  and replay protection. Retrofitting these is the expensive path.

## 2. Credentials and sessions (hard constraints)

- **Two credential kinds behind one port:** a browser session and a static token
  for automation. The transport asks only "is this request authenticated, and as
  whom"; it must not branch on the credential kind.
- **Sessions are server-side records**, so ending one is immediate and a leaked
  cookie can be invalidated. A stateless credential would make revocation mean
  "invalidate everyone".
- **The cookie is script-inaccessible and same-site**, with the weakest relaxation
  that still allows a provider redirect back into the app.
- **Mutations additionally require a non-simple request and a same-site origin.**
  *Reason:* any web page can post a simple cross-origin form to a loopback port;
  loopback binding alone is not a defence once the hub can start agents.
- **Non-loopback exposure without a configured credential is refused**, preserving
  the existing trusted-local boundary by construction.

## 3. Identity, not authorization (hard constraint)

- A **principal** is recorded (stable subject, plus display fields when the
  provider supplies them) for attribution.
- **No roles, no scopes, no per-principal filtering of work.** Any authenticated
  principal is the operator.
- Attribution is written where the fact is created (who started work, who answered
  a steering turn), not inferred later.

## 4. Local ergonomics (hard constraint)

- The default local path must remain **one command to start, one click to sign
  in**, with no unauthenticated mode that can be left on unnoticed. Any
  authentication-off switch must be explicit, loopback-only, and loud.
- The local provider runs **in the existing local compose stack**, so a
  contributor gets a working sign-in without external accounts.

## 5. Platform seams this feature must settle

- **Schema ownership changes here**, because this is the first context whose tables
  a second process needs: migration becomes an explicit step, and processes verify
  the schema version and fail fast rather than racing to apply it.
- Each context owns its **own migrations**, and there are **no cross-context
  foreign keys** — contexts must stay independently testable.
- Adapter tests use real containers (per repository policy); domain rules are unit
  tested with no database.

## 6. Frontend seams

- One central place attaches credentials and one central place handles
  "unauthenticated": redirect to sign-in, preserve the intended destination, and
  end any open stream cleanly rather than hanging.
- No identity library, no token in script storage, no silent-renew frame.

## 7. Risks and open decisions

- **Cookie scope versus independently hosted assets.** First-party cookies require
  the app and API to share a site. Recommended deployment fronts both on one
  domain; a genuinely cross-origin bundle pays with relaxed cookie rules and a
  strict origin allowlist. Decide per deployment, not in code.
- **Sign-in brute force** is possible only at the provider, but the callback and
  session endpoints still need attempt limits and constant-time comparisons.
- **Device-code flow for the CLI** is a documented later step; the static token
  covers automation now.
- **Session lifetime** versus an unbounded human wait (later feature): the stream
  must survive token refresh, and an expired session must fail visibly.
