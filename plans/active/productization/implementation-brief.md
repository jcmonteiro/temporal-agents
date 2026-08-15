# Productization implementation brief

## Status and assumptions

This brief shapes the first self-hosted team product. It assumes one installation,
multiple authenticated users, multiple approved GitHub repositories, and trusted
installation administrators. A public multi-tenant service is a later product with
a different threat model.

Infrastructure-as-code is outside this plan. Deployable artifacts, process
boundaries, startup contracts, and runtime requirements are inside it.

## Existing foundation

The current code provides useful product foundations:

- Temporal owns durable orchestration and activity retry.
- Postgres-backed contexts own versioned migrations.
- The Hub provides an authenticated web and HTTP surface.
- Hexagonal ports already separate agent, Git, GitHub, storage, notification, and
  orchestration concerns.
- `internal/scoped` provides append-only, inherited configuration with recorded
  provenance.
- Instructions, model selection, and steering already use stored configuration.
- The service has explicit migration and health-check behaviour.

The current runtime also has desktop assumptions that must be removed or bounded:

- `internal/piagent/piagent.go` starts the `pi` executable with inherited process
  environment and normal home-directory discovery.
- `internal/ghcli/ghcli.go` uses the worker account's `gh` configuration.
- `internal/gitcli/gitcli.go` inherits the worker environment for Git operations.
- `config.go` stores templates and worktrees under the operating-system user's
  configuration directory.
- repository places and scoped values use absolute local paths as identity.
- `serve.go` assumes the API server can inspect the same filesystem the worker
  will use.
- all mutating activities use one task queue and carry local paths, so another
  worker can receive a later activity without possessing the workspace state.
- `docker-compose.yml` runs supporting services, but no application or runner
  image exists.
- the repository does not pin a Pi runtime as part of a deployable artifact.
- durable text redaction recognizes common GitHub token forms, but cannot be the
  control for every model, registry, cloud, integration, and plugin credential.

## Governing constraints

Any valid design must satisfy these constraints.

### Separation of responsibilities

The Hub is the control plane. It owns user-facing configuration, authorization,
repository registration, run submission, audit facts, and orchestration.

A runner is the execution plane. It owns workspace allocation, checkout, the Pi
runtime, tool execution, local build commands, and workspace cleanup.

The control plane must not require repository write credentials. The Pi process
must not inherit Hub, Postgres, Temporal, OIDC, or installation administration
credentials.

### Stable identity

A repository is identified by stable source-control facts, not a server path. A
workspace is a disposable execution resource identified independently from its
current path or runner host.

Path-based places may remain as local runtime facts and historical display data.
They must not remain the authority for repository authorization, configuration
scope, or cross-process routing.

### Durable orchestration

Raw credentials must never be Temporal workflow inputs, search attributes,
memos, or workflow results. A workflow carries stable credential references and
business identities. An activity resolves a reference immediately before the
credential is required.

Workspace state must not rely on Temporal scheduling the next activity on the same
worker by chance. The first release may constrain deployment to one execution
worker, but the contract and recorded identities must permit a later isolated
runner service.

Mutating work must have recoverable checkpoints. A runner loss must either resume
from an explicit checkpoint or restart the affected stage from a known repository
state. It must not silently continue from a partial local tree.

### Least authority

Each credential is scoped to one purpose, repository or integration, and bounded
lifetime where the upstream supports it. A capability receives only the credentials
it declared and the administrator approved.

An authenticated user identity is not a downstream service credential. Background
work cannot depend on the requesting user's live sign-in session.

### Explainability

Each run records the non-secret facts required to explain it:

- requesting principal;
- mapped GitHub identity when a change is attributed to a user;
- repository identity and revision;
- execution profile and version;
- resolved instruction, setting, model, and capability versions;
- credential-reference identifiers and credential type, never credential value;
- workspace and runner identity;
- produced commit SHAs and pull request;
- GitHub App installation identity used for remote actions.

### Compatibility

The migration-first startup contract remains. Workflow changes must preserve
Temporal replay compatibility. Runner, Pi, capability, database schema, and API
versions must be visible and checked before work is accepted.

## Contexts and seams

### Installation

Owns deployment-wide policy and defaults. Examples include allowed model routes,
approved capability sources, credential-provider selection, maximum concurrency,
and default execution profiles.

### Principal mapping

