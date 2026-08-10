# Slice 1 — Stored instructions with provenance

**Discharges:** IB §1 (resolution model, append-only versions, recorded
provenance), IB §2 (shipped defaults published at startup), IB §4.

**Demo:** with nothing configured, every workflow behaves exactly as before, and
`history` (and the run's detail read) reports which instruction key, scope,
version, and content hash each run used. Deleting nothing and changing nothing
still runs.

## Tasks

- [ ] Model the instruction catalogue in the core: keys, required inserts per key,
      and which part of each key's text is the system's own block.
- [ ] Add the storage port and its adapter (own migrations): append-only versions,
      plus a pointer per (key, scope). Idempotent publication of shipped defaults at
      startup, safe under concurrency.
- [ ] Implement scope-chain resolution in the core, per key, with the factory value
      as the final fallback; unit-test the chain exhaustively, including a gap in
      the middle of the chain.
- [ ] Add a resolve-at-start activity, and carry the resolved text plus provenance
      through workflow inputs, including across continue-as-new. No workflow reads
      storage.
- [ ] Record provenance on the execution record; keep each row reporting only its
      own facts. Recover text via the version record, never by copying it per run.
- [ ] Surface provenance in `history` output and in the run detail read.
- [ ] Fail the unit of work visibly if resolution cannot complete; never substitute
      a default silently.
- [ ] Replay tests: recorded histories still replay; add one covering the new
      activity.
- [ ] Integration tests with containers: publication is idempotent; a version is
      never mutated; a run's provenance still resolves after a later edit.

## Done when

Instructions live in storage, resolution happens once per unit of work, every run
explains which instruction it used, and unconfigured behaviour is byte-for-byte the
old behaviour.
