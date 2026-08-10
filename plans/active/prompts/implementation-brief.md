# Implementation brief — Prompts

Derived from `brief.md`. Constraints and seams. Concrete choices are marked **hard
constraint** with the reason.

## 1. Resolution model (hard constraints)

- **A project is a place.** Scoped values resolve
  `run -> place -> parent place -> ... -> base place -> global -> factory`, and a
  descendant inherits an ancestor's value. *Reason:* a second grouping concept
  beside places could disagree with places, and the disagreement would be
  invisible.
- **Resolution is per key**, never per bundle: a place may override one instruction
  and inherit the rest.
- **A value is resolved once per unit of work**, in an activity, and the resolved
  text travels with the work. Units: a chain for an agent instruction, a session for
  a conversational instruction. *Reasons:* workflow code must not perform I/O, and a
  loop that re-resolves per pass would let a mid-run edit change what an already
  recorded pass did.
- **Storage is append-only versions.** An edit adds a version; a reset moves or
  removes a pointer; nothing that a finished run referenced is ever mutated.
  *Reason:* provenance must stay resolvable after any later edit.
- **Every execution records which value it used** — key, scope it came from,
  version, and a content hash — and the full text stays recoverable from the version
  record rather than being copied per run.

## 2. Safety of overrides (hard constraints)

- **Overridable text is a template with declared required inserts.** Saving
  validates that every required insert is present and that the template parses;
  a failing save is refused and names what is missing. *Reason:* an override that
  drops the material the agent must act on produces a plausible-looking run with
  no input, which is expensive to diagnose.
- **Text that a parser depends on is not overridable.** The system appends its own
  block after the operator's instruction, so the operator changes *how*, never the
  *shape* the code consumes. *Reason:* no operator should be asked to maintain a
  machine contract by hand.
- **Length is bounded**, and a refusal explains the bound rather than truncating.
- **The shipped defaults remain the source of truth in code** and are published into
  storage at startup, so an upgrade reaches every place that has no override, and
  "return to the shipped default" means the current one.

## 3. Seams

- The resolution rules and the scope chain belong to the application core, with a
  driven port for storage; the core must not know SQL.
- The same scope-chain machinery must serve **non-text scoped settings** (feature
  enablement), because a later feature needs exactly that and a parallel mechanism
  would duplicate the rules.
- Recording provenance uses the existing durable execution record; it must not
  change what that record already means (each row still reports its own facts).
- The frontend reaches values and their provenance through the existing client
  boundary and never re-derives inheritance.

## 4. Non-functionals

- Resolution happens once per unit of work and must not add a per-step database
  dependency to a running loop.
- A storage failure at resolution must fail the unit of work **visibly** rather than
  silently substituting a default, because a silent substitution changes agent
  behaviour without record.
- Startup publication of shipped defaults is idempotent and safe to run concurrently
  by two processes.

## 5. Risks and open decisions

- **Blast radius of a global edit** is every place that inherits it; the interface
  must make the inherited-versus-overridden distinction and the pending change
  visible before saving.
- **Machine-contract instructions** (plan decomposition) are the most valuable to
  tune and the most dangerous to break; they stay overridable with the system block
  enforced, and are marked as advanced.
- **Bounded history growth** of versions is accepted; pruning is not part of this
  feature.
- Whether a place page or a settings destination hosts the editor is a presentation
  decision, provided both global and per-place scopes are reachable.

## 6. Seams this work must not cross

- No conversational feature may write configuration.
- No change to what existing commands do when nothing is configured.
- No copying of instruction text into execution rows in place of provenance.
