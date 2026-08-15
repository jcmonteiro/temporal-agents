# Slice 05 — Install private dependencies

## Outcome

A repository run can install an approved private dependency with narrowly scoped,
read-only access. No long-lived package credential enters the repository or durable
execution state.

## Demonstration

1. Configure a private registry reader as a credential reference.
2. Approve that reference for one repository execution profile.
3. Start a run that installs and tests a private dependency.
4. Confirm that installation succeeds only for the approved registry and package
   scope.
5. Confirm that the temporary package configuration is outside the repository and is
   removed after the run.
6. Revoke or rotate the registry credential and run again without changing the
   repository or workflow definition.

## Delivers

- A typed private-package credential reference and readiness check.
- Package-manager-specific temporary configuration or an authenticated package
  proxy.
- Read-only package scope and bounded credential lifetime where supported.
- Network policy for approved registry destinations at the selected assurance level.
- Cleanup of temporary package configuration on every terminal path.
- Audit facts for credential-reference use, registry, repository, profile, and run.
- An explicit policy for private Git dependencies if they are supported by the first
  runner profile.

## Constraints discharged

- Package credentials are separate from capability and Pi configuration.
- Package publication credentials are unavailable to the general agent shell.
- Repository files and commits cannot contain generated package authentication
  configuration.
- Rotation does not require workflow or repository changes.

## Security note

A temporary token inside the runner is readable by an unrestricted agent shell. That
is an accepted residual risk only for a short-lived, read-only, narrowly scoped token
in a trusted-team profile. A profile that must hide the credential from the agent
uses an auth-injecting proxy.

## Not included

- Publishing packages.
- General cloud deployment credentials.
- Unrestricted access to every organization registry.
- A claim that environment filtering hides a credential from an unrestricted shell.

## Done when

- The approved private dependency installs in the supported runner profile.
- An unapproved repository, registry, or package scope is refused.
- A scan of workspace files, Git state, Temporal history, durable records, logs, and
  notifications finds no package token.
- Temporary files have restrictive permissions and are removed on success, failure,
  cancellation, timeout, and runner restart reconciliation.
- The selected assurance level is visible in profile configuration.
