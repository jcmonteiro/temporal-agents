# Brief — Human-in-the-loop steering

## Problem / opportunity

A review loop runs to completion on its own. The agent reviews, implements the
review's feedback, reviews again, and keeps going until nothing is left to commit or
until it hits its pass limit. The operator learns what happened only at the end.

That autonomy is valuable when the loop is on the right track and expensive when it
is not:

- **A wrong turn compounds.** Each pass builds on the previous one, so a
  misunderstanding in pass one is reinforced through every later pass, and the
  operator pays for all of them.
- **The operator's knowledge arrives too late.** The reasons a piece of feedback
  should be ignored, or a change made differently, exist in the operator's head while
  the loop runs, and the loop has no way to ask.
- **Hitting the pass limit is a dead end.** The loop stops with feedback still
  outstanding, and the operator has no way to say either "keep going, you are close"
  or "this is good enough, stop".
- **The operator is not told when a decision is due**, so even a loop that could wait
  has nobody watching it.

## Who has it

The operator, who has context the agent does not, and who currently either accepts
autonomous outcomes or babysits a terminal.

## Desired outcome

When this ships:

- After each review round, the loop **pauses and asks the operator for guidance**
  before the next implementation pass.
- The operator is **told that a decision is waiting** — in the hub, and through the
  notification channels already in use — and is reminded until they act.
- The loop **waits as long as it takes**. Nothing expires while a human is thinking.
- The operator can **give guidance directly**, or **be questioned by an agent** that
  draws the guidance out of them, and can edit the result either way.
- The operator's guidance **reaches the implementation pass** together with the review
  material it applies to.
- The operator can also **decline to guide** and let the pass proceed, or **stop the
  loop** deliberately.
- On reaching the pass limit, the operator **decides**: continue with a fresh budget,
  or accept the work as finished.
- Every ending is **recorded honestly**: converged on its own, accepted by a human,
  stopped by a human, or stopped by the limit.
- Steering is the default, and an operator can explicitly disable it for work that must run autonomously.

## Success signals

- An operator redirects a loop that started down the wrong path, without restarting
  it and without a terminal.
- A loop that reaches its pass limit ends by a human decision, not by silence.
- An operator away from the hub still learns that a decision is waiting, and finds it
  waiting when they return, hours later.
- A run's history states why the loop ended and what guidance it was given.
- An unattended run with steering explicitly switched off behaves exactly as before.

## Success signals that must not regress

- The pass limit still bounds how much work happens without human involvement.
- Token usage stays attributable per run, including the cost of the conversation.

## Scope boundaries (explicitly out)

- **Command-line steering.** Interaction happens in the hub only.
- **Changing configuration from a conversation.** Guidance applies to one run; it
  never edits stored instructions.
- **Steering anything other than review rounds** — not develop, not planning, not
  arbitrary pauses.
- **Restricting who may answer.** Any signed-in operator may respond.
- **Notifications to a closed browser through the browser itself.** Existing channels
  cover that case.
- **Editing the agent's work directly** from the conversation; guidance is text, not a
  patch.
