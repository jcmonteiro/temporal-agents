package gitcli

import (
	"errors"
	"testing"

	"temporal-agents/internal/codereview"
)

// TestClassifyExists pins the load-bearing "already exists" substring match:
// git phrases both a colliding branch and a colliding worktree path with that
// substring, and only those must be wrapped as the non-retryable sentinel.
func TestClassifyExists(t *testing.T) {
	tests := []struct {
		name         string
		in           error
		wantSentinel bool
	}{
		{"nil", nil, false},
		{"branch exists", errors.New("git checkout -b: exit 128: fatal: a branch named 'x' already exists"), true},
		{"path exists", errors.New("git worktree add: exit 128: fatal: 'wt/x' already exists"), true},
		{"other failure", errors.New("git checkout -b: exit 128: fatal: not a git repository"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.Is(classifyExists(tt.in), codereview.ErrBranchOrWorktreeExists)
			if got != tt.wantSentinel {
				t.Fatalf("errors.Is = %v, want %v", got, tt.wantSentinel)
			}
		})
	}
}
