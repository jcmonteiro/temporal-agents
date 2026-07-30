package gitcli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/cleanup"
)

// TestParseWorktrees pins the porcelain parsing and baseDir filtering: only
// branch-backed worktrees nested under baseDir are returned, while the main
// checkout, detached entries, and unrelated worktrees are dropped.
func TestParseWorktrees(t *testing.T) {
	out := "" +
		"worktree /home/me/repo\n" +
		"HEAD 1111111111111111111111111111111111111111\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /cfg/temporal-agents/worktrees/feat/x\n" +
		"HEAD 2222222222222222222222222222222222222222\n" +
		"branch refs/heads/feat/x\n" +
		"\n" +
		"worktree /cfg/temporal-agents/worktrees/flaming-duck\n" +
		"HEAD 3333333333333333333333333333333333333333\n" +
		"branch refs/heads/flaming-duck\n" +
		"\n" +
		"worktree /cfg/temporal-agents/worktrees/detached\n" +
		"HEAD 4444444444444444444444444444444444444444\n" +
		"detached\n"

	got := parseWorktrees(out, "/cfg/temporal-agents/worktrees")

	require.Equal(t, []cleanup.Worktree{
		{Path: "/cfg/temporal-agents/worktrees/feat/x", Branch: "feat/x"},
		{Path: "/cfg/temporal-agents/worktrees/flaming-duck", Branch: "flaming-duck"},
	}, got)
}

func TestUnderDir(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		baseDir string
		want    bool
	}{
		{"nested", "/wt/feat/x", "/wt", true},
		{"nested with trailing slash base", "/wt/feat/x", "/wt/", true},
		{"sibling prefix is not under", "/wt-other/x", "/wt", false},
		{"outside", "/somewhere/else", "/wt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, underDir(tt.path, tt.baseDir))
		})
	}
}