Maps an authenticated Hub principal to stable downstream identity facts. A GitHub
mapping must prefer the immutable GitHub user ID over the mutable login. Display
name and commit email are attributes of that verified mapping, not arbitrary run
input.

### Repository catalogue

Owns approved remote repositories, source-control installation association,
default branch, enabled profiles, and repository-level policy. Registration is an
authorization act, not a filesystem probe.

### Workspace execution

Owns workspace allocation, checkout, leases, isolation, checkpointing, cleanup,
and the mapping from a stable workspace ID to a temporary execution location.
Workflows and application cores consume a workspace reference through a port;
they do not construct runner paths.

### Credential resolution

Owns credential metadata, references, authorization, acquisition, refresh,
short-lived token minting, and revocation. The product may use an external secret
system, an internal encrypted store, or both. The domain contract is the same:
consumers request a capability for a bounded operation and receive no durable raw
secret.

### Capability catalogue

Owns approved capability bundles, versions, configuration schemas, required
credentials, runtime requirements, and policy metadata. Pi packages, skills,
extensions, MCP servers, and custom model providers are implementation forms, not
separate product configuration systems.

## Repository and workspace model

A repository registration must include enough stable information to select a
remote repository and authorize access. The exact storage schema remains a design
choice. A valid implementation can use GitHub installation and repository IDs,
canonical remote coordinates, or another stable compound identity.

A workspace must be disposable by default. It must have:

- one owning run or explicitly serialized execution chain;
- a clean source revision;
- bounded storage and lifetime;
- a controlled home and temporary directory;
- isolated Pi sessions and package-manager state;
- cleanup after success, failure, cancellation, and timeout;
- orphan detection after runner failure.

A linked worktree can remain an optimization inside one trusted runner. It cannot
be the cross-run or cross-host product contract because it shares repository
metadata with its source checkout.

The implementation options for durable workspace state are:

1. A single execution worker with a persistent workspace volume for the first
   release. This is the smallest deployable boundary, but limits availability and
   scale.
2. A runner service that owns workspace leases and receives stage commands. This
   gives a stable execution boundary and supports isolated workers, but adds a
   service protocol and recovery lifecycle.
3. Shared filesystem workspaces across workers. This is operationally direct, but
   needs distributed locking, tenant separation, and cleanup guarantees.
4. Reconstruct each stage from remote Git and explicit checkpoints. This improves
   portability, but every useful intermediate state must be committed or stored.

The first release may select option 1 if it refuses unsafe multiple-worker
configuration. The architecture should move toward option 2 or option 4 rather
than making a shared host path permanent.

## Configuration model

Configuration is split into three classes.

### Deployment configuration

This is required before a process can start:

- Postgres and Temporal connection details;
- public URL and OIDC client configuration;
- credential-provider and encryption configuration;
- runner backend and writable storage roots;
- TLS, proxy, and network settings;
- logs, metrics, and tracing exporters;
- process role and capacity.

It is supplied through environment variables, mounted files, or external secret
references. Startup validates it as one typed contract. A validation command must
report all safe-to-display errors without starting a long-running process.

### Durable product configuration

This is non-secret configuration managed through the Hub:

- registered repositories;
- execution profiles;
- model selection and model allowlists;
- instructions and steering settings;
- approved tools and capabilities;
- filesystem and network policy;
- runner/toolchain profile;
- concurrency and token budgets;
- notifications and schedules.

The existing append-only version and provenance discipline should continue. The
scope chain changes from path identity to stable product identity. A suitable
shape is factory, installation, repository, execution profile, and explicit run
override. Exact layering remains open where profile and repository policy conflict;
the winner must be defined once and exposed to users.

### Secret references

Durable product configuration contains typed references such as a GitHub
installation, model route, package-registry reader, or integration account. It
does not contain raw credentials.

A reference must reveal enough metadata for readiness checks and audit without
allowing secret recovery through ordinary configuration reads.

## Credential model

### Credential classes

| Credential class | Expected owner | Delivery boundary | Required restriction |
|---|---|---|---|
| Postgres, Temporal, OIDC, encryption | deployment | owning service only | never inherited by a runner or Pi |
| GitHub repository access | installation GitHub App | trusted checkout, push, or API operation | short-lived installation token and repository scope |
| Model provider | installation or tenant | model gateway or in-memory model runtime | no model provider key in agent shell state |
| Private package read | installation or repository profile | package proxy or temporary runner package config | read-only, package scope, short lifetime where possible |
| MCP or SaaS integration | installation or user-authorized integration | isolated connector or capability process | declared scopes and no unrelated secret access |
| Webhook destination | installation | notification adapter only | URL and secret absent from logs and runner state |

