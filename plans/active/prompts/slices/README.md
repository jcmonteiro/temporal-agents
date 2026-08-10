# Vertical slices — Prompts

Slices 1–2 are the **foundation** and ship before the steering feature, which
depends on a scoped setting and a governed conversational instruction. Slices 3–4
are the configuration surface and ship after steering.

| # | Slice | Demo |
|---|-------|------|
| 1 ✅ | [Stored instructions with provenance](./01-stored-instructions.md) | shipped defaults live in storage; a run records which instruction version it used, and `history` shows it |
| 2 | [Scoped settings](./02-scoped-settings.md) | a non-text setting resolves through the place chain, demonstrated by one real setting |
| 3 | [Override, inherit, reset — API](./03-override-api.md) | an override is saved for one place, inherited by its worktree, refused when invalid, and reset in one call |
| 4 | [Prompt configuration surface](./04-configuration-surface.md) | an operator edits instructions globally and per place in the hub, sees the diff and the inherited source, and resets |
