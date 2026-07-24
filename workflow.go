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
}

// PromptWorkflow runs the Pi agent activity for the given prompt and returns its output.
func PromptWorkflow(ctx workflow.Context, req PromptRequest) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
	})

	var result string
	err := workflow.ExecuteActivity(ctx, RunPiAgent, req).Get(ctx, &result)
	return result, err
}
