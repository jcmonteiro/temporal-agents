# Brief — Agent Hub frontend ("Orbit" overview)

## Problem / opportunity

`temporal-agents` is operated entirely through a CLI. To see what agent work is
running, waiting, paused, or failed, an operator must run `list`, `watch`, and
read Temporal's web UI, then hold the overall picture in their head. There is no
single, glanceable view of "what is happening right now" across all the agent
workflows a person owns.

The opportunity is a web surface — **Agent Hub** — that presents active agent
work as a living overview, so an operator understands the state of everything at
a glance and can drill into any single piece of work.

## Who has it

The person running the worker and submitting work: the operator/owner of the
agent workflows (the "Luiza" in the concept). Single-user, self-hosted context —
the same person who runs the CLI today.

## Desired outcome

When this ships, the operator opens Agent Hub and sees, on one screen:

- Every piece of active work rendered as an item **orbiting** a central point,
  giving an immediate sense of "what's around me today".
- Each item's **status** distinguishable at a glance (todo, in progress, paused,
  waiting input, waiting, done, failed/blocked), with a legend that names each
  state.
- The ability to **select** any item and read its details (which grouping it
  belongs to, progress, estimate, owner) in a dedicated panel.
- A short **"up next"** queue of work that has not yet started.
- Enough spatial control (zoom, recenter) to keep the overview readable as the
  amount of work grows.

The overview is the first-class deliverable. The other destinations named in the
concept (Fleets, Workflows, Templates, Insights, Settings) exist as reachable
places but are not required to be functional for this outcome to be met.

## Success signals

- An operator can name, without reading the CLI, how many pieces of work are in
  each state right now.
- An operator can go from "open the app" to "read the details of one specific
  piece of work" without leaving the overview screen.
- The overview stays legible (no overlap that hides status) as the number of
  work items changes.
- A new contributor can locate where a piece of the UI lives and add to it,
  because the frontend follows a familiar, conventional structure.

## Scope boundaries (explicitly out)

- **Authentication / authorization** — no login, no identity, no roles. The app
  assumes a single trusted local operator.
- **A production backend / live data source** — this brief does not require the
  overview to be wired to real running workflows. Being fed by a stand-in data
  source is acceptable for the outcome; a real data feed is a later concern.
- **The non-overview destinations** — Fleets, Workflows, Templates, Insights,
  Settings are navigable placeholders only.
- **Mutating agent work from the UI** — starting, stopping, or editing workflows
  is out; this is a read/observe surface.
- **Multi-user concerns** — sharing, permissions, presence, notifications
  delivery. The bell/notifications and search are presentational only.
- **Any change to the Go worker/CLI behaviour.**
