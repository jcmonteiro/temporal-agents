package fleet

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/wfnotify"
	"temporal-agents/internal/wfrecord"
)

// FleetPlanInput is the input to FleetPlanWorkflow.
type FleetPlanInput struct {
	// Goal is the high-level change to decompose into a dependency graph.
	Goal string
	// WorkDir is the repository directory the planning agent inspects.
	WorkDir string
	// PlanID is the handle to store the produced plan under. The caller generates
	// it (and prints it), so the store write is deterministic under activity retry
	// and the operator knows the handle even if planning later fails.
	PlanID string
	// Name is an optional operator-chosen label for the stored plan. It is
	// display-only metadata, never a way to select a plan.
	Name string
}

// FleetPlanWorkflow drives the "fleet plan" step: it has the Pi agent decompose
// the goal into a dependency graph, stores the parsed, validated plan under its
// handle, and returns it for the user to review before executing it. It makes no
// code changes.
//
// The store is the plan's sole home (there is no plan file), so storing it is
// authoritative rather than best-effort: a plan that cannot be written fails the
// workflow instead of reporting a handle that resolves to nothing. Planning is
// also recorded as its own execution kind, so its status, timing and token cost
// appear in history separately from the `fleet execute` run it feeds.
func FleetPlanWorkflow(ctx workflow.Context, in FleetPlanInput) (plan FleetPlan, err error) {
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Fleet planning failed", err) }()

	rec, perr := startFleetPlanState(ctx, in)
	if perr != nil {
		return FleetPlan{}, perr
	}
	// Settle the record on every path out, including a cancellation. A failed
	// terminal write is reported and never changes planning's outcome: the plan
	// itself is already stored, so failing here would throw away a usable plan over
	// bookkeeping (see wfrecord.TerminalWriteFailed).
	defer func() {
		if perr := finishFleetPlanState(ctx, rec, err); perr != nil {
			wfrecord.TerminalWriteFailed(ctx, "fleet planning run", "stored plan "+rec.PlanID, err, perr)
		}
	}()

	// The planning agent is a long-running Pi step that streams heartbeats. It
	// runs once: a re-run would produce a different graph, so it is not retried.
	agentCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	var a *Activities
	var res GeneratePlanResult
	if err := workflow.ExecuteActivity(agentCtx, a.GeneratePlan,
		GeneratePlanRequest{Goal: in.Goal, WorkDir: in.WorkDir}).Get(agentCtx, &res); err != nil {
		return FleetPlan{}, err
	}
	plan = res.Plan
	rec.Tokens = res.Tokens
	rec.PlanNodes = len(plan.Nodes)

	// Store the plan before reporting success: the store is where `fleet execute`
	// will look for it, so a plan that was not written must not be announced as
	// ready. The write is a quick, idempotent upsert on the handle, so it is safe to
	// retry.
	//
	// A run started before plans moved into the store carries no handle, so there is
	// nothing to store it under; skipping the write also keeps such a run replayable
	// against this code, since its history has no store command. Every run started by
	// this CLI carries one.
	if in.PlanID != "" {
		storeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
		})
		if err := workflow.ExecuteActivity(storeCtx, a.StorePlan,
			StorePlanRequest{PlanID: in.PlanID, Name: in.Name, Plan: plan}).Get(storeCtx, nil); err != nil {
			return FleetPlan{}, fmt.Errorf("store the fleet plan: %w", err)
		}
	}

	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Fleet plan ready",
		Body: fmt.Sprintf("Planned %d node(s) for: %s\nStored as %s.\nPlanning token usage: %s tokens.",
			len(plan.Nodes), plan.Goal, in.PlanID, groupThousands(res.Tokens)),
	})
	return plan, nil
}

