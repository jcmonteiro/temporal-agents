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
- Capture per-node terminal status. **Skipped** nodes (a node whose dependency
  did not succeed) never start a `DevelopWorkflow` — `FleetWorkflow` records a
  `NodeResult{Status: StatusSkipped}` in its own `results` map
  (`internal/fleet/workflow.go:150-157`) and starts no child — so there is no
  child `run_id` to self-record. Skipped outcomes therefore live in the parent
  `Fleet` row's `jsonb` `detail` (the per-node breakdown, per Q4), and `history`
  expands them from there rather than expecting a child row.
- The `Fleet` row records only its own incremental tokens (typically none of its
  own agent work beyond planning aggregation); per-node develop/review tokens
  live on the node rows, so summing all rows avoids double-counting (see token
  accounting in the implementation brief).
- Recording is a hard dependency (idempotent upsert on `run_id`, must succeed) —
  a `Persist…` failure fails the workflow; Temporal retries absorb transient
  outages.

## Demo (done state)

Run a `fleet execute`; `temporal-agents history` shows the parent `Fleet` row
(with skipped nodes expanded from its `detail`) and a row per node that actually
ran, each with its status and token usage, all attributable to their parent
fleet run.

## Depends on

Slice 1 (foundation); slice 2 (node develop executions reuse the `code`
develop recording).

## Testing

Temporal Go test suite: assert `FleetWorkflow` invokes `PersistFleetWorkflowState`
and that node/review children link via `parent_workflow_id`; verify a skipped
node appears in the `Fleet` row's `detail` (no child row expected). Tree
reconstruction from `parent_workflow_id` via unit tests.
