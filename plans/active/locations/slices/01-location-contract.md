# Slice 1 — Location contract (registry + references, all unknown)

**Discharges:** IB §1 (published contract), IB §2 (unknown is a real place).

**Demo:** `GET /api/v1/fleets|runs|schedules|fleets/:id` each return a
`locations` array holding exactly the unknown location, and every item carries
`locationId: "unknown"`. The OpenAPI document describes the tagged union. `list`
and `history` behave exactly as before.

## Tasks

- [ ] Add the location model to the application core as a **closed set of
      variants** with constructors as the only way to build one (unknown,
      directory, remote), so an invalid location cannot be expressed by a struct
      literal outside the package. Accessors report their value only for the
      matching variant.
- [ ] Validate on construction: a directory is absolute and cleaned; a ref is
      non-empty and bounded; the unknown variant takes no parent.
- [ ] Add registry construction: given the locations referenced by a response,
      emit the **ancestry closure**, ordered parents-first, ids stable and
      deterministic, labels server-computed.
- [ ] Extend the wire representations with `locationId` on fleets, runs,
      schedules, and fleet nodes, and with the `locations` array on every response
      that carries them. Keep the paged active-work model's field **optional**.
- [ ] Extend the published schema with the union (discriminated on the variant)
      and the registry property; extend the schema tests rather than rewriting
      them.
- [ ] Assert **deterministic serialisation** of the registry, and that entity tags
      stay stable for an unchanged read (IB §1).
- [ ] Unit tests (behaviour, no database): a response referencing several
      locations emits each once; ancestry is closed; ordering is parents-first;
      an item with no known place references the unknown location.
- [ ] Contract tests: existing consumers decode the new payload unchanged; no
      field became required.

## Done when

Every collection and item response publishes the registry and a reference, the
schema documents the union, the only location in the system is `unknown`, and no
existing command or client changed behaviour.
