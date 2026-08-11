# Temporal Agents REST API

The REST API is the data boundary for Agent Hub. It describes agent **work** as
fleets, standalone run chains, and schedules. It does not expose Temporal or
Postgres records as a public model.

## Start it

The worker must have started at least once, because the worker owns the execution
record and plan schema. The API owns and applies the dismissal schema itself.

```sh
export DATABASE_URL=postgres://postgres:postgres@localhost:15432/temporal_agents?sslmode=disable
temporal-agents serve
```

The default listener is `127.0.0.1:3000`. Responses can contain workflow goals and
prompts. The server accepts only loopback Host names, its concrete listener host, and
names supplied with `--allow-host`. This blocks DNS-rebinding requests that use a
hostile hostname.

The CLI `list` command reads this API at the default versioned endpoint. Set
`AGENT_HUB_API_URL` to a different `/api/v1` endpoint. If authentication is enabled,
set `AGENT_HUB_AUTH_TOKEN`; the CLI sends it as a bearer token. The CLI refuses a
non-loopback plaintext HTTP endpoint.

## Sign in

A person signs in with an identity provider; a script keeps using the bearer token.
Both credentials are resolved by one port, so a resource never branches on which was
presented.

The local compose stack runs a provider (Dex) with one operator, and a loopback
listener signs in against it by default, so signing in needs no external account and
no configuration:

```sh
docker compose up -d
temporal-agents serve
# then open http://127.0.0.1:3000/
# operator@example.test / operator
```

Point the hub at another provider with `AGENT_HUB_OIDC_ISSUER`; the client id and
secret are then required, and nothing is defaulted. `AGENT_HUB_PUBLIC_URL` states the URL a browser reaches the hub at,
which is what the provider redirects back to; it is derived from `--addr` when that
names a host, and required behind a proxy or on `0.0.0.0`. The provider's local
configuration, including the operator's credentials, is in `deploy/dex/config.yaml`.

The routes are:

| Route | Purpose |
|---|---|
| `GET /api/v1/auth/sign-in?return=<path>` | Redirects to the provider. `return` is honoured only when it is a path inside the application. |
| `GET /api/v1/auth/callback` | Where the provider sends the browser back. Sets the session cookie and redirects. |
| `GET /api/v1/auth/session` | Who the request is made by (`session.v1`). |
| `DELETE /api/v1/auth/session` | Ends the session immediately and clears the cookie. |

The API is the confidential client. The authorization-code exchange, the refresh and
the provider's tokens are server-side; the browser holds one `HttpOnly`, `SameSite=Lax`
session cookie and nothing else. The cookie is also `Secure` when the deployment serves
TLS. Sessions are server-side records, so ending one takes effect on the next request
rather than at the next expiry.

A request without an accepted credential is `401` with `WWW-Authenticate` and a
`Link: <…/auth/sign-in>; rel="authenticate"` header. A credential that could not be
*checked* — an unreachable store — is `503`, not `401`: an outage must not read as
everybody being signed out.

Every route needs a credential except three: `auth/sign-in` and `auth/callback`,
which are how a credential is obtained, and `health`, which a monitor probes and
which discloses only whether dependencies answer. The application bundle is not an
API route and stays reachable, because the page offering the sign-in has to load.

## Automation

`list` and any other script authenticate with `AGENT_HUB_AUTH_TOKEN` and no browser.
On a loopback listener `serve` mints that token on first start and stores it in
`<user config dir>/temporal-agents/api-token`, readable only by its owner, and `list`
on the same machine reads it from there — so neither command needs configuring. A
listener that is not loopback never mints one: there, the token is set deliberately
and must contain at least 32 characters.

There is one way to serve an open API, and it is explicit:
`AGENT_HUB_ALLOW_UNAUTHENTICATED=1`. It is refused on a non-loopback listener and the
process announces it on every start.

A non-loopback bind requires TLS and bearer authentication. Set a random token in
the environment; do not put a fixed token in a command-line argument. The token must
contain at least 32 characters.

```sh
export AGENT_HUB_AUTH_TOKEN="$(openssl rand -base64 32)"
temporal-agents serve --addr 0.0.0.0:3000 \
  --tls-cert /run/secrets/hub.crt \
  --tls-key /run/secrets/hub.key \
  --allow-host hub.example.test
curl -H "Authorization: Bearer $AGENT_HUB_AUTH_TOKEN" \
  https://hub.example.test:3000/api/v1
```

