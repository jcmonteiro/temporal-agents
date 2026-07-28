package main

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
	"temporal-agents/internal/piagent"
	"temporal-agents/internal/wfnotify"
)

// TaskQueue is the single, default task queue used by everything.
const TaskQueue = "default"

// PromptRequest is the input to PromptWorkflow.
type PromptRequest struct {
	// Prompt is the instruction handed to the Pi agent.
	Prompt string
	// WorkDir is the directory the CLI was invoked from; the Pi agent runs there.
	WorkDir string
	// Chain, when true, immediately re-triggers the same workflow (via
	// continue-as-new) after each successful run, looping indefinitely.
	Chain bool
	// TokensSoFar carries the accumulated total token usage from prior runs of a
	// chained workflow, so a terminal run's result reports the whole chain's
	// usage.
	TokensSoFar int
}

// PromptWorkflow runs the Pi agent activity for the given prompt and returns its
// output. When req.Chain is set, a successful run continues as new with the
// same input, chaining the workflow indefinitely.
func PromptWorkflow(ctx workflow.Context, req PromptRequest) (out string, err error) {
	// Notify best-effort when the run fails. Continue-as-new is a control signal
	// (chained runs), not a failure, so NotifyFailureBestEffort excludes it.
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Run failed", err) }()

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		// The activity streams Pi's progress via heartbeats; if it stops
		// heartbeating for this long, Temporal treats it as failed.
		HeartbeatTimeout: time.Minute,
	})

	var res piagent.Result
	if err := workflow.ExecuteActivity(ctx, RunPiAgent, req).Get(ctx, &res); err != nil {
		return "", err
	}

	// Fold this run's usage into the running total carried across chained runs.
	total := req.TokensSoFar + res.Tokens

	if req.Chain {
		// Re-run the same workflow with the same input, carrying the accumulated
		// token usage forward. Continue-as-new keeps the event history bounded
		// across iterations.
		next := req
		next.TokensSoFar = total
		return "", workflow.NewContinueAsNewError(ctx, PromptWorkflow, next)
	}

	// A non-chained run has reached its terminal step: append the total token
	// usage to the result and notify best-effort. This runs only here (never
	// before continue-as-new, which would cancel the in-flight activity).
	result := res.Output + "\n\n" + piagent.FormatTokenTotal(total)
	wfnotify.NotifyBestEffort(ctx, notification.Notification{Title: "Run complete", Body: result})
	return result, nil
}
