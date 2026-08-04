# Fleet workflows

The `fleet` package implements fan-out orchestration on top of Temporal. It
exposes two workflows:

- **`FleetPlanWorkflow`** — the *plan* step. A read-only Pi agent decomposes a
  high-level goal into a validated dependency graph (`FleetPlan`) for the user
  to review before executing. It makes no code changes.
- **`FleetWorkflow`** — the *execute* step. It orchestrates an approved
  `FleetPlan` in dependency layers, running one child
  `codereview.DevelopWorkflow` per node.

Both are driven from `workflow.go`; the pure domain logic (plan validation,
`TopoLayers`, prompt building, plan parsing, result aggregation) lives in
`domain.go`; the driven adapters (Pi agent, Git) are wired through
`activities.go` / `ports.go`.

> Dependencies gate both **ordering** and **what a node builds on**. Every node
> develops on its own run-scoped branch/worktree cut from a single pinned base,
> and a dependent's branch is **seeded by merging in its dependencies' branches**
> — so it is developed and reviewed on top of the committed, reviewed work of
> the slices it depends on.

---

## `FleetPlanWorkflow` — decompose a goal into a plan

```mermaid
flowchart TD
    start([FleetPlanInput: Goal, WorkDir]) --> gp

    subgraph gp["GeneratePlan activity (RetryPolicy: MaxAttempts 1)"]
        direction TB
        fpBefore["Git.Fingerprint(WorkDir) → before"]
        fpBefore --> sandbox["Git.AddDisposableWorktree → sandbox<br/>(discarded on defer)"]
        sandbox --> agent["Agent.RunReadOnly(BuildPlanPrompt, sandbox)<br/>read-only tool policy"]
        agent --> fpAfter["Git.Fingerprint(WorkDir) → after"]
        fpAfter --> tripwire{"after == before?"}
        tripwire -->|no| mutated["NonRetryable: PlanningMutatedRepo"]
        tripwire -->|yes| parse["ParsePlan(output)"]
        parse -->|error| invalid1["NonRetryable: InvalidPlan"]
        parse -->|ok| validate["ValidatePlan(plan)"]
        validate -->|error| invalid2["NonRetryable: InvalidPlan"]
        validate -->|ok| result["GeneratePlanResult: Plan, Tokens"]
    end

    result --> notify["wfnotify: 'Fleet plan ready'<br/>(node count + planning tokens)"]
    notify --> done([return FleetPlan])

    mutated --> fail([return error])
    invalid1 --> fail
    invalid2 --> fail
    agent -->|error| fail

    fail -.->|defer| failnote["wfnotify: 'Fleet planning failed'"]
```

**Notes**

- Planning is contracted read-only and *enforced*: the agent runs in a
  disposable detached worktree under a read-only tool policy, and a content
  **fingerprint tripwire** re-reads the source repo afterwards — a mismatch
  fails non-retryably (`PlanningMutatedRepo`) so a plan is never returned from a
  run that escaped the sandbox.
- The agent step is not retried (`MaximumAttempts: 1`): a re-run would produce a
  different graph.

---

## `FleetWorkflow` — orchestrate the approved plan

```mermaid
flowchart TD
    start([FleetInput: Plan, WorkDir, WorktreesDir, Summary, WithRemote]) --> wtguard{"WorktreesDir set?"}
    wtguard -->|no| errWT["NonRetryable: MissingWorktreesDir"] --> fail

    wtguard -->|yes| topo["TopoLayers(Plan)"]
    topo -->|error| errPlan["NonRetryable: InvalidPlan"] --> fail
    topo -->|ok| resolve["ResolveBase activity: Git.Head(WorkDir) → base<br/>(RetryPolicy: MaxAttempts 3)"]
    resolve -->|error| fail
    resolve -->|ok| layerLoop{"more layers?"}

    layerLoop -->|yes| layer["process next layer<br/>(nodes sorted, deterministic)"]

    subgraph layerBody["for each node in layer"]
        direction TB
        nodeCheck{"blockingDependency?<br/>(a dep failed/skipped)"}
        nodeCheck -->|yes| skip["record StatusSkipped<br/>(names blocking dep)"]
        nodeCheck -->|no| spawn["ExecuteChildWorkflow codereview.DevelopWorkflow<br/>WorkflowID = fleetID-nodeID<br/>Branch = NodeBranch(fleetID, id)<br/>StartPoint = base<br/>MergeBranches = DependencyBranches(...)<br/>AwaitReview = true"]
    end

    layer --> layerBody
    layerBody --> settle["await all started children in layer"]
    settle --> record{"child result"}
    record -->|error| markFail["record StatusFailed"]
    record -->|ok| markOk["record StatusSucceeded<br/>+ ParseTokenTotal(detail)"]
    markFail --> layerLoop
    markOk --> layerLoop
    skip -.-> settle

    layerLoop -->|no more layers| cancel{"ctx.Err()?<br/>(parent canceled)"}
    cancel -->|yes| fail
    cancel -->|no| summarize["SummarizeFleet(goal, ordered results)"]
    summarize --> notifyDone["wfnotify: 'Fleet run complete'<br/>(per-node status, PR links, dev-step tokens)"]
    notifyDone --> done([return summary])

    fail([return error]) -.->|defer| failnote["wfnotify: 'Fleet run failed'"]
```

