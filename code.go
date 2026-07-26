package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/codereview"
)

// codeCmd dispatches the "code" subcommands.
func codeCmd(args []string) {
	if len(args) == 0 {
		codeHelp(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		codeHelp(os.Stdout)
	case "pilot":
		if wantsHelp(args[1:]) {
			pilotHelp(os.Stdout)
			return
		}
		mode, text, chain := parsePilotFlags(args[1:])
		startPilot(mode, text, chain)
	case "review":
		if wantsHelp(args[1:]) {
			reviewHelp(os.Stdout)
			return
		}
		if len(args) > 1 {
			fatalf("unexpected argument %q", args[1])
		}
		startReview()
	default:
		fatalf("unknown code subcommand %q (try: pilot, review)", args[0])
	}
}

// startReview launches the ReviewWorkflow for the current repository. It starts
// with no payload, so the first pass only reviews; actionable items drive
// subsequent implement-then-review passes via continue-as-new.
func startReview() {
	c := dial()
	defer c.Close()

	id := "review-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.ReviewWorkflow, codereview.ReviewInput{
		WorkDir: cwd(),
	})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Review started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  workdir: %s\n", cwd())
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// parsePilotFlags reads the optional, mutually exclusive --append/--replace
// flags (each in "--flag value" or "--flag=value" form) and returns the prompt
// mode plus its text.
func parsePilotFlags(args []string) (mode codereview.PromptMode, text string, chain bool) {
	set := func(m codereview.PromptMode, v string) {
		if mode != codereview.PromptDefault {
			fatalf("--append and --replace are mutually exclusive")
		}
		if strings.TrimSpace(v) == "" {
			fatalf("--%s requires a prompt", m)
		}
		mode, text = m, v
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--chain":
			chain = true
		case a == "--append", a == "--replace":
			if i+1 >= len(args) {
				fatalf("%s requires a prompt", a)
			}
			set(codereview.PromptMode(strings.TrimPrefix(a, "--")), args[i+1])
			i++
		case strings.HasPrefix(a, "--append="):
			set(codereview.PromptAppend, strings.TrimPrefix(a, "--append="))
		case strings.HasPrefix(a, "--replace="):
			set(codereview.PromptReplace, strings.TrimPrefix(a, "--replace="))
		default:
			fatalf("unexpected argument %q", a)
		}
	}
	return mode, text, chain
}

// startPilot launches the PilotWorkflow for the current repository.
func startPilot(mode codereview.PromptMode, text string, chain bool) {
	c := dial()
	defer c.Close()

	id := "pilot-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.PilotWorkflow, codereview.PilotInput{
		WorkDir:    cwd(),
		PromptMode: mode,
		PromptText: text,
		Chain:      chain,
	})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Pilot started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  workdir: %s\n", cwd())
	if mode != codereview.PromptDefault {
		fmt.Printf("  prompt:  %s (%s)\n", mode, truncate(text, 50))
	}
	if chain {
		fmt.Printf("  chain:   on (spawns a delayed child run after each success)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

func codeHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code — agent workflows for the current repository

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>]

SUBCOMMANDS
  pilot   Address the unresolved review comments on the current branch's PR
  review  Review the current branch locally, then implement + re-review in a loop

See "temporal-agents code pilot --help" and "temporal-agents code review --help".
`)
}

func reviewHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code review — review the current branch locally in a loop

Runs a Pi agent to review the current branch on this machine (no GitHub or
Copilot involved). The review's raw output is carried into the next pass, where:

  - A Pi agent implements that feedback (checking the git HEAD before and after
    to confirm the change landed) and then reviews the branch again.
  - When an implement pass makes no commits, there was nothing left to change
    and the workflow finishes successfully. It also stops after a bounded number
    of passes.

In other words: with a payload it implements + reviews; without one it just
reviews.

USAGE
  temporal-agents code review

EXAMPLES
  temporal-agents code review
  temporal-agents watch <workflow-id>
`)
}

func pilotHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code pilot — address reviewer feedback on the open PR

Finds the single open pull request for the current branch and runs the Pi agent
over its unresolved review comments. When the agent commits its work, each
comment is answered with the resulting commit hashes and resolved, then a fresh
Copilot review is requested. If there are no unresolved comments, it exits
successfully without changing anything.

The agent works from a built-in default prompt. Use the flags to adjust it:

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>]

FLAGS
  --append <prompt>   Append extra instructions to the default prompt
  --replace <prompt>  Replace the default prompt entirely
  --chain             After a successful pass, spawn a child run that starts
                      3 minutes later, looping to fold in new feedback

The --append and --replace flags are mutually exclusive. The unresolved
comments are always appended to whichever prompt is used.

EXAMPLES
  temporal-agents code pilot
  temporal-agents code pilot --append "prefer table-driven tests"
  temporal-agents code pilot --replace "only fix the comments about naming"
  temporal-agents code pilot --chain
`)
}
