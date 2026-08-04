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
  belongs to, progress, and status) in a dedicated panel.
- A short **"up next"** queue of work that has not yet started.
- Enough spatial control (zoom, recenter) to keep the overview readable as the
  amount of work grows.

The overview reflects **real agent work** from the Temporal server this repo
already runs against — an operator sees actual workflow executions and their
states, not a mockup. (Development proceeds against a stand-in data source, but
the shipped outcome is wired to live data.)

Each orbiting item is a top-level piece of work — either an entire **fleet** or a
**standalone workflow** (a run started via `run`, `schedule`, or `code
develop`). Selecting one shows its details; from a fleet's details the operator
can open a dedicated **fleet view** that shows that fleet's work and how its
pieces depend on one another. The app is single-operator with no identity: there
is no personalized greeting or owner attribution.

The **Overview** (orbit) and the **Fleet view** are the first-class
deliverables. The remaining destinations (Workflows, Templates, Insights,
Settings) exist as reachable places but are not required to be functional for
this outcome to be met.

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
- **A production backend, hosting, or persistence layer** — no new services,
  databases, or deployment. The only data path added is a **minimal, local,
  read-only feed** that reads existing Temporal workflow state and exposes it to
  the frontend. Anything beyond read-only local observation is out.
- **Workflows, Templates, Insights, Settings** — navigable placeholders only.
  (Overview and the Fleet view are in scope; the rest are not.)
- **Mutating agent work from the UI** — starting, stopping, or editing workflows
  is out; this is a read/observe surface.
- **Multi-user concerns** — sharing, permissions, presence, notifications
  delivery. The bell/notifications and search are presentational only.
- **Any change to the Go worker/CLI behaviour.**
