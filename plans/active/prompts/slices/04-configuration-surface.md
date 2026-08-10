# Slice 4 — Prompt configuration surface

**Discharges:** brief outcome (edit from the hub, see the effect before saving,
reset in one action), IB §5 (blast radius made visible), IB §3 (no client-side
inheritance).

**Demo:** in the hub, an operator edits an instruction globally, sees a diff against
the shipped default before saving, saves, then overrides the same instruction for
one repository and sees the worktree page report the inherited value and its source.
Both resets work from the interface.

## Tasks

- [ ] Add the configuration destination listing every instruction key with its
      effective value, source scope, and an overridden/inherited indicator.
- [ ] Add the editor: current text, inherited text, read-only system block, required
      inserts listed, character bound shown.
- [ ] Show a **diff against the inherited value** before saving, and state the blast
      radius ("applies to this place and everything inheriting from it").
- [ ] Add both resets with confirmation: return to inherited, and return to shipped
      default.
- [ ] Reach the per-place scope from the place page as well as from the
      configuration destination.
- [ ] Surface refusals from validation inline against the offending field.
- [ ] Component tests: inherited versus overridden rendering, diff shown before
      save, refusal rendering, both resets, and that no inheritance is computed in
      the client.

## Done when

An operator tunes instructions globally and per place from the hub, understands the
effect before saving, and can always get back to the inherited or shipped text.
