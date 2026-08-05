# Slice 2 — Record `code` executions

**Goal:** extend the recording path (established in slice 1) to the `code`
workflows via `PersistDevelopWorkflowState`, `PersistReviewWorkflowState`, and
`PersistPilotWorkflowState`, each called from inside its workflow.

Open-pr is **not** a separate type: it runs only inside the `--with-remote`
develop pipeline, so its outcome (the PR URL from `OpenPRResult`) is folded into
the `Develop` record as a field. The standalone `open-pr-` execution is not
recorded on its own.

## Tasks

- Add and wire `PersistDevelopWorkflowState`, `PersistReviewWorkflowState`, and
  `PersistPilotWorkflowState`, calling each from inside its workflow to record
  start and terminal state, reusing the pattern from slice 1.
- Capture the outcome detail these workflows already produce (review
  convergence, the PR URL from `OpenPRResult` as a field on the Develop record)
  without leaking adapter types into the core. Record each workflow's **own
  incremental** tokens only, not inclusive `TokensSoFar` (see token accounting in
  the implementation brief), so the develop and its child review rows don't
  double-count.
- Ensure the workflow-ID classification already used by `list`/`watch`
  (`develop-`, `review-`, `pilot-`) maps to the recorded command kind
  consistently.
- Recording is a hard dependency (idempotent upsert on `run_id`, must succeed) —
  a `Persist…` failure fails the `code` workflow; Temporal retries absorb
  transient outages.

## Demo (done state)

Run `temporal-agents code develop "…"` (and the other subcommands); each appears
in `temporal-agents history` with the correct kind, status, and token usage, and
a `--with-remote` develop record surfaces the PR URL.

## Depends on

Slice 1 (port, adapter, `history`, recording pattern).

## Testing

Temporal Go test suite: assert each `code` workflow invokes its `Persist…`
activity at start/terminal with the right record (incl. PR URL / convergence in
`detail`) and fails on a `Persist…` failure. Domain formatting via unit tests.
