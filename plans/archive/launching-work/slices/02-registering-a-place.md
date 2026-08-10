# Slice 2 — Registering a place

**Discharges:** IB §4 (places without work), IB §5 (registration is a guarded
mutation).

**Demo:** an operator registers a repository that has never run anything; it appears
as a place with no work; registering a non-existent path, a relative path, or a
non-repository is refused with a specific message; registering the same place twice
is a no-op.

## Tasks

- [x] Add the registry write behind its own port: register a place, validated
      server-side (absolute, exists, is a repository), idempotent on the natural key.
- [x] Keep probe-derived hierarchy authoritative: a registration must not contradict
      what the probe establishes for the same place.
- [x] Include registered-but-idle places in the registry reads so they appear in the
      hub with no work.
- [x] Require authentication and the mutation request rules; record which principal
      registered the place.
- [x] Add the interface: register from the settings destination and from the empty
      state of the places list, with inline refusals.
- [x] Unit tests: validation matrix, idempotency, and that a registration cannot
      invent a parent that contradicts the probe.
- [x] Integration tests with containers: registered places survive restart and appear
      in reads.

## Done when

An operator can add a place before any work exists in it, invalid registrations are
impossible, and idle places are visible.
