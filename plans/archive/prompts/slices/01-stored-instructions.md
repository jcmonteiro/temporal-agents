# Slice 1 — Stored instructions with provenance

**Discharges:** IB §1 (resolution model, append-only versions, recorded
provenance), IB §2 (shipped defaults published at startup), IB §4.

**Demo:** with nothing configured, every workflow behaves exactly as before, and
`history` (and the run's detail read) reports which instruction key, scope,
version, and content hash each run used. Deleting nothing and changing nothing
still runs.

## Tasks

- [x] Model the instruction catalogue in the core: keys, required inserts per key,
      and which part of each key's text is the system's own block.
- [x] Add the storage port and its adapter (own migrations): append-only versions,
      plus a pointer per (key, scope). Idempotent publication of shipped defaults at
      startup, safe under concurrency.
- [x] Implement scope-chain resolution in the core, per key, with the factory value
      as the final fallback; unit-test the chain exhaustively, including a gap in
      the middle of the chain.
- [x] Add a resolve-at-start activity, and carry the resolved text plus provenance
      through workflow inputs, including across continue-as-new. No workflow reads
      storage.
- [x] Record provenance on the execution record; keep each row reporting only its
      own facts. Recover text via the version record, never by copying it per run.
- [x] Surface provenance in `history` output and in the run detail read.
- [x] Fail the unit of work visibly if resolution cannot complete; never substitute
      a default silently.
- [x] Replay tests: recorded histories still replay; add one covering the new
      activity.
- [x] Integration tests with containers: publication is idempotent; a version is
      never mutated; a run's provenance still resolves after a later edit.

## Done when

Instructions live in storage, resolution happens once per unit of work, every run
explains which instruction it used, and unconfigured behaviour is byte-for-byte the
old behaviour.

## Delivered

`internal/instruction` is the context: the catalogue (`review.perform`,
`review.implement`, `pilot.address`), the scope chain
(`directory -> repository -> global -> factory`, per key), the resolve activity, and
the storage port. `instructionpg` owns its migrations, keeps versions append-only,
and publishes the shipped defaults under a per-key lock at worker startup.
`wfinstruction` carries the version gate and the resolve-once rule; the review and
pilot loops resolve at their first pass and carry the result across
continue-as-new. Provenance lands on the execution record as values
(`execstore.InstructionUse`) and is printed by `history`.

Two decisions worth carrying forward:

- **The keys governed are the review loop's and the pilot's.** The develop, summarize
  and fleet-planning instructions stay compiled in until something asks for them; a
  catalogue entry nobody resolves is weight, and the mechanism is proved by the loops
  that steering depends on.
- **`history` is the run detail read.** The hub's run resource represents a
  `PromptWorkflow` chain, whose instruction is the operator's own text, and it does
  not represent review passes at all — so there was nothing there to surface. The
  configuration surface (slice 4) is where provenance reaches the hub.
