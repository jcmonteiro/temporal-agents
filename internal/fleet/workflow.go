package fleet

import (
	"fmt"
	"sort"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/wfnotify"
)

// FleetPlanInput is the input to FleetPlanWorkflow.
type FleetPlanInput struct {
	// Goal is the high-level change to decompose into a dependency graph.
	Goal string
	// WorkDir is the repository directory the planning agent inspects.
	WorkDir string
}

// FleetPlanWorkflow drives the "fleet plan" step: it has the Pi agent decompose
// the goal into a dependency graph and returns the parsed, validated plan for
// the user to review and approve before executing it. It makes no code changes.
func FleetPlanWorkflow(ctx workflow.Context, in FleetPlanInput) (plan FleetPlan, err error) {
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Fleet planning failed", err) }()

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

	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Fleet plan ready",
		Body: fmt.Sprintf("Planned %d node(s) for: %s\nPlanning token usage: %s tokens.",
			len(plan.Nodes), plan.Goal, groupThousands(res.Tokens)),
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
	// auto-generated branch.
	WorktreesDir string
	// Summary is propagated to each child develop workflow's --summary behavior.
	Summary bool
	// WithRemote is propagated to each child develop workflow: when true a node
	// runs the full remote pipeline (review, PR, Copilot pilot) and the fleet
	// waits for that whole pipeline before a dependent node starts.
	WithRemote bool
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
// Each node develops in its own git worktree (WorktreesDir) on an
// auto-generated branch cut from the repository base, so concurrent nodes never
// contend for a working tree. The base is the repository HEAD captured once when
// the run starts (ResolveBase) and passed to every child as an explicit
// worktree start point, so a node started in a later layer branches from the
// same commit as the first even if the user checks out, pulls, or merges while
// earlier layers run. The graph therefore controls execution *ordering*, not
// code layering: a dependent node starts only after the nodes it depends on have
// succeeded, but it develops from the pinned base without their commits.
// Ordering is the coordination an approved plan prescribes.
//
// "Succeeded" means the child DevelopWorkflow returned successfully. In the
// default mode that is once the develop step landed its commits and the review
// loop was *started* (an abandoned child that keeps running afterwards), so the
// fleet releases dependents after the develop step, not after review converges.
// Pass WithRemote when a dependent should wait for the full review+PR+pilot
// pipeline to complete before it starts.
func FleetWorkflow(ctx workflow.Context, in FleetInput) (result string, err error) {
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Fleet run failed", err) }()

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
				WorkDir:      in.WorkDir,
				WorktreesDir: in.WorktreesDir,
				StartPoint:   base,
				Prompt:       node.Prompt,
				Summary:      in.Summary,
				WithRemote:   in.WithRemote,
			})
			started = append(started, pending{id: id, fut: fut})
		}
		// Wait for the whole layer to settle before advancing so dependents in the
		// next layer see their prerequisites' final status.
		for _, p := range started {
			var childOut string
			if cerr := p.fut.Get(ctx, &childOut); cerr != nil {
				results[p.id] = NodeResult{ID: p.id, Status: StatusFailed, Detail: cerr.Error()}
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

	// Preserve cancellation. When the parent workflow is canceled, each pending
	// ChildWorkflowFuture.Get returns a cancellation error that the loop above
	// records as a node failure; without this check the run would still build a
	// summary and return a nil error, so Temporal would record it as completed
	// rather than canceled. Surfacing ctx.Err() lets the run terminate as canceled.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	ordered := make([]NodeResult, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, results[id])
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