// FleetInput is the input to FleetWorkflow.
type FleetInput struct {
	// Plan is the approved dependency graph to orchestrate.
	Plan FleetPlan
	// WorkDir is the repository directory the fleet operates in.
	WorkDir string
	// WorktreesDir is the base directory under which each node develops in its
	// own git worktree. It is required so parallel nodes never share a working
	// tree: each child develop workflow gets an isolated worktree keyed by its
	// run-scoped branch (see NodeBranch).
	WorktreesDir string
	// Summary is propagated to each child develop workflow's --summary behavior.
	Summary bool
	// WithRemote is reserved for the future remote phase (Phase 2) and is not yet
	// wired into FleetWorkflow; nothing reads it today.
	WithRemote bool
	// PlanID is the handle of the stored plan being executed, recorded on the run so
	// a fleet execution can be traced back to the plan it came from.
	PlanID string
}

// FleetWorkflow orchestrates the approved plan's dependency graph. It processes
// the graph in dependency layers (see TopoLayers): every node in a layer runs
// concurrently as a child develop workflow, and the next layer only starts once
// the current one has settled. A node whose dependency did not succeed is
// skipped (running a node sequenced after a failed prerequisite is pointless),
// while independent branches of the graph keep running. It aggregates every
// node's outcome — status, develop-step token usage, and any PR link — into a
// single summary notification.
//
// Each node develops in its own git worktree (WorktreesDir) on a run-scoped
// branch (see NodeBranch), so concurrent nodes never contend for a working tree.
// Every branch is cut from a single base — the repository HEAD captured once
// when the run starts (ResolveBase) — and then seeded with the branches of the
// node's dependencies (DependencyBranches), so a dependent is developed on top
// of the committed, reviewed work of the slices it depends on rather than the
// bare base. Pinning the base once keeps a node's start point stable even if the
// user checks out, pulls, or merges while earlier layers run; the dependency
// graph then controls both *ordering* and what a node builds on.
//
// "Succeeded" means the child DevelopWorkflow returned successfully. Each node
// runs in the AwaitReview (Phase 1) mode: it develops its seeded branch and then
// waits for its local review loop to converge, so a dependent starts only after
// its prerequisites have been both developed and reviewed. A remote phase (open
// a PR per node and track it until merged) is planned for a later stage but is
// not yet implemented.
func FleetWorkflow(ctx workflow.Context, in FleetInput) (result string, err error) {
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Fleet run failed", err) }()

	// Record the orchestration run as started before anything else, so even a run
	// rejected up front (an invalid plan, a missing worktrees directory) leaves a
	// durable trace of having been attempted.
	rec, perr := startFleetState(ctx, in)
	if perr != nil {
		return "", perr
	}
	// Settle the record on every path out, including a cancellation. A failed
	// terminal write is reported and never changes the run's outcome: every node has
	// already done its work, and the record is bookkeeping (see
	// wfrecord.TerminalWriteFailed).
	defer func() {
		if perr := finishFleetState(ctx, rec, err); perr != nil {
			wfrecord.TerminalWriteFailed(ctx, "fleet run", result, err, perr)
		}
	}()

	// WorktreesDir is required (see FleetInput): every child develops in its own
	// worktree so concurrent nodes never share a working tree. An empty value
	// would send each child through DevelopWorkflow's in-place branch path against
	// the same WorkDir, letting parallel children switch and modify one working
	// tree. Reject it before resolving the base or starting any child.
	if in.WorktreesDir == "" {
		return "", temporal.NewNonRetryableApplicationError(
			"WorktreesDir is required so parallel nodes never share a working tree", errMissingWorktreesDir, nil)
	}

	layers, err := TopoLayers(in.Plan)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("invalid fleet plan: %v", err), errInvalidPlan, nil)
	}

	nodesByID := make(map[string]FleetNode, len(in.Plan.Nodes))
	for _, n := range in.Plan.Nodes {
		nodesByID[n.ID] = n
	}

	// Capture the repository base once, before any node starts, and pin every
	// child worktree to it (see the doc comment): the graph then controls only
	// ordering, never each node's start point.
	baseCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var a *Activities
	var base string
	if err := workflow.ExecuteActivity(baseCtx, a.ResolveBase,
		ResolveBaseRequest{WorkDir: in.WorkDir}).Get(baseCtx, &base); err != nil {
		return "", err
	}

	fleetID := workflow.GetInfo(ctx).WorkflowExecution.ID
	results := make(map[string]NodeResult, len(in.Plan.Nodes))
	// order records the sequence nodes settled in so the summary is deterministic
	// and reflects execution order.
	var order []string

	for _, layer := range layers {
		type pending struct {
			id  string
			fut workflow.ChildWorkflowFuture
		}
		var started []pending
		for _, id := range layer {
			node := nodesByID[id]
			if blocker := blockingDependency(node, results); blocker != "" {
				results[id] = NodeResult{
					ID:     id,
					Status: StatusSkipped,
					Detail: fmt.Sprintf("skipped: dependency %q did not succeed", blocker),
				}
				order = append(order, id)
				continue
			}
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID: fleetID + "-" + id,
			})
			fut := workflow.ExecuteChildWorkflow(childCtx, codereview.DevelopWorkflow, codereview.DevelopInput{
				WorkDir:       in.WorkDir,
				WorktreesDir:  in.WorktreesDir,
				Branch:        NodeBranch(fleetID, id),
				StartPoint:    base,
				MergeBranches: DependencyBranches(fleetID, node),
				Prompt:        node.Prompt,
				Summary:       in.Summary,
				// Phase 1: develop the node on a branch seeded from its dependencies'
				// branches, then wait for its local review loop to converge before a
				// dependent starts. The remote phase (open PR, pilot, track until
				// merged) is orchestrated separately once every node has been reviewed.
				AwaitReview: true,
			})
			started = append(started, pending{id: id, fut: fut})
		}
		// Wait for the whole layer to settle before advancing so dependents in the
		// next layer see their prerequisites' final status.
		for _, p := range started {
			var childOut string
			if cerr := p.fut.Get(ctx, &childOut); cerr != nil {
				// Some child failures are recoverable and leave the branch clean, so
				// record them as blocked (a distinct outcome) rather than failed: a seed
				// conflict the child could not resolve, and a local review loop that
				// stopped at the pass cap without converging (development landed, but
				// feedback is still outstanding). Its dependents are still gated either
				// way (see blockingDependency, which treats any non-succeeded status as
				// blocking).
				status := StatusFailed
				var appErr *temporal.ApplicationError
				if errors.As(cerr, &appErr) &&
					(appErr.Type() == codereview.SeedConflictBlockedErrType ||
						appErr.Type() == codereview.ReviewNotConvergedErrType) {
					status = StatusBlocked
				}
				results[p.id] = NodeResult{ID: p.id, Status: status, Detail: cerr.Error()}
			} else {
				results[p.id] = NodeResult{
					ID:     p.id,
					Status: StatusSucceeded,
					Detail: childOut,
					Tokens: ParseTokenTotal(childOut),
				}
			}
			order = append(order, p.id)
		}
	}

	ordered := make([]NodeResult, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, results[id])
	}
	// Hand the per-node breakdown to the record before the cancellation check below,
	// so a cancelled run still records what its nodes did. A skipped node lives here
	// and nowhere else: it starts no child workflow, so it has no row of its own.
	rec.Nodes = ordered

	// Preserve cancellation. When the parent workflow is canceled, each pending
	// ChildWorkflowFuture.Get returns a cancellation error that the loop above
	// records as a node failure; without this check the run would still build a
	// summary and return a nil error, so Temporal would record it as completed
	// rather than canceled. Surfacing ctx.Err() lets the run terminate as canceled.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	summary := SummarizeFleet(in.Plan.Goal, ordered)
	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Fleet run complete",
		Body:  summary,
	})
	return summary, nil
}

// blockingDependency returns the ID of the first dependency of node that did not
// succeed (failed or was skipped), or "" when every dependency succeeded. It
// reads the results accumulated from earlier layers; because layers are
// processed in dependency order, every dependency has already settled by the
// time its dependent is considered. Dependencies are checked in sorted order so
// the reported blocker is deterministic.
func blockingDependency(node FleetNode, results map[string]NodeResult) string {
	deps := append([]string{}, node.DependsOn...)
	sort.Strings(deps)
	for _, dep := range deps {
		if r, ok := results[dep]; !ok || r.Status != StatusSucceeded {
			return dep
		}
	}
	return ""
}
