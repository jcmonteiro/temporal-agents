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

The default listener is `127.0.0.1:8973`. Responses can contain workflow goals and
prompts. The server accepts only loopback Host names, its concrete listener host, and
names supplied with `--allow-host`. This blocks DNS-rebinding requests that use a
hostile hostname.

The CLI `list` command reads this API at the default versioned endpoint. Set
`AGENT_HUB_API_URL` to a different `/api/v1` endpoint. If authentication is enabled,
set `AGENT_HUB_AUTH_TOKEN`; the CLI sends it as a bearer token. The CLI refuses a
non-loopback plaintext HTTP endpoint.

A non-loopback bind requires TLS and bearer authentication. Set a random token in
the environment; do not put a fixed token in a command-line argument. The token must
contain at least 32 characters.

```sh
export AGENT_HUB_AUTH_TOKEN="$(openssl rand -base64 32)"
temporal-agents serve --addr 0.0.0.0:8973 \
  --tls-cert /run/secrets/hub.crt \
  --tls-key /run/secrets/hub.key \
  --allow-host hub.example.test
curl -H "Authorization: Bearer $AGENT_HUB_AUTH_TOKEN" \
  https://hub.example.test:8973/api/v1
```

The certificate must be valid for each remote host name. A TLS reverse proxy is also
valid when this process stays on its default loopback listener. If that proxy is
reachable from another host, `AGENT_HUB_AUTH_TOKEN` is required even though the
process itself still listens on loopback. The proxy must keep the upstream connection
on the same host and must preserve the client's `Authorization` header unchanged.

The server explicitly allows its own configured origins for the bundled UI. Each
additional browser origin must be listed separately:

```sh
temporal-agents serve --allow-origin http://localhost:5173
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
| `GET /api/v1/runs/{id}` | One standalone chain |
| `GET /api/v1/schedules` | One item per schedule |
| `GET /api/v1/dismissals` | View-state dismissals in force |
| `POST /api/v1/dismissals` | Hide one finished fleet or run |
| `DELETE /api/v1/dismissals/{id}` | Make the item visible again |

Collections accept `limit` from 1 to 200 and default to 25. Existing fleet, run,
schedule, and dismissal collections keep their original v1 envelope with `items`,
`count`, and `limit`.

`/active-work` is an additive resource for complete active-work reads. Its
`active-work-collection.v1` envelope also has `next`. Follow that URL without
inspecting or changing its opaque `cursor`; `next` is `null` on the final page. Each
request reads at most one native Temporal execution page or one native schedule
page. Continue-as-new changes the current run timestamp, but does not move the chain
across these source-native pages. The CLI follows every page. Use `history` for the
durable execution record.

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

In the active-work projection, `running` reports whether the parent fleet execution
is unsettled. It is separate from fleet status because status is aggregated from
node states. A running fleet can consequently have `todo`, `failed`, or
`waiting-input` status. The existing `fleet.v1` model is unchanged and does not add
this field.

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

### Schedule model

A schedule resource is identified by schedule ID. Its status is:

1. `paused` when the schedule is paused
2. `in-progress` when one or more actions are running
3. the latest completed action's `done` or `failed` result
4. `todo` when no action has completed

A schedule is recurring. It has no progress, no terminal state, and cannot be
dismissed.

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