### Credential acquisition lifecycle

1. Configuration selects a credential reference.
2. Authorization confirms that the run, repository, profile, and capability may
   use the reference.
3. A trusted activity requests a credential immediately before use.
4. The provider returns a short-lived token, request signer, proxy route, or
   temporary file handle.
5. Only the intended trusted operation receives it.
6. The operation records safe audit metadata.
7. The credential is revoked when possible and all temporary material is removed.

Credential values must not be placed in command arguments, repository remotes,
workflow state, prompts, tool output, or structured logs.

### GitHub policy

GitHub access uses an installation workload identity rather than user personal
access tokens. GitHub App installation tokens are minted just in time for checkout,
push, and GitHub API operations.

Git and GitHub attribution is fixed as follows:

```text
Author:     Requesting user, mapped to their GitHub identity
Committer:  Temporal Agents
Pusher:     GitHub App
PR creator: GitHub App
```

Required behaviour:

- The requesting Hub principal must have a verified mapping to a GitHub user before
  an attributed change is published.
- The mapping uses stable GitHub user ID as identity and retains current login for
  display.
- Author name and email come from the verified mapping, not request fields or agent
  output.
- A GitHub `noreply` address tied to the mapped account is used when no verified
  commit email is available.
- Commit creation sets author identity from the mapping and committer identity to a
  fixed Temporal Agents name and email.
- Push authentication uses a short-lived GitHub App installation token.
- Pull requests are created through the GitHub App.
- Audit records retain requester principal, mapped GitHub user ID and login,
  GitHub App installation ID, commit SHAs, and pull request identity.

The existing pull-request port permits a GitHub API adapter to replace ambient
`gh` authentication. Git operations can use a temporary credential helper,
a request-signing proxy, or a trusted adapter. Tokens must not appear in remote
URLs or process arguments.

### Model credentials

Two valid implementation options remain:

1. An installation-managed model gateway gives each run an opaque, short-lived
   route token and retains upstream provider credentials. This gives central model
   policy, budgets, rotation, and usage attribution.
2. A Pi SDK runtime uses an injected in-memory credential store and strips provider
   credentials from every child process environment. This has fewer services, but
   Pi and its shell still share more of the process trust domain.

The model gateway is preferred for a product. The in-memory SDK route is acceptable
for an initial trusted installation if the residual risk is documented and the
credential is not written into the Pi profile or workspace.

### Package registry credentials

Private package installation has three acceptable maturity levels:

1. A temporary package-manager configuration file with a short-lived, read-only
   token in an isolated runner.
2. The same mechanism plus package scope and network egress allowlisting.
3. An authenticated package proxy that injects upstream credentials, leaving no
   registry secret in the runner.

Temporary configuration must be outside the repository, readable only by the
runner user, selected through package-manager-specific configuration, and deleted
at run end. Package publishing and release credentials are not available to the
general agent process.

### Integration and MCP credentials

MCP server configuration is not a credential store. A capability declares its
integration type, required OAuth scopes, configuration schema, secret references,
and allowed network destinations.

Refresh tokens require encryption at rest or an external secret system. Prefer an
out-of-process connector or gateway that receives a short-lived access token and
exposes a narrow tool protocol. An in-process trusted extension is acceptable only
when it is reviewed and intentionally shares the runner trust domain.

## Pi runtime and execution policy

Pi already supports explicit skills, extensions, custom providers, packages, and a
programmatic resource loader. Productization should control these mechanisms rather
than reproduce them as a second agent framework.

Every run must use a generated or selected Pi profile independent of the service
account's ordinary home. The runner must control:

- Pi version;
- configuration and session directories;
- model catalogue and selected model;
- enabled built-in and custom tools;
- approved skills, extensions, prompts, and context sources;
- package and extension versions;
- project trust policy;
- update checks and telemetry policy;
- child-process environment;
- writable paths and network access.

Project-local executable extensions and automatic package installation are disabled
by default. Approved resources are loaded explicitly. Repository context files and
skills may be allowed under a separate policy because they influence the model even
when they do not directly execute as extension code.