The certificate must be valid for each remote host name. A TLS reverse proxy is also
valid when this process stays on its default loopback listener. If that proxy is
reachable from another host, `AGENT_HUB_AUTH_TOKEN` is required even though the
process itself still listens on loopback. The proxy must keep the upstream connection
on the same host and must preserve the client's `Authorization` header unchanged.

The server explicitly allows its own configured origins for the bundled UI. Each
additional browser origin must be listed separately:

```sh
temporal-agents serve --allow-origin http://127.0.0.1:3001
```

`web/dist` is served as a single-page application for local convenience when it
exists. Use `--web-dir=` for JSON only or `--web-dir <path>` for another bundle.
Static hosting is independent of the API: the same bundle can be hosted elsewhere
without changing any API URL or payload.

## Discover the contract

| Resource | Purpose |
|---|---|
| `GET /api/v1` | API entry point, resource catalogue, vocabulary, and limits |
| `GET /api/v1/openapi.json` | OpenAPI 3.1 contract |
| `GET /api/v1/schemas` | Versioned model schema index |
| `GET /api/v1/schemas/{model}` | Self-contained JSON Schema 2020-12 document |
| `GET /api/v1/problems` | Problem type catalogue |
| `GET /api/v1/problems/{code}` | Meaning and recovery guidance for one problem type |
| `GET /api/v1/health` | Readiness of Temporal, the execution store, and the dismissal store |
| `GET /.well-known/api-catalog` | Host-level linkset to the description, contract, and health resource |

A successful model response includes a `Link: <...>; rel="describedby"` header.
The linked schema is versioned independently, such as `fleet.v1`. The major API
version is in the URL. A future breaking API does not replace `/api/v1`; consumers
move to it deliberately.

## Resources

| Method and path | Meaning |
|---|---|
| `GET /api/v1/active-work` | Paged active top-level fleets, runs, and schedules |
| `GET /api/v1/fleets` | Running fleets and non-dismissed terminal fleets |
| `GET /api/v1/fleets/{id}` | One fleet and its plan node graph |
| `GET /api/v1/runs` | Running standalone chains and non-dismissed terminal chains |
| `POST /api/v1/runs` | Start a develop or review pass in a known place |
| `GET /api/v1/runs/{id}` | One standalone chain |
| `GET /api/v1/schedules` | One item per schedule |
| `GET /api/v1/dismissals` | View-state dismissals in force |
| `POST /api/v1/dismissals` | Hide one finished fleet or run |
| `DELETE /api/v1/dismissals/{id}` | Make the item visible again |
| `GET /api/v1/places` | The places the hub may work in |
| `POST /api/v1/places` | Register a place |
| `GET /api/v1/settings` | What the tool is configured to do, and where each value came from |

