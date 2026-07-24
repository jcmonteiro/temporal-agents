package main

import (
	"context"
	"fmt"
	"os/exec"
)

// RunPiAgent shells out to the Pi agent in non-interactive mode and returns stdout.
func RunPiAgent(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "pi", "-p", prompt, "--no-session")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pi failed: %w\n%s", err, out)
	}
	return string(out), nil
}
