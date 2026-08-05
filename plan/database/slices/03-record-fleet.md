# Slice 3 — Record `fleet` executions (parent + nodes)

**Goal:** record fleet orchestration, preserving the parent/child relationship
between the fleet parent execution and its per-node develop executions.

## Tasks

- Record the `fleet execute` parent execution (`PersistFleetWorkflowState`) and
  rely on each node's `DevelopWorkflow` self-recording its own `Develop` row
  (slice 2). Node and review children link to their parents via the
  `parent_workflow_id` column from slice 1.
- History reconstructs the fleet→node tree from `parent_workflow_id` rather than
  from ID-prefix parsing; the existing prefix disambiguation still labels rows.
- Capture per-node terminal status including **skipped** nodes (a node whose
  dependency did not succeed). The `Fleet` row records only its own incremental
  tokens (typically none of its own agent work beyond planning aggregation);
  per-node develop/review tokens live on the node rows, so summing all rows
  avoids double-counting (see token accounting in the implementation brief).
- Recording is a hard dependency (idempotent upsert on `run_id`, must succeed) —
  a `Persist…` failure fails the workflow; Temporal retries absorb transient
  outages.

## Demo (done state)

Run a `fleet execute`; `temporal-agents history` shows the parent and each node
with its status (including skipped nodes) and token usage, and the nodes are
attributable to their parent fleet run.

## Depends on

Slice 1 (foundation); slice 2 (node develop executions reuse the `code`
develop recording).

## Testing

Temporal Go test suite: assert `FleetWorkflow` invokes `PersistFleetWorkflowState`
and that node/review children link via `parent_workflow_id`; verify skipped-node
status is recorded. Tree reconstruction from `parent_workflow_id` via unit tests.
