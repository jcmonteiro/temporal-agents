# Slice 3 — Override, inherit, reset (API)

**Discharges:** IB §2 (validated overrides, non-overridable system block, bounded
length), IB §1 (per-key resolution), brief outcome (set globally, override per
place, return to inherited or shipped).

**Demo:** save an override for one repository; a worktree of it inherits the new
text; save an override that omits a required insert and the call is refused naming
the insert; reset the place override and the inherited value returns; reset the
global override and the shipped default returns.

## Tasks

- [ ] Add reads: the catalogue with, per key, the effective value for a scope, the
      inherited value, the source scope, whether it is overridden here, the system
      block (read-only), and the required inserts.
- [ ] Add writes: set an override for global or one place; reset an override at
      either scope. Writes are idempotent and produce a new version.
- [ ] Validate on save: required inserts present, template parses, length within
      bound, system block not smuggled in. Refusals are problem documents naming the
      cause.
- [ ] Mark machine-contract keys as advanced in the payload, and publish their
      system block alongside.
- [ ] Enforce authentication and the mutation request rules from the authentication
      feature; record which principal saved a version.
- [ ] Unit tests: validation matrix per key; reset semantics at both scopes;
      inheritance after each write; a refused save leaves the previous version
      effective.
- [ ] Integration tests with containers: concurrent saves produce distinct versions
      with a deterministic winner for the pointer.

## Done when

Overrides can be set and cleared at both scopes through the API, invalid overrides
are impossible to store, and inheritance is observable in the response.
