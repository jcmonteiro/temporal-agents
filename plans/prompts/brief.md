# Brief — Prompts

## Problem / opportunity

The instructions handed to the agent *are* the product's behaviour: how it reviews,
how it acts on feedback, how it slices a goal into a plan. Today every one of them
is compiled into the binary. Three consequences follow.

First, **tuning requires a rebuild**. An operator who learns that reviews should
insist on integration tests cannot express that without editing Go source and
reinstalling.

Second, **one instruction must serve every repository**. A repository with strict
infrastructure checks and a small library with none need different review
instructions, and there is nowhere to put the difference.

Third, **results are not explainable**. When a run produced a poor change, there is
no record of the exact instruction that produced it, so nothing can be learned from
it and nothing can be reproduced.

## Who has it

The operator tuning the agent's behaviour, and anyone later trying to understand why
a past run behaved as it did.

## Desired outcome

When this ships:

- Instructions are **stored, not compiled**, and an operator changes them from the
  hub.
- An instruction can be set **once globally** and **overridden for one place**, with
  places inheriting from the place they belong to, so a repository's setting covers
  its worktrees automatically.
- An operator can **return to the inherited instruction**, and can **return to the
  shipped default**, without remembering what either said.
- An operator **sees the effect of a change before saving it**, and cannot save an
  instruction that would break the run it governs.
- Every run **records which instruction it used**, so a past result is explainable
  and reproducible, and editing an instruction never rewrites the history of runs
  already finished.
- **Upgrades still improve the defaults**: a better shipped instruction reaches
  every place that has not overridden it.
- Behaviour is unchanged for anyone who configures nothing.

## Success signals

- An operator changes how reviews are performed for one repository, without
  affecting another, and without rebuilding anything.
- An operator answers "which instruction produced this pull request?" from the hub.
- An operator restores a default they experimented away from, in one action.
- An attempt to save an instruction that omits required material is refused with an
  explanation, not accepted and silently broken.
- A run in flight is unaffected by an instruction edited while it runs.

## Scope boundaries (explicitly out)

- **Steering conversations changing instructions.** Guidance given to a specific
  run is context for that run, never a configuration change.
- **Authoring help** — no suggestions, linting of prose, or generated instructions.
- **Experimentation features** — no A/B tests, no per-run comparisons.
- **Sharing or importing instruction sets** between installations.
- **Instruction text that code parses** becoming freely editable: the shapes the
  system depends on stay the system's own.
- **Per-run overrides beyond what the CLI already offers.**
