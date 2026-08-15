# Slice 03 — Use a controlled execution profile

## Outcome

An administrator can assign a versioned execution profile to a repository. Every
run uses the selected model, instructions, tools, and approved Pi resources without
loading unrelated service-account or project executable configuration.

## Demonstration

1. Create or select an execution profile with a model route, instruction settings,
   tool policy, and an approved skills-only capability.
2. Assign it to a registered repository.
3. Place conflicting Pi settings and an unapproved executable project extension in
   the repository or host environment.
4. Start a run.
5. Confirm that the selected profile controls the run and the unapproved extension
   does not load.
6. Inspect the resolved profile and resource versions in the run record.
7. Rotate the model credential or gateway route without editing the workflow or
   repository.

## Delivers

- Stable execution-profile identity and append-only versions.
- Defined precedence among factory, installation, repository, profile, and explicit
  run settings.
- A controlled Pi configuration and session directory per run or workspace.
- Pinned Pi runtime and explicit model/resource selection.
- Explicit project trust policy with project-local executable extensions disabled
  by default.
- An allowlist for built-in and custom tools.
- A model credential reference resolved through either a model gateway or an
  in-memory runtime boundary.
- A stripped child-process environment that excludes control-plane credentials.
- Recorded provenance for model, instruction, setting, skill, and profile versions.

## Constraints discharged

- A worker account's ordinary Pi home cannot influence production behaviour.
- Model-provider keys are not stored in repository files, Pi profiles, workflow
  input, or durable output.
- Approved resource activation is separate from resource installation.
- Configuration is inspectable and versioned.

## Not included

- General executable capability bundles.
- MCP integrations.
- Private package registry access.
- Full network egress policy.

## Done when

- The same repository and profile produce the same resolved runtime configuration on
  two clean runner instances.
- Unapproved host and project extensions are demonstrably ignored.
- A missing model route, unavailable credential reference, or incompatible Pi/profile
  version fails readiness or run preflight with corrective detail.
- The Pi process and its child shell do not receive Hub, Postgres, Temporal, OIDC, or
  GitHub App credentials.
- The run record identifies every resolved configuration version without copying
  secret material.
