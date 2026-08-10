# Slice 1 — Explicit schema migration

**Discharges:** IB §5 (schema ownership, per-context migrations, no cross-context
foreign keys).

**Demo:** `temporal-agents migrate` applies every context's schema and prints what
it applied. Starting `worker` or `serve` against an older schema fails immediately
with a message naming the missing version and the command to run.

## Tasks

- [x] Add a `migrate` command that applies each context's migrations and reports
      the resulting version per context.
- [x] Add a startup **verification** step to `worker` and `serve`: required version
      or higher, else fail fast with a remedy in the message. No DDL at startup
      outside an explicit, documented development mode.
- [x] Keep each context's migrations in that context's adapter package; do not
      merge them into one shared set.
- [x] Document the operational order (migrate, then start) in the README.
- [x] Integration tests with containers: fresh database migrates; a stale database
      is refused by both processes; migrating twice is a no-op; two concurrent
      `migrate` invocations do not corrupt state.

## Done when

Schema application is a deliberate step, both processes fail fast on a stale
schema, and no context reaches into another's migrations.
