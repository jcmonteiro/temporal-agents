# Slice 2 — Scoped settings (the same chain for non-text values)

**Discharges:** IB §3 (one scope-chain mechanism serves settings too), preventing a
duplicate mechanism in the steering feature.

**Demo:** one real setting (a boolean) resolves through the place chain: unset
everywhere it reads the shipped default; set globally it applies everywhere; set on
a repository it applies to that repository and its worktrees; set on a worktree it
applies only there.

## Tasks

- [ ] Generalise the resolution machinery so a typed, non-text value uses the same
      chain, the same per-key rule, and the same append-only storage discipline.
- [ ] Define the first real setting and its shipped default, chosen so the steering
      feature can consume it unchanged.
- [ ] Add a read of the effective value plus its source scope, through the client
      boundary, so a later interface can show "inherited from X".
- [ ] Unit tests: precedence, per-key independence, typed parse failures refused at
      save time, unset chain falls back to shipped default.
- [ ] Integration test with containers: values at three levels resolve correctly for
      a worktree.

## Done when

A non-text setting obeys exactly the same inheritance as an instruction, with its
source scope reportable, and no parallel mechanism exists.
