# temporal-agents

A thin [Temporal](https://temporal.io) worker that runs a [Pi](https://pi.dev/) coding agent. Kick off one-off prompts, schedule recurring runs, or drive full code workflows (develop → review → PR → review w/ Copilot) against the current repo — all durable and observable via Temporal.

## Requirements

- Go 1.25.4+
- `pi` CLI on your `PATH` (the agent the worker runs)
- Docker (for the local Temporal server)

## Install

```sh
make install        # go install → $GOBIN (or $GOPATH/bin); ensure it's on PATH
# or
make build          # build ./temporal-agents in the current dir
make setup          # optional: enable git hooks (gofmt on commit)
```

## Run

1. Start the Temporal dev server (gRPC on `17233`, web UI on http://localhost:18233):

   ```sh
   docker compose up -d
   ```

2. Start the worker (executes workflows + notifications):

   ```sh
   temporal-agents worker
   ```

3. In another terminal, submit work:

   ```sh
   temporal-agents run "summarize the README"
   temporal-agents watch <workflow-id>
   ```

The CLI connects to `localhost:17233` by default. Override with `TEMPORAL_ADDRESS`.

## Commands

| Command | What it does |
|---|---|
| `worker [--no-desktop] [--webhook <url>]` | Start the worker. Desktop notifications on by default (macOS); `--webhook` POSTs completion JSON. |
| `run "<prompt>" [--save <name>] [--chain]` | Start a workflow and return immediately. |
| `schedule "<interval\|cron>" "<prompt>" [--save <name>] [--chain]` | Run a workflow on a schedule (overlaps skipped). Interval = Go duration (`1h`, `30m`); or a 5-field cron. |
| `template <list\|show\|run\|delete> [name]` | Manage/run prompts saved via `--save`. |
| `code pilot [--append\|--replace <prompt>] [--chain] [--summary]` | Address unresolved review comments on the current branch's PR. |
| `code review [--summary]` | Review the current branch locally, then implement + re-review in a loop. |
| `code develop "<prompt>" [--branch <name>] [--worktree] [--summary] [--with-remote]` | Create a branch, implement the prompt, then run the review loop (and PR + pilot with `--with-remote`). |
| `watch <workflow-id>` | Stream a workflow's live Pi progress, then its result. |
| `list` | List running workflows and schedules. |

Common flags: `--save <name>` stores the invocation as a reusable template; `--chain` re-triggers on each success; `--summary` (code subcommands only) sends a Pi-generated summary as the webhook body.

Run any command with `--help` for details, e.g. `temporal-agents code develop --help`.

## Docker

Only the Temporal server runs in Docker (see `docker-compose.yml`); the worker and CLI run on the host so the agent can operate on your local repo.

```sh
docker compose up -d      # start Temporal (data persists in a named volume)
docker compose down       # stop; add -v to wipe stored workflow state
```
