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
	if err := workflow.ExecuteActivity(agentCtx, a.GeneratePlan,
		GeneratePlanRequest{Goal: in.Goal, WorkDir: in.WorkDir}).Get(agentCtx, &plan); err != nil {
		return FleetPlan{}, err
	}

	wfnotify.NotifyBestEffort(ctx, notification.Notification{
		Title: "Fleet plan ready",
		Body:  fmt.Sprintf("Planned %d node(s) for: %s", len(plan.Nodes), plan.Goal),
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
// skipped (building on top of a missing foundation is meaningless), while
// independent branches of the graph keep running. It aggregates every node's
// outcome — status, token usage, and any PR link — into a single summary
// notification.
//
// Each node develops in its own git worktree (WorktreesDir) on an
// auto-generated branch, so concurrent nodes never contend for a working tree.
// The graph therefore controls execution *ordering*: a dependent node starts
// only after the features it builds upon have landed, which is the coordination
// an approved plan prescribes.
func FleetWorkflow(ctx workflow.Context, in FleetInput) (result string, err error) {
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Fleet run failed", err) }()

	layers, err := TopoLayers(in.Plan)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("invalid fleet plan: %v", err), errInvalidPlan, nil)
	}

	nodesByID := make(map[string]FleetNode, len(in.Plan.Nodes))
	for _, n := range in.Plan.Nodes {
		nodesByID[n.ID] = n
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
