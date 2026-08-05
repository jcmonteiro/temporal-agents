# Slice 4 — Record schedule linkage on fired runs

**Goal:** attribute schedule-fired executions to the schedule that produced them.
Schedule is **not** a distinct persistence type — a fired run is a `Run` record
(recorded via `PersistRunWorkflowState`, landed in slice 1) carrying a nullable
schedule-ID field.

## Tasks

- Give `PromptWorkflow` the information needed to know it was schedule-fired and
  which schedule fired it (e.g. a field on `PromptRequest` that `startSchedule`
  sets and `startRun` leaves empty), so `PersistRunWorkflowState` can populate
  the schedule-ID field.
- Populate the nullable schedule-ID column (added in slice 1) on fired runs.
- Handle the overlap-skip policy: a run that never fires (previous one still
  going) creates no record, so history shows no misleading skipped entry.
- The schedule itself is **not** a persisted row: it has no workflow, and all
  writes happen inside workflows. The schedule definition stays in Temporal
  (shown by `list`); fired runs are grouped via the `schedule_id` tag.
- Recording is a hard dependency (idempotent upsert on `run_id`, must succeed) —
  a `Persist…` failure fails the workflow; Temporal retries absorb transient
  outages.

## Demo (done state)

Create a short-interval schedule; after a few fires, `temporal-agents history`
shows each fired execution as a `Run` attributable to its schedule (filterable
by schedule ID), with status and token usage.

## Depends on

Slice 1 (`Run` recording and the schedule-ID column).

## Testing

Temporal Go test suite: assert a schedule-fired `PromptWorkflow` records a `Run`
row carrying the `schedule_id`, and a plain `run` records none. Overlap-skip
produces no row.
