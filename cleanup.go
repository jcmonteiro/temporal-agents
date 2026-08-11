package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"temporal-agents/internal/cleanup"
	"temporal-agents/internal/gitcli"
)

// cleanupCmd runs the interactive cleanup of the worktrees temporal-agents
// created under the user config directory for CLI, hub, and fleet development.
func cleanupCmd(args []string) {
	if wantsHelp(args) {
		cleanupHelp(os.Stdout)
		return
	}
	if len(args) > 0 {
		fatalf("unexpected argument %q", args[0])
	}

	baseDir, err := worktreesDir()
	if err != nil {
		fatalf("Could not locate worktrees directory: %v", err)
	}

	cleaner := &cleanup.Cleaner{
		Git:    gitcli.New(),
		Prompt: stdinPrompter{in: bufio.NewReader(os.Stdin), out: os.Stdout},
		Out:    os.Stdout,
	}
	if _, err := cleaner.Run(context.Background(), cwd(), baseDir); err != nil {
		fatalf("%v", err)
	}
}

// stdinPrompter is the terminal-backed cleanup.Prompter: it prints the question
// with a [Y/n] or [y/N] hint and reads a single line from stdin.
type stdinPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// Confirm asks question and returns the user's yes/no answer. An empty line
// (just Enter) selects defaultYes; EOF is treated as accepting the default so a
// non-interactive stdin does not hang.
func (p stdinPrompter) Confirm(question string, defaultYes bool) (bool, error) {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(p.out, "%s %s ", question, hint)
		line, err := p.in.ReadString('\n')
		// A real terminal I/O failure must not be reported as a successful
		// answer; only EOF is a benign signal (non-interactive stdin) that
		// selects the default below.
		if err != nil && err != io.EOF {
			return false, fmt.Errorf("read answer: %w", err)
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		case "":
			if err == io.EOF {
				fmt.Fprintln(p.out)
			}
			return defaultYes, nil
		}
		if err == io.EOF {
			return defaultYes, nil
		}
		fmt.Fprintln(p.out, `Please answer "y" or "n".`)
	}
}

func cleanupHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents cleanup — remove managed development worktrees

Loops through every git worktree temporal-agents created under your user config
directory (<config>/temporal-agents/worktrees) — from CLI, hub, and fleet
development — and, for each one, asks before
deleting it. Before a delete it checks whether the branch is merged into the
current repository HEAD; if it is not, it asks again whether to delete by force
(defaulting to no). The worktree directory and its branch are both removed.

Run it from the repository the worktrees were branched from, with the branch
you merge into (e.g. main) checked out and up to date, so the merge check is
accurate.

USAGE
  temporal-agents cleanup

EXAMPLES
  temporal-agents cleanup
`)
}
