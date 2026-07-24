package main

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

// TaskQueue is the single, default task queue used by everything.
const TaskQueue = "default"

// PromptWorkflow takes a prompt, runs the Pi agent activity, and returns its output.
func PromptWorkflow(ctx workflow.Context, prompt string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
	})

	var result string
	err := workflow.ExecuteActivity(ctx, RunPiAgent, prompt).Get(ctx, &result)
	return result, err
}
