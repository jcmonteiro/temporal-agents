# Slice 01 — Deploy a clean installation

## Outcome

An administrator can start Temporal Agents from versioned runtime artifacts in a
clean environment. The installation does not depend on an operator's Pi, GitHub,
Git, Node, package-manager, or home-directory configuration.

## Demonstration

1. Start the supporting services and product artifacts on a machine with no prepared
   Temporal Agents service account.
2. Run the explicit migration command.
3. Open the Hub and sign in.
4. Start one non-publishing evaluation run against an explicitly supplied evaluation
   repository.
5. Inspect the run result and the runtime versions that produced it.
6. Restart the Hub and execution worker and confirm that durable state remains.

## Delivers

- Immutable Hub and runner artifacts with pinned required runtime dependencies.
- The built web application in the Hub artifact.
- A non-root runtime with explicit writable directories.
- One typed startup configuration contract and a validation command.
- Migration-before-start behaviour in the packaged runtime.
- Build, Pi, schema, and runner-profile version reporting.
- Hub and worker health/readiness behaviour suitable for deployment.
- A complete evaluation deployment path, without claiming production credential
  isolation yet.
- A hard deployment limit of one mutating execution worker until slice 07 removes
  unsafe workspace affinity.

## Constraints discharged

- Independence from ambient operator installation state.
- Deployable artifacts are in scope even though infrastructure-as-code is not.
- Runtime and schema incompatibility is visible before work starts.
- The web application and service versions ship together deliberately.

## Not included

- Remote repository registration.
- GitHub App access.
- Strong model or package credential mediation.
- General runner scaling.
- Third-party capability installation.

## Done when

- The documented deployment uses only versioned product artifacts and explicit
  configuration.
- Removing or changing the host user's Pi configuration cannot change the run.
- The runner contains no copied operator credential files.
- Unsupported extra worker replicas are refused or clearly prevented.
- Startup diagnostics omit secret values.
- A release test builds the artifacts, migrates a fresh database, starts the
  services, completes the evaluation run, and verifies restart.
