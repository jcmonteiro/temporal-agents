package main

import (
	"context"

	"temporal-agents/internal/piagent"
)

// RunPiAgent runs the Pi agent for req.Prompt in req.WorkDir. The heavy lifting
// (subprocess management, JSON event streaming, and heartbeating) lives in the
// piagent package so it can be shared across workflows. It returns the agent's
// final message alongside the session's total token usage.
func RunPiAgent(ctx context.Context, req PromptRequest) (piagent.Result, error) {
	return piagent.Run(ctx, req.Prompt, req.WorkDir)
}
