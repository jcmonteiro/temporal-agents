# Slice 08 — Operate and audit the product

## Outcome

An operator can validate, monitor, rotate, diagnose, upgrade, and audit a production
installation without inspecting service-account home directories or exposing
credentials.

## Demonstration

1. Validate a complete installation configuration before startup.
2. Inspect readiness for Hub, orchestration, stores, runners, model route, GitHub App,
   package registry, and activated capabilities.
3. Run and publish one change, then trace it from requester through workflow,
   workspace, model route, commit, and pull request.
4. Rotate a GitHub, model, package, or integration credential reference and run again.
5. Trigger a cleanup and external-integration failure and inspect actionable,
   secret-safe diagnostics.
6. Upgrade the product and one capability while an allowed long-running workflow
   exists, then verify workflow and schema compatibility.
7. Export or inspect the append-only administrative and execution audit trail.

## Delivers

- Complete startup validation and safe diagnostic output.
- Role-specific liveness and readiness semantics.
- Structured correlation across installation, principal, repository, workflow, run,
  workspace, runner, profile, capability, and credential-reference IDs.
- Metrics for queue wait, duration, outcomes, retries, token use, model routes,
  capacity, cleanup, credential acquisition, and runner health.
- Trace propagation without secret-bearing attributes or baggage.
- Append-only audit for repository registration, principal mapping, profile changes,
  capability installation/activation, credential-reference use, work submission,
  steering, and publication.
- Credential rotation and revocation procedures.
- Workspace and temporary-secret reconciliation dashboards or diagnostics.
- Release compatibility checks for database schema, Temporal histories, Pi runtime,
  runner profiles, and capability bundles.
- Documented backup, restore, rollback, and data-retention responsibilities.

## Constraints discharged

- Operators can diagnose the product through supported surfaces.
- Raw prompts and command output are not required in general operational logs.
- Redaction remains defence in depth rather than the primary secret boundary.
- Upgrade refusal happens before incompatible work is accepted.
- Every externally visible change is attributable to requester and workload identity.

## Not included

- Infrastructure provisioning automation.
- A universal observability vendor requirement.
- Public SaaS compliance certification.
- Automated recovery from every external provider outage.

## Done when

- A production-readiness runbook can perform the demonstration without direct
  database edits or service-account shell access.
- Credential rotation does not require workflow definition or repository changes.
- Failure diagnostics identify corrective action and pass secret-leak tests.
- Audit queries reconstruct one change's complete identity and configuration chain.
- Release tests replay representative in-flight workflow histories and verify
  migration, forward startup, and supported rollback behaviour.
- Cleanup and credential-acquisition alerts identify the affected safe IDs without
  exposing credential values.