Two integration options remain valid:

1. Continue using the Pi CLI with a generated `PI_CODING_AGENT_DIR`, explicit
   resource flags, a stripped environment, and pinned runtime files. This minimizes
   initial change.
2. Add a Node runner based on the Pi SDK or RPC mode. This gives direct in-memory
   credentials, resource loaders, tool replacement, and event handling, but adds a
   maintained process and protocol boundary.

A CLI-based first slice must not make CLI subprocess inheritance part of the final
credential contract. The SDK or RPC option should be selected when tool mediation
or in-memory credentials require it.

## Shell and secret boundary

An unrestricted shell and hidden credentials cannot safely share one sandbox. The
agent can inspect environment variables, files, process information, and network
configuration. Environment filtering and redaction alone do not provide strong
secrecy.

A high-assurance profile therefore:

- keeps broad GitHub and infrastructure credentials outside Pi;
- performs checkout, push, PR creation, and publishing through trusted operations;
- uses a model gateway or narrowly scoped runtime model token;
- uses an auth-injecting package proxy where registry credentials must remain
  secret from the agent;
- restricts filesystem access, process privileges, and network egress;
- runs rootless with no host socket or operator home mount.

A lower-assurance trusted-team profile may expose a temporary read-only package
token inside an isolated runner. The Hub must label that residual capability and
its scope clearly.

## Capability and plugin model

### Product abstraction

The product-level concept is a capability bundle. A bundle can contain or reference
Pi skills, extensions, prompt templates, model providers, MCP connectors, and
runtime tools.

A bundle declares:

- stable ID, version, and immutable source digest;
- included resource types and entry points;
- configuration schema and defaults;
- required credential types and scopes;
- required executable commands and runtime dependencies;
- filesystem and network permissions;
- compatible runner profiles and Pi versions;
- migration and upgrade policy;
- provenance, publisher, and review status.

### Trust levels

- Built-in capabilities can run in the product runner.
- Organization-approved capabilities can run after review, pinning, and policy
  approval.
- Untrusted integrations run out of process behind a narrow protocol or are not
  supported.

Pi extensions execute arbitrary code with runner permissions. Skills can direct the
model to execute arbitrary commands. Both require provenance and review. A skills-
only package has less direct execution authority than an extension package, but it
is not harmless.

### Installation and updates

Production capability code is installed during image or bundle construction, not
by an agent or a running production service. Sources and dependency lockfiles are
pinned and verified. Runtime installation from npm or Git is disabled unless an
explicit administrative workflow implements equivalent review and immutability.

Capability activation is a configuration operation. Code installation and code
activation are separate audit events.

## Deployable artifacts

### Hub image

The Hub artifact contains the Go service, built web application, CA certificates,
and only required runtime files. It runs without compilers or package managers and
uses a non-root user. The web bundle may be embedded in the binary or copied into an
immutable image path.

### Runner image or profiles

A runner artifact pins Temporal Agents, Pi, Node.js, Git, package managers, CA
certificates, and approved built-in capabilities. It contains no operator home,
repository checkout, or deployment credential.

Toolchain support should use several bounded runner profiles rather than one
universal image. Initial profiles may include a base coding runner and the
languages required by the first supported repositories.

### Migration command

The existing explicit `temporal-agents migrate` contract remains the deployment
migration step. Long-running services verify schema and do not silently migrate in
production.

### Evaluation stack

The existing local supporting-service stack can become a complete evaluation stack
by adding product artifacts and safe development credentials. It does not define
the production security model.

## Operational requirements

### Startup and readiness

A process reports its role, build version, Pi version where relevant, schema
requirements, runner profile, capability catalogue generation, and safe dependency
status. It fails before accepting work when required configuration or compatibility
is invalid.

Readiness covers dependencies required to accept new work. Liveness does not fail
only because an optional external integration is unavailable.

### Logs, metrics, and tracing

Structured events correlate installation, principal, repository, workflow, run,
workspace, runner, profile, and capability IDs. No raw prompt or command output is
required in general operational logs.

Minimum metrics include queue wait, run and stage duration, outcomes, retry counts,
token use, model route, workspace capacity, cleanup failures, credential acquisition
failures, and runner availability.

Trace propagation must not place secrets in baggage or attributes.

### Audit

