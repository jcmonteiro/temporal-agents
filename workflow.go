package main

import (
	"time"

	"go.temporal.io/sdk/workflow"
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
}

// PromptWorkflow runs the Pi agent activity for the given prompt and returns its
// output. When req.Chain is set, a successful run continues as new with the
// same input, chaining the workflow indefinitely.
func PromptWorkflow(ctx workflow.Context, req PromptRequest) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		// The activity streams Pi's progress via heartbeats; if it stops
		// heartbeating for this long, Temporal treats it as failed.
		HeartbeatTimeout: time.Minute,
	})

	var result string
	if err := workflow.ExecuteActivity(ctx, RunPiAgent, req).Get(ctx, &result); err != nil {
		return "", err
	}

	if req.Chain {
		// Re-run the same workflow with the same input. Continue-as-new keeps
		// the event history bounded across iterations.
		return "", workflow.NewContinueAsNewError(ctx, PromptWorkflow, req)
	}
	return result, nil
}
