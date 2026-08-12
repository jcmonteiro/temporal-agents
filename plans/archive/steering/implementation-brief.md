# Implementation brief — Human-in-the-loop steering

Derived from `brief.md`. Constraints and seams; concrete choices are marked **hard
constraint** with the reason.

## 1. Where the wait lives (hard constraints)

- **The wait is a durable orchestrated unit of its own**, signalled to proceed — not a
  long-running activity, and not a blocking wait owned by the API process.
  *Reasons:* an activity holding a human wait occupies a worker slot and must heartbeat
  for days, and a worker restart would lose the conversation; an API-owned wait splits
  human-in-the-loop state across two lifecycles, so a lost message leaves a loop
  blocked with no visible cause.
- **The wait is unbounded.** Nothing on the wait path carries an execution timeout, and
  the only timer permitted is the reminder, which never ends the wait.
- **The waiting session is not a separate item in the overview.** The loop's own item
  reports that it needs input. *Reason:* one piece of work must not appear twice.
- **The session records its own execution row** so token usage stays attributable
  without becoming a second satellite.
- **The unit must never restart itself in a way that loses conversational context.**
  If conversational memory is keyed on the unit's identity, that identity must be
  stable for the session's whole life, and the constraint must be stated in the code.

## 2. The decision contract (hard constraints)

- Exactly **two pause points**: after a local review produces material, and after the
  remote review's unresolved comments are fetched — both immediately before the agent
  acts on that material.
- **Three decisions:** proceed with guidance, proceed without guidance, and stop. The
  last ends the loop as needing a human.
- **Guidance is mandatory when proceeding with guidance**; empty text is not a
  decision, and the operator must use "proceed without guidance" instead. *Reason:* an
  empty guidance block is indistinguishable from a mistake.
- **The first decision wins**, and a repeat returns the recorded decision instead of an
  error. *Reason:* two browser tabs and retried requests are normal, and a second
  decision must never start a second implementation pass.
- **Reaching the pass limit becomes a decision point** with two outcomes: continue with
  the counter reset, or accept the work as finished. Resets are unlimited and always
  human-gated; the accumulated cost must be visible at the moment of deciding.
- **A loop's ending is recorded as a named outcome** — converged, accepted by a human,
  stopped by a human, or limit reached — because a single "converged" flag cannot
  express the now-reachable endings, and the interface and history must both explain
  why a loop ended.
- **Steering is on by default and can be disabled through the scoped setting or a
  per-run command-line option**, resolved once at the start of the work. *Reason:*
  interactive review is the normal behavior, while unattended runs need an explicit
  autonomous mode.

## 3. The guidance artifact (hard constraints)

- **A session produces exactly one artifact: guidance text.** It is what reaches the
  agent, fenced as an additive block, placed after the instruction and before the
  material it applies to. No existing composed section changes.
- **Typing the guidance and being questioned by an agent are peer producers** of that
  text, and the text stays editable until the decision is sent. A session starts with
  **no agent cost**; questioning happens only when asked for.
- **The conversation itself never reaches the implementing agent** — only the final
  text does. *Reason:* cost, noise, and the operator's right to edit.
- **The guidance block is bounded**, and an over-long block is refused with an
  explanation rather than truncated, because silent truncation would drop the operator's
  most recent sentences.
- The questioning agent is **read-only** with respect to the repository. *Reason:* a
  conversational agent with write access while a review loop is mid-flight can corrupt
  the working tree.
- The questioning agent's instruction is a **governed instruction** under the prompts
  feature, resolved once per session.

## 4. Durability, streaming, and identity (hard constraints)

- **The conversation is authoritative in the durable store; only decisions and the
  final text belong to orchestration history.** *Reason:* streaming partial text into
  history would bloat it and make replay expensive, while the decision must be in
  history because behaviour depends on it.
- **Streaming is resumable by sequence**, so a reload, a second tab, or a sleeping
  laptop loses nothing, and the transport reconnects natively.
- **Server push carries events, not data.** The list read path keeps polling; push must
  not become a second source of truth for state.
- **A notification is addressed** to the initiating principal when known, and to every
  principal when not (command-line and scheduled work has no initiator), with read
  state per principal. *Reason:* an unbounded wait must not be stranded because the
  initiator is absent or does not exist.
- **Reminders repeat daily, without limit**, until the decision is made.
- **Any signed-in operator may answer**, and each contribution records who made it.

## 5. Failure behaviour (constraints)

- The conversational layer must never be able to block the loop: if questioning is
  unavailable, giving guidance directly, proceeding without guidance, and stopping all
  remain possible.
- A lost or duplicated decision must not produce two implementation passes.
- If the durable store is unavailable at the moment of a decision, the decision must
  fail visibly to the operator rather than appear accepted.

## 6. Risks and open decisions

- **A loop can now sit open for days**, so the interface must make waiting work
  prominent and say since when.
- **Cost of questioning** is real and operator-driven; it must be visible while the
  conversation grows.
- **Unlimited resets** are deliberate; the guard is human attention plus visible cost.
- **Granularity of what the questioning agent may read** (the material, the repository,
  the branch state) is open; more context costs tokens and adds latency.
- **Reminder fatigue** versus missed decisions: daily and unlimited is the chosen
  balance, and it must be trivially observable which loops are waiting.

## 7. Seams this work must not cross

- No command-line conversation surface; the command line only selects autonomous mode.
- No configuration written from a conversation.
- No second satellite for a waiting session, and no duplicate of the run's identity.
- No change to unattended behaviour when steering is off.