Audit records cover repository registration, principal mapping, profile and
capability changes, credential-reference use, work submission, steering decisions,
change publication, and administrative installation or activation of capability
code.

Audit is append-only from ordinary product surfaces. Retention and export policy
remain an installation decision.

### Cleanup and recovery

Cancellation and runner shutdown trigger bounded cleanup. A periodic reconciler
finds expired workspace leases, temporary credential files, incomplete clones,
and orphaned sessions. Cleanup failure is visible and retryable.

A workspace must not be reused for another principal or repository until cleanup
has been verified.

### Upgrade safety

Images and capability bundles are immutable and versioned. Rollback requirements
must account for database migrations and in-flight Temporal workflow histories.
Pi and capability compatibility is tested as part of the release artifact, not
assumed from the host.

## Options and trade-offs

| Decision | Smaller first step | Stronger target | Consequence |
|---|---|---|---|
| Runner topology | one execution worker and persistent volume | isolated runner service | first step must refuse unsafe replicas |
| Pi integration | controlled CLI subprocess | SDK or RPC runner | SDK/RPC gives stronger resource and credential control |
| Model auth | in-memory per-run credential | model gateway | gateway adds service cost but removes upstream keys from runners |
| Package auth | temporary read-only config | auth-injecting proxy | temporary token remains readable inside the runner |
| Plugin execution | approved in-process extensions | out-of-process connectors | process boundary costs more but limits authority |
| Workspace recovery | restart stage from Git checkpoint | durable workspace lease and restore | checkpoint design limits uncommitted recovery |
| GitHub implementation | controlled `gh`/Git subprocesses | native API and credential-aware adapters | adapters remove ambient CLI state and improve audit |

## Risks and open decisions

- The exact repository-to-configuration scope precedence must be defined before
  migrating path-scoped overrides.
- Existing stored path-scoped values need an explicit migration, compatibility, or
  deprecation policy.
- Temporal activity boundaries may need reshaping so a mutating stage remains on one
  leased workspace without relying on worker affinity.
- Pi session retry semantics must remain correct when sessions move from a user home
  to disposable or durable workspace storage.
- Build commands can download and execute untrusted code. Container isolation alone
  does not control network exfiltration or supply-chain behaviour.
- User-to-GitHub mapping must define what happens when an account is renamed,
  removed, or lacks a usable commit email.
- GitHub App permissions must be minimized while still supporting checkout, push,
  pull requests, review threads, and requested reviewers.
- Private Git dependencies need a policy separate from npm-style package registries.
- An internal encrypted credential store creates a master-key and rotation
  responsibility; external secret systems create availability and portability
  dependencies.
- Capability configuration evolution needs schema migration and rollback rules.
- Runner profiles must state which languages and package managers are product-
  supported rather than merely present.
- Cost and concurrency quotas require policy for scheduled work and fleet fan-out.

## Delivery outlook

These are planning ranges, not commitments:

| Target | Approximate effort |
|---|---:|
| Containerized personal appliance with explicit credential mounts | 3–6 engineer-weeks |
| Team beta with GitHub App, controlled Pi profile, and disposable workspaces | 16–24 engineer-weeks |
| Team-ready product with credential mediation, private registries, approved capabilities, audit, and isolation | 30–50 engineer-weeks |
| Hostile-tenant SaaS boundary | an additional 4–8 months plus dedicated security review |

Workspace identity and runner isolation are expected to cost more than the
capability catalogue.

## Slice traceability

1. `01-deploy-clean-installation.md` establishes immutable artifacts, startup
   contracts, and independence from an operator home.
2. `02-run-registered-github-repository.md` establishes stable repository identity,
   GitHub App checkout, disposable workspace, and secret references.
3. `03-use-controlled-execution-profile.md` establishes controlled Pi resources and
   model access.
4. `04-publish-attributed-change.md` establishes the fixed author, committer,
   pusher, and pull-request creator policy.
5. `05-install-private-dependencies.md` establishes narrowly scoped package access.
6. `06-activate-approved-capability.md` establishes capability packaging,
   configuration, credential declaration, and MCP integration boundaries.
7. `07-isolate-and-recover-concurrent-runs.md` removes unsafe workspace affinity and
   demonstrates isolation and recovery.
8. `08-operate-and-audit-the-product.md` closes diagnostics, audit, rotation,
   cleanup, and upgrade readiness.
