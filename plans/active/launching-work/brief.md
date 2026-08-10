# Brief — Launching work from the hub

## Problem / opportunity

The hub shows work but cannot start it. An operator watching the overview who
decides "this repository needs a develop pass" must leave the hub, find a terminal,
change into the right directory, recall the command and its flags, and only then
start the work. The hub then shows the result of a decision it had no part in.

That detour costs more than keystrokes:

- **The working directory is implicit and easy to get wrong.** The command acts on
  wherever the shell happens to be, and a run started in the wrong checkout is
  discovered only afterwards.
- **Repeating a past run means reconstructing it** from memory or from history
  output, including the prompt.
- **Two loops can be started in the same checkout**, where they stash and commit
  over each other.
- The next feature makes the hub the place where an operator *converses* with a run;
  a hub that can steer work but not start it is an odd surface.

## Who has it

The operator, who already lives in the hub to watch work and wants to act from
there.

## Desired outcome

When this ships:

- An operator **starts agent work from the hub**, choosing what to do and where,
  and lands on that run's own page.
- **Where the work runs is chosen, not typed**: the operator picks a known place, so
  a wrong or non-existent directory cannot be submitted.
- An operator can **register a place** they have not run anything in yet, so the
  first run in a repository is possible from the hub.
- An operator can **repeat a past run** without re-entering anything.
- **Double submission is impossible**: an impatient click, a retry, or a reload
  never produces two runs.
- **Conflicting work is refused with an explanation**, not silently allowed to
  corrupt a working tree.
- Every run started from the hub **records who started it**.
- The overview is unchanged as a *watching* surface: starting work happens on the
  dedicated pages, not on the overview.

## Success signals

- An operator starts a develop run in a chosen repository, from the hub, and watches
  it progress on its page without touching a terminal.
- An operator repeats yesterday's run in one action.
- Attempting a second concurrent loop in the same checkout is refused with a message
  naming the conflict.
- Clicking the start action twice produces exactly one run.
- No run started from the hub exists whose place is unknown.

## Scope boundaries (explicitly out)

- **Starting fleets and creating schedules**, and any plan-approval interface.
- **Editing, stopping, or cancelling** running work.
- **Templates or saved presets** for launching.
- **Steering a running loop** — the next feature.
- **Starting work from the overview.**
- **Running anything on a machine other than the one the worker runs on.**
