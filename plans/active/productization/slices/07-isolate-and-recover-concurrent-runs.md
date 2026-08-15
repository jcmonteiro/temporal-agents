# Slice 07 — Isolate and recover concurrent runs

## Outcome

The installation can execute concurrent repository work without shared mutable
workspace state, and it can recover deterministically when a runner disappears.

## Demonstration

1. Start two runs for different principals or repositories at the same time.
2. Show that each receives a separate workspace, Pi session area, temporary home,
   credentials, and process boundary.
3. Attempt cross-workspace file and credential access and confirm refusal.
4. Terminate one runner during a mutating stage.
5. Observe the workflow resume from an explicit checkpoint or restart the stage from
   a known repository state.
6. Confirm that the other run is unaffected and that orphan cleanup removes the lost
   runner's temporary resources.

## Delivers

- A runner scheduling and workspace lease contract that does not depend on Temporal
  worker affinity.
- Per-run filesystem, process, session, and credential isolation.
- Bounded CPU, memory, storage, process, and execution-time policy.
- Explicit workspace checkpoint and recovery semantics.
- Runner heartbeats, capacity, lease expiry, and orphan reconciliation.
- Safe concurrency controls for the same repository and branch.
- Network isolation or egress policy appropriate to the supported profile.
- Removal of the one-mutating-worker deployment restriction from slice 01.

## Constraints discharged

- A later activity cannot accidentally operate on another worker's unrelated path.
- A workspace is never silently reused across principals or repositories.
- Runner loss cannot turn partial local state into an unexplained success.
- Concurrent fleet nodes preserve independent branch and workspace state.
- Cleanup failures are visible and retryable.

## Not included

- Hostile public multi-tenant operation without further security analysis.
- Live migration of an arbitrary in-memory agent process.
- Unlimited concurrency.
- A universal runner toolchain.

## Done when

- Concurrency tests prove workspace, session, and credential separation.
- A forced runner loss has one documented, deterministic recovery outcome.
- Same-repository conflict policy prevents unsafe concurrent publication.
- Capacity exhaustion produces queueing or refusal rather than unsafe sharing.
- Expired leases and orphaned temporary material are reconciled after process
  restart.
- Multiple execution workers can be enabled without a shared operator home or
  accidental local-path affinity.