**Notes**

- **`WorktreesDir` is required.** An empty value would send every child down
  `DevelopWorkflow`'s in-place branch path against the same `WorkDir`, letting
  parallel children mutate one working tree. It is rejected before any work
  starts.
- **Layered execution.** `TopoLayers` groups nodes by longest dependency chain
  length. Every node in a layer runs concurrently as a child workflow; the next
  layer starts only once the current layer has fully settled, so dependents see
  their prerequisites' final status.
- **Skips propagate ordering, not code.** A node whose dependency failed or was
  skipped is marked `StatusSkipped` (running it would be pointless), while
  independent branches keep running.
- **Pinned base + dependency seeding.** `ResolveBase` captures repo HEAD once at
  the start; every child branch is cut from that same commit via `StartPoint`
  (stable even if the checkout moves), then seeded by merging in the branches of
  the node's dependencies (`DependencyBranches` → `DevelopInput.MergeBranches`),
  so a dependent is developed on top of its prerequisites' committed work.
- **Awaited review (Phase 1).** Each node runs with `AwaitReview = true`: the
  child returns only after its local review loop has converged, so a dependent
  starts once its prerequisites have been both developed **and** reviewed.
- **Cancellation.** After the layer loop, `ctx.Err()` is surfaced so a canceled
  run terminates as *canceled* rather than building a summary and completing.

---

## Child: `codereview.DevelopWorkflow` (per node)

Each fleet node reuses the existing develop pipeline rather than reimplementing
it. After creating the branch it optionally seeds it from `MergeBranches`, then
forks on mode: the fleet drives every node with **`AwaitReview`** (Phase 1);
the `default` and `WithRemote` modes are the standalone `code develop` paths.

```mermaid
flowchart TD
    start([DevelopInput: WorkDir, WorktreesDir, Branch, StartPoint=base,<br/>MergeBranches, Prompt, Summary, AwaitReview / WithRemote]) --> branch["CreateBranch<br/>(fresh worktree under WorktreesDir,<br/>cut from StartPoint; RetryPolicy: 5)"]
    branch --> seed{"MergeBranches?"}
    seed -->|no| devAgent
    seed -->|yes| merge["MergeDependency (per dependency branch)"]
    merge --> conflict{"Conflicted?"}
    conflict -->|no| seeded["base := post-merge HEAD"]
    conflict -->|yes| resolve["ResolveMergeConflict<br/>(agent; bounded retries)"]
    resolve -->|resolved| seeded
    resolve -->|exhausted| abort["AbortMerge<br/>(clean branch) → return blocked"]
    abort --> blocked([return: SeedConflictBlocked → node BLOCKED])
    seeded --> devAgent["RunDevelopAgent(prompt)<br/>long-running, heartbeats; RetryPolicy: 2"]
    devAgent --> ensure["EnsureDeveloped<br/>(HEAD advanced past base + clean tree)"]
    ensure --> devSummary["summarizeForWebhook (against quiescent tree)"]
    devSummary --> mode{"mode"}

    mode -->|"AwaitReview<br/>(fleet Phase 1)"| reviewAwait["ReviewWorkflow (awaited, no PR)"]
    reviewAwait --> retPhase1([return: developed branch, review converged])

    mode -->|default| reviewAbandon["ExecuteChildWorkflow ReviewWorkflow<br/>(ABANDON parent-close policy)"]
    reviewAbandon --> retLocal([return: developed branch, started review])

    mode -->|WithRemote| review["ReviewWorkflow (awaited)"]
    review --> openpr["OpenPRWorkflow<br/>(open PR + request Copilot review)"]
    openpr --> pilot["PilotWorkflow (Chain: true, awaited)<br/>loops until no unresolved comments"]
    pilot --> retRemote([return: branch, PR URL, pilot complete])
```

**Notes**

- **Seeding.** When `MergeBranches` is set (a dependent node), each dependency
  branch is merged into the fresh branch before the agent runs (`MergeDependency`
  per branch); the post-seed HEAD becomes `EnsureDeveloped`'s base — so that
  check verifies the *develop agent* (not the seeding merges) advanced the
  branch.
- **Seed conflicts.** A conflicting dependency merge is resolved by the agent
  (`ResolveMergeConflict`, bounded retries). If resolution is exhausted the
  merge is aborted (`AbortMerge`, leaving the branch clean — no conflict markers)
  and the workflow returns the non-retryable `SeedConflictBlocked` type, which
  the fleet records as the node being **blocked** (recoverable) rather than
  failed; its dependents are still gated.
- **`AwaitReview` (fleet Phase 1).** The child runs the local review loop as a
  supervised, awaited child and returns once it converges, without opening a PR
  or running the pilot. This is what lets the fleet gate a dependent on its
  prerequisites being both developed and reviewed.
- The **`default`** (abandoned review) and **`WithRemote`** (review → open PR →
  Copilot pilot) modes are the standalone `code develop` behaviours; the fleet
  does not use them for Phase 1. `ParseTokenTotal` extracts the develop-step
  token usage aggregated across nodes, and `SummarizeFleet` scans a child's
  summary (`extractPRURL`) for a PR link when the remote phase surfaces one.
