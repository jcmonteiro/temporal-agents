# Brief — Authentication

## Problem / opportunity

The hub is an observe-only surface today, so "anything that can reach the port is
the operator" has been an acceptable rule. That rule expires with the next two
features: the hub is about to **start agent work** on the operator's machine and
to **hold conversations** that steer it. At that point an unauthenticated surface
means any page the operator visits in the same browser can start an agent, and any
process on the machine can answer a steering question on the operator's behalf.

There is also a second, slower need. The hub is useful enough to be shared — a
small team watching one worker, or a hosted instance. That requires signing in with
an existing organisational identity rather than inventing another credential.

## Who has it

The operator, who needs the hub's new powers to be reachable only by them, and the
operator's future team-mates, who will want to sign in with the identity they
already have.

## Desired outcome

When this ships:

- **Only a signed-in person can use the hub.** There is no unauthenticated mode
  left on by accident.
- **Signing in uses an existing identity provider**, so no new password is
  invented, and pointing the hub at a company provider later is a matter of
  configuration rather than of code.
- **Sessions are real:** they can be ended deliberately, they expire, and ending
  one takes effect immediately.
- **Actions carry who did them.** Starting work and answering a steering question
  are attributable, which is what makes a shared instance trustworthy.
- **Automation keeps working.** Scripts and the CLI authenticate without a browser.
- **Local use stays a single command.** Turning on authentication must not turn a
  one-command local tool into a configuration exercise.
- **Nothing the hub already does becomes harder:** the overview, history, and the
  CLI keep their current behaviour behind the new door.

## Success signals

- Opening the hub without signing in reaches a sign-in step and nothing else.
- Signing out, or ending a session, immediately stops that browser from reading or
  changing anything.
- A run started from the hub records who started it, and a steering answer records
  who answered.
- The CLI works with no browser and no interactive step.
- A local operator starts the hub and signs in without editing configuration files.
- Pointing the hub at a different identity provider requires no code change.

## Scope boundaries (explicitly out)

- **Roles, permissions, and multi-tenancy.** Any signed-in person is the operator.
- **Filtering work by person.** Everyone signed in sees all the work; only personal
  surfaces (an inbox, later) are per person.
- **User management** — invitations, approval flows, profile editing.
- **Encrypting or protecting the machine itself.** The hub runs agents on a trusted
  machine; this feature guards the hub's surface, not the host.
- **Changing what existing worker or CLI commands do.**
