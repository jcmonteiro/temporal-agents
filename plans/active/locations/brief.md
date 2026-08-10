# Brief — Locations

## Problem / opportunity

Agent work no longer happens in one place. An operator runs `develop` in a main
checkout, lets a fleet node work in its own worktree, and lets a pilot loop act
on a pull request that has no local checkout at all. The hub shows all of it as
one undifferentiated orbit: nothing on screen says *where* a piece of work is
happening, work from unrelated repositories sits side by side, and there is no
way to see how loaded one repository is.

The same gap blocks everything planned after it. Starting work needs a place to
start it in. A per-project setting needs a project. A notification about waiting
work needs to say which repository is waiting. Each of those would otherwise
invent its own grouping.

## Who has it

The operator who runs the worker and submits work — the single, self-hosted user
of the hub, now working across several repositories and worktrees at once.

## Desired outcome

When this ships:

- Every piece of work in the hub reports **where it runs**, and says so honestly:
  work whose place cannot be established is shown as *unknown*, never guessed.
- Places form a **hierarchy** that reflects reality: a worktree belongs to the
  repository it was created from; work with no local directory is a place of its
  own.
- The overview **groups work by place** — one body per place, with that place's
  work around it — so an operator sees at a glance which repository is busy and
  which is idle.
- Deeper places **fold into their parent** as the operator zooms out, and a single
  action folds everything into its topmost place, giving one body per repository.
- The picture stays **legible** as places and work multiply: a crowded place folds
  rather than overlapping its work into illegibility.
- An operator can open **one place** and see just that place's work.

## Success signals

- An operator answers "which repository is this run working in?" from the hub
  alone, without the CLI and without reading a workflow input.
- An operator answers "how much work is running in repository X right now?" by
  folding the view, in one action.
- Work whose place is unknown is visibly unknown; no item is shown under a place
  it does not belong to.
- Repeated refreshes of the overview do not reshuffle places, so the picture is
  stable enough to read.

## Scope boundaries (explicitly out)

- **Starting, stopping, or re-running work** from any place page — a later
  feature; this one observes.
- **Per-place configuration** of any kind (prompts, defaults, enablement) — a
  later feature.
- **Registering a place by hand.** This feature reports only places that recorded
  work establishes; explicit registration arrives with the feature that needs it.
- **Grouping by branch.** A branch is a property of a run, not a place.
- **Relations between places** beyond the worktree-to-repository one that git
  itself states.
- **Authentication**, and any change to what existing worker or CLI commands do.