Collections accept `limit` from 1 to 200 and default to 25. Existing fleet, run,
schedule, and dismissal collections keep their original v1 envelope with `items`,
`count`, and `limit`. The fleet, run, and schedule collections additionally publish
`locations` (see [Locations](#locations)).

`/active-work` is an additive resource for complete active-work reads. Its
`active-work-collection.v1` envelope also has `next`. Follow that URL without
inspecting or changing its opaque `cursor`; `next` is `null` on the final page. Each
request reads at most one native Temporal execution page or one native schedule
page. It does not load fleet trees or plan nodes. Continue-as-new changes the current
run timestamp, but does not move the chain across these source-native pages. The CLI
follows at most 1,000 pages and retains at most 200,000 items during one overview
read. Its complete read also has a 35-second deadline. Use `history` for the durable
execution record.

All times are RFC 3339 UTC timestamps. Missing times are JSON `null`, not the year
1. Successful GET responses carry a strong `ETag` and support `If-None-Match`.
This makes regular polling return `304 Not Modified` with no body when the model has
not changed.

### Fleet model

A fleet is identified by the parent workflow ID. Its plan comes from the stored plan
handle written by that fleet execution. This is authoritative for the current code:
plans are durable Postgres records, so an ambient plan file or a decoded workflow
start input is neither needed nor trusted.

The detail resource reconciles the plan against child workflow IDs of the form
`<fleet-id>-<node-id>`. Each node has its plan prompt, predecessor links, derived
status, and child execution when it started. There are no cross-fleet edges and no
fabricated owner, estimate, or description.

In the active-work projection, `running` and `status` report only the parent fleet
execution facts from the bounded Temporal page. A running fleet therefore has
`in-progress` status. Node aggregation is available from the fleet collection and
detail resources, which can load the complete fleet tree. The existing `fleet.v1`
model is unchanged and does not add `running`.

Fleet status is aggregated by the server. The first matching rule wins:

1. no nodes: `todo`
2. any `failed`: `failed`
3. any `waiting-input`: `waiting-input`
4. any `in-progress`: `in-progress`
5. any `paused`: `paused`
6. all `done`: `done`
7. otherwise: `in-progress` when any node is done, or `todo`

A failed parent remains failed even when no node started. A running parent is not
reported done while it still has orchestration work to finish. Progress is done
nodes divided by all plan nodes. Skipped and blocked nodes remain in the denominator
and never enter the numerator.

### Run model

A run resource is a standalone execution **chain**. Its identity is the workflow ID,
which is stable over continue-as-new. The active-work projection reports current
liveness in `running`; the existing `run.v1` model is unchanged. The latest iteration
supplies status, the first known iteration supplies start time, and token usage is
summed from each iteration's incremental count. A chain is never returned as one
item per run ID.

Schedule-fired runs are represented by their schedule. Child workflows are
represented by their parent. They are excluded from `/runs` to avoid showing the
same work twice.

`GET /api/v1/runs/{id}` additionally carries the run's **provenance**: `startedBy`,
which names who started it *from the hub* and is absent for a run begun on the
command line or fired by a schedule, and `instructions`, which names the stored
instruction the run resolved for each governed key (`key`, `scope`, `version`,
`hash`). The instruction's text is deliberately not published there: a record that
carried it would drift from the version it claims to be. Neither field is on the run
collection, which no consumer reads them from.

### Schedule model

A schedule resource is identified by schedule ID. Its status is:

1. `paused` when the schedule is paused
2. `in-progress` when one or more actions are running
3. the latest completed action's `done` or `failed` result
4. `todo` when no action has completed

A schedule is recurring. It has no progress, no terminal state, and cannot be
dismissed. The lightweight active-work projection always sets schedule `running` to
`false`. Its schedule status uses configuration and the last observed completed
action. Use the schedule resource when reconciled current action liveness is needed.

### Locations

Every item reports **where** it runs, and every response that carries work publishes
the places it refers to:

- Each fleet, run, schedule, and fleet node carries `locationId`, an **opaque**
  server-issued reference. Never parse it: the natural key (the directory, or the
  ref) is published as a field of its own.
- Each work collection carries a `locations` array (the dismissal collection does
  not: a dismissal is view state and runs nowhere); the single-item resources
  (`/fleets/{id}`, `/runs/{id}`) carry the same array themselves, because they have
  no envelope to hold it.
- A location is a **tagged union** discriminated by `kind`:
  - `unknown` — nothing was recorded about where the work ran. It is a real,
    rendered place, not a null branch. It carries no directory, no ref, and no
    parent, and it is always present in the registry.
  - `directory` — an absolute, cleaned path in `directory`.
  - `remote` — work with no local directory, identified by `ref`. A git ref of a
    piece of work is an attribute of that work, not a location.
- Every location carries a server-computed `label`. No consumer derives a display
  name from a path.
- The registry is **flat**, **closed under ancestry** (every referenced place plus
  all of its ancestors), holds each place exactly once, and is ordered
  **parents-first** with a deterministic order: a published `parentId` always names
  a place published earlier in the same array, so following it terminates. The set
  and its order come from the places alone, never from the order the server happened
  to collect them in, so a client builds the tree in one pass and the response's
  entity tag stays stable for an unchanged read.
- The fleet, run, and schedule collections always carry `locations`, even when
  `items` is empty: the unknown place is always in the registry.
- On the paged active-work model the reference and the registry are **optional**
  members, so an existing consumer decodes the payload unchanged.

The union and the registry are published as `location.v1` under `/api/v1/schemas`.

#### How a place is established

A place is only ever a **probed fact**, never an inference:

- When a unit of work starts, the server asks git where its working directory is:
  the working tree the directory belongs to, and whether that working tree is a
  linked worktree of some repository. Those two answers are recorded with the
  execution.
- A `directory` place is the recorded working tree. It has a `parentId` **only**
  when git reported the work runs in a linked worktree, in which case the parent is
  the repository that worktree was created from. One path containing another never
  makes a parent: git puts a worktree outside its repository by default.
- A fleet reports the repository it orchestrates from, while each of its nodes
  reports the worktree it develops in — so a node's place hangs under its fleet's.
- A schedule runs nothing itself, so it reports the place of the most recent run it
  fired that recorded one.
- Work whose place could not be established — a directory in no repository, a git
  that could not answer, or an execution started before the server probed at all —
  is reported as `unknown`. It is never guessed.

### Status vocabulary

The closed status vocabulary is:

- `todo`
- `in-progress`
- `paused`
- `waiting-input`
- `waiting`
- `done`
- `failed`

Only states supported by a plan, an execution, or a schedule are emitted. A canceled,
timed-out, terminated, or otherwise unsuccessful execution is `failed`. A blocked
fleet node is `waiting-input`. A node skipped because a prerequisite did not succeed
is `paused`.

## Dismissals

A dismissal is durable operator view state. It never signals, cancels, or changes a
workflow. Only terminal fleets and runs (`done` or `failed`) can be dismissed. A
schedule cannot be dismissed.

```http
POST /api/v1/dismissals
Content-Type: application/json

{"kind":"run","itemId":"run-..."}
```

The response is `201 Created`, with a `Location` header such as:

```text
/api/v1/dismissals/run:run-...
```

The identity is derived from kind and item ID. Repeating the same POST is idempotent
and keeps the original dismissal time. Delete the returned resource to undo it.

## Starting work

```http
POST /api/v1/runs
Content-Type: application/json

{"requestId":"5f9c…","kind":"develop","placeId":"5f2b…","prompt":"make the flaky test pass"}
```

```json
{"id":"develop-1f2e…","kind":"run","type":"develop","label":"make the flaky test pass",
 "locationId":"5f2b…","startedAt":"2026-08-06T12:00:00Z","startedBy":"https://issuer.test|operator-1",
 "locations":[…]}
```

Three rules are the server's, and none of them is the client's to remember:

- **A request never carries a path.** It names a place, and the server resolves the
  working tree from its own registry — the places it registered, and the places it
  has watched work run in. There is no directory field, and an unknown field is
  refused rather than ignored.
- **One request identity is one run.** `requestId` is the caller's own; the same
  identity always names the same execution, so a double click, a retried request or a
  reload answers `201` with the run it already started. Mint a fresh identity per
  intent, not per attempt.
- **Work that would collide is refused** with `409` (`place-is-busy`), naming what is
  already running there: two loops in one working tree stash and commit over each
  other. The problem document carries `conflictingRunId`, so a consumer links to the
  work in the way instead of parsing the detail. A linked worktree is a place of its
  own, so work in it does not make its repository busy.

What may be started is `develop` (with a `prompt`) and `review` (without one). A
fleet needs its plan approved and a schedule a recurrence, so neither is started
here.

The answer is `started-work.v1`, not a run: a start returns as soon as the
orchestrator accepts the submission, so the resource carries the run's identity, its
place and its provenance, and no status, iterations or token usage — those are facts
only once the work is observable. Follow the `Location` header to `GET
/api/v1/runs/{id}`, which answers `404` for a moment while the orchestrator catches
up.

## Places

A place with work in it is *observed*: the location probe recorded it, so every work
collection publishes it. A place with **no** work in it cannot be observed, so it is
registered:

```http
POST /api/v1/places
Content-Type: application/json

{"directory":"/srv/repos/pricing"}
```

```json
{"locationId":"5f2b…","registeredAt":"2026-08-06T12:00:00Z","registeredBy":"https://issuer.test|operator-1",
 "locations":[{"id":"unknown","kind":"unknown","label":"Unknown","parentId":null},
              {"id":"5f2b…","kind":"directory","label":"pricing","parentId":null,"directory":"/srv/repos/pricing"}]}
```

The request names a directory and nothing else. What is registered is what the
location probe establishes about it, so naming a subdirectory registers the working
tree that holds it, and a worktree hangs under the repository the probe names — a
registration can state no hierarchy of its own. The directory must be absolute and
written plainly (`400`), and it must exist on the machine the work runs on and be
held by a repository (`422`, with the detail saying which of the two is missing).

Registering the same place again answers `201` with the registration that is already
there: the identity is the place, so a retried request, a double click and a second
operator all address one resource.

`GET /api/v1/places` lists the registered places with the registry their
`locationId` resolves against. It is the only read that reports a place nothing has
ever run in.

## Settings

A setting is what the tool is configured to *do*, as opposed to what an agent is
*told* (that is an instruction, and it is configured through the same chain). Every
setting resolves through the place work runs in, then the repository that place
belongs to, then the installation, then the value this build ships — per setting, so
a place can override one and inherit the rest.

```http
GET /api/v1/settings
```

```json
{"items":[{"key":"steering.enabled","purpose":"Stop a review round …",
           "enabled":true,"source":"factory","version":1}],"count":1,"limit":1}
```

`source` is the *kind* of scope the value came from — `directory`, `global`, or
`factory` — never the scope itself: a scope names an absolute path on the server, and
this field is read for "where was this set". `version` is which stored version
answered, or `0` when storage holds none and the answer is the value the build ships.

The server resolves inheritance, so a consumer never re-derives it. Reading a
setting as it applies to one place, and writing one, arrive with the configuration
surface.

## Errors

Every API failure is `application/problem+json`. The `type` URI resolves to a stable
problem description under `/api/v1/problems`, and `requestId` matches the structured
server log. Dependency and driver details are never returned.

Transient `429` and `503` responses include `Retry-After`. The API refuses partial
results when Temporal or a store cannot be reached: an incomplete overview would
look like work had disappeared.

Write requests must use `application/json`, must fit within 64 KiB, and reject unknown
fields and multiple JSON documents.

## Compatibility and service expectations

- `/api/v1` does not introduce breaking contract changes. New optional fields and new
  resources can be added. Existing meanings, required fields, and enum values are not
  changed in place.
- Model schemas have explicit names such as `run.v1`. A breaking model is published
  under a new model name and can coexist with the old one.
- Deprecation is announced with the standard `Deprecation` header. A planned removal
  also carries `Sunset`; consumers are not moved automatically.
- This is a self-hosted local process, so there is no hosted availability SLA. A
  request has a 30-second application deadline. Consumers must handle `429`, `503`,
  and `504` and respect `Retry-After`.
- The model is independent of locale and deployment region. Times are UTC and text is
  UTF-8 JSON. Availability is the responsibility of the process and its local
  Temporal and Postgres dependencies.

## Security notes

- There is no unauthenticated mode that can be left on unnoticed: a server that
  neither authenticates nor was explicitly asked not to refuses to start.
- A change (`POST`, `DELETE`) is refused when the browser reports it as cross-site.
  Loopback binding is no defence: any page can send a request to a local port.
- Signing in is server-side. No provider token, refresh token or identity ever reaches
  the browser, and the session cookie is script-inaccessible and same-site.
- A callback is accepted once, only when it is bound to a sign-in this server started
  for this browser (state, nonce, PKCE, and a server-side pending record). Which check
  refused a callback is never disclosed.
- The sign-in, callback and session routes have their own, tighter attempt limit,
  because they are the only routes where trying repeatedly could pay.
- Loopback is the default bind. A non-loopback `--addr` requires `--tls-cert`,
  `--tls-key`, and a strong `AGENT_HUB_AUTH_TOKEN`. Plaintext remote HTTP is refused.
- Every request Host must match the loopback names, the concrete listener host, or an
  exact `--allow-host` entry.
- A supplied browser Origin is rejected unless it is one of the server's own origins
  or an exact `--allow-origin` entry. Wildcards are not accepted.
- Requests have body, rate, and time limits. Responses use restrictive browser
  security headers.
- Postgres remains bound to loopback in the local compose stack.
- No DSN, stack trace, driver error, workflow failure detail, or request body is
  written into an API response or access log.
