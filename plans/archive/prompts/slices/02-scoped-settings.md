# Slice 2 — Scoped settings (the same chain for non-text values)

**Discharges:** IB §3 (one scope-chain mechanism serves settings too), preventing a
duplicate mechanism in the steering feature.

**Demo:** one real setting (a boolean) resolves through the place chain: unset
everywhere it reads the shipped default; set globally it applies everywhere; set on
a repository it applies to that repository and its worktrees; set on a worktree it
applies only there.

## Tasks

- [x] Generalise the resolution machinery so a typed, non-text value uses the same
      chain, the same per-key rule, and the same append-only storage discipline.
- [x] Define the first real setting and its shipped default, chosen so the steering
      feature can consume it unchanged.
- [x] Add a read of the effective value plus its source scope, through the client
      boundary, so a later interface can show "inherited from X".
- [x] Unit tests: precedence, per-key independence, typed parse failures refused at
      save time, unset chain falls back to shipped default.
- [x] Integration test with containers: values at three levels resolve correctly for
      a worktree.

## Done when

A non-text setting obeys exactly the same inheritance as an instruction, with its
source scope reportable, and no parallel mechanism exists.

## Delivered

The mechanism was **extracted rather than copied**: `internal/scoped` now owns the
chain (`directory -> repository -> global -> factory`), the append-only versioned
store with one pointer per (key, scope), the ports, and the "first scope wins" rule.
Two catalogues sit on it and share one key space — `internal/instruction` (text, with
inserts and the system's own block) and `internal/setting` (typed) — so a setting and
an instruction cannot start disagreeing about which place covers which. One Postgres
adapter, `scoped/scopedpg`, serves both.

The first real setting is `steering.enabled`, shipped **off**, which is what the
steering feature consumes unchanged. A stored value that cannot be read back as the
type its key means fails the read instead of quietly becoming the default, and the
same check is what a save runs.

`GET /api/v1/settings` publishes each setting's effective value with the *kind* of
scope it came from (`directory | global | factory`) and the version that answered, so
an interface can say "inherited from …" without deriving inheritance. It is the
installation-wide read: resolving for one named place needs a way to address a place
over the API, which the configuration surface (slice 4) introduces together with the
writes.
