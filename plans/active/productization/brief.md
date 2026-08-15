# Productization brief

## Problem

Temporal Agents was built for one person to run on their own machine. It uses
that machine's repositories, credentials, tools, package-manager setup, and Pi
configuration. This makes local use direct, but it prevents a team from deploying
one controlled installation with predictable behaviour.

The affected users are:

- Installation administrators, who need to deploy, configure, upgrade, and
  diagnose the product without preparing a personal shell account.
- Repository administrators, who need to decide which repositories the product
  may access and what it may do in each one.
- Requesting users, who need work to run under a clear identity and produce
  attributable changes.
- Security operators, who need credentials, repository data, and generated output
  to remain within explicit boundaries.
- Capability authors, who need a supported way to add approved tools,
  instructions, models, and integrations without changing the product core.

## Desired outcome

A team can deploy one self-contained Temporal Agents installation, connect
approved GitHub repositories, select controlled execution profiles, and run work
without depending on any operator's home directory or ambient credentials.

Configuration is explicit, inspectable, versioned, and inherited through stable
product scopes. Credentials are independently managed and narrowly authorized.
Every run has an attributable requester, configuration provenance, repository,
and result.

Approved capabilities can be added and configured without giving arbitrary
third-party code implicit access to every installation credential.

## Success signals

The outcome is present when:

- A new installation starts in a clean environment with no prepared user home
  directory.
- An administrator can connect and select an approved GitHub repository without
  registering a host filesystem path.
- A requester can start work in that repository and inspect its progress and
  result through the Hub.
- A produced commit and pull request identify the requester, Temporal Agents, and
  the remote actor according to the installation's attribution policy.
- A run uses only the execution profile and capabilities selected for its scope;
  unrelated host configuration does not change its behaviour.
- A private dependency can be installed with narrowly authorized access and no
  long-lived credential in the repository or execution record.
- A credential can be rotated or revoked without changing a workflow definition
  or a repository checkout.
- Workflow history, durable run records, notifications, and normal logs contain
  credential references and audit facts, not raw credentials.
- Two concurrent runs cannot read or modify each other's workspace, sessions, or
  credentials.
- An operator can determine why a run behaved as it did from recorded requester,
  repository, profile, capability, and configuration versions.
- Upgrades retain durable workflow compatibility and refuse incompatible runtime
  or schema combinations before accepting work.

## Product boundary

The first product target is a self-hosted team installation:

- One installation can serve multiple authenticated users and multiple approved
  GitHub repositories.
- Repositories and installed capabilities are trusted according to explicit
  installation policy.
- Background and scheduled work uses installation workload identity and does not
  depend on a user's active browser session.
- The first supported source-control product is GitHub.
- The first supported deployment can limit execution capacity while preserving a
  path to isolated concurrent runners.

## In scope

- Deployable application and runner artifacts.
- Explicit startup validation and operational diagnostics.
- Stable repository identity independent of local paths.
- Controlled, disposable workspaces.
- GitHub repository access, change publication, and attribution.
- Controlled Pi models, instructions, skills, extensions, and tools.
- Model-provider, source-control, package-registry, and integration credentials.
- Read access to private package registries.
- Approved capability packaging and configuration.
- Audit, configuration provenance, cleanup, and recovery requirements.
- A safe path from one runner to concurrent isolated runners.

## Out of scope

- Infrastructure-as-code for any deployment platform.
- A hostile-tenant, public multi-tenant SaaS service.
- Support for every source-control provider.
- Package publication or release signing by the general coding agent.
- Unreviewed third-party executable plugins installed directly in a running
  production installation.
- A universal runner image containing every language and toolchain.
- Full prevention of data exfiltration while an unrestricted shell, unrestricted
  network access, and valuable credentials share one trust domain.
- Replacing Temporal as the durable orchestration system.
- Replacing the existing Hub authentication model unless a product requirement
  makes that necessary.

## Non-goals

- Reproducing a user's complete personal Pi setup on a server.
- Treating a plugin configuration file as a credential store.
- Treating log redaction as the primary control for secret protection.
- Making server filesystem paths stable product identifiers.
