package main

import (
	"context"
	"fmt"
	"os/exec"
)

// RunPiAgent shells out to the Pi agent in non-interactive mode and returns stdout.
// The agent runs in req.WorkDir (the directory the CLI was invoked from).
func RunPiAgent(ctx context.Context, req PromptRequest) (string, error) {
	cmd := exec.CommandContext(ctx, "pi", "-p", req.Prompt, "--no-session")
	cmd.Dir = req.WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pi failed: %w\n%s", err, out)
	}
	return string(out), nil
}
