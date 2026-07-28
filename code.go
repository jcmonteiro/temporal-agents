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
		mode, text, summary := parsePilotFlags(args[1:])
		startPilot(mode, text, summary)
	case "review":
		if wantsHelp(args[1:]) {
			reviewHelp(os.Stdout)
			return
		}
		startReview(parseReviewFlags(args[1:]))
	case "develop":
		if wantsHelp(args[1:]) {
			developHelp(os.Stdout)
			return
		}
		prompt, branch, summary, withRemote := parseDevelopFlags(args[1:])
		startDevelop(prompt, branch, summary, withRemote)
	default:
		fatalf("unknown code subcommand %q (try: pilot, review, develop)", args[0])
	}
}

// parseReviewFlags reads the review command's only flag, --summary, and rejects
// any other argument.
func parseReviewFlags(args []string) (summary bool) {
	for _, a := range args {
		if a == "--summary" {
			summary = true
			continue
		}
		fatalf("unexpected argument %q", a)
	}
	return summary
}

// startReview launches the ReviewWorkflow for the current repository. It starts
// with no payload, so the first pass only reviews; actionable items drive
// subsequent implement-then-review passes via continue-as-new.
func startReview(summary bool) {
	c := dial()
	defer c.Close()

	id := "review-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.ReviewWorkflow, codereview.ReviewInput{
		WorkDir: cwd(),
		Summary: summary,
	})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Review started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  workdir: %s\n", cwd())
	if summary {
		fmt.Printf("  summary: on (webhook message summarizes the last Pi run)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// parseDevelopFlags reads the develop command's arguments: a required prompt
// (positional), a required branch name (--branch <name> or --branch=<name>),
// and the optional --summary and --with-remote flags.
func parseDevelopFlags(args []string) (prompt, branch string, summary, withRemote bool) {
	setPrompt := func(v string) {
		if prompt != "" {
			fatalf("unexpected argument %q", v)
		}
		prompt = v
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--summary":
			summary = true
		case a == "--with-remote":
			withRemote = true
		case a == "--branch":
			if i+1 >= len(args) {
				fatalf("--branch requires a branch name")
			}
			branch = args[i+1]
			i++
		case strings.HasPrefix(a, "--branch="):
			branch = strings.TrimPrefix(a, "--branch=")
		default:
			setPrompt(a)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		fatalf("develop requires a prompt")
	}
	if strings.TrimSpace(branch) == "" {
		fatalf("develop requires a branch name (--branch <name>)")
	}
	return prompt, branch, summary, withRemote
}

// startDevelop launches the DevelopWorkflow for the current repository.
func startDevelop(prompt, branch string, summary, withRemote bool) {
	c := dial()
	defer c.Close()

	id := "develop-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.DevelopWorkflow, codereview.DevelopInput{
		WorkDir:    cwd(),
		Branch:     branch,
		Prompt:     prompt,
		Summary:    summary,
		WithRemote: withRemote,
	})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Develop started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  branch:  %s\n", branch)
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
	if summary {
		fmt.Printf("  summary: on (webhook message summarizes the last Pi run)\n")
	}
	if withRemote {
		fmt.Printf("  remote:  on (after review, open the PR + Copilot, then run the pilot loop)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// parsePilotFlags reads the optional, mutually exclusive --append/--replace
// flags (each in "--flag value" or "--flag=value" form) and returns the prompt
// mode plus its text, along with the --summary toggle.
func parsePilotFlags(args []string) (mode codereview.PromptMode, text string, summary bool) {
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
		case a == "--summary":
			summary = true
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
	return mode, text, summary
}

// startPilot launches the PilotWorkflow for the current repository. The pilot
// loop always chains: after a pass that addresses comments it continues as new,
// looping until a pass finds no unresolved comments left.
func startPilot(mode codereview.PromptMode, text string, summary bool) {
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
		Chain:      true,
		Summary:    summary,
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
	fmt.Printf("  chain:   on (loops until no unresolved comments remain)\n")
	if summary {
		fmt.Printf("  summary: on (webhook message summarizes the last Pi run)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

func codeHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code — agent workflows for the current repository

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>]
  temporal-agents code review
  temporal-agents code develop "<prompt>" --branch <name>

SUBCOMMANDS
  pilot    Address the unresolved review comments on the current branch's PR
  review   Review the current branch locally, then implement + re-review in a loop
  develop  Create a branch, implement a prompt, then start a local review loop

All subcommands accept --summary, which runs a final Pi step before returning
(on success or failure) that summarizes the last Pi execution and sends that
summary as the webhook notification's body (only the webhook).

See "temporal-agents code pilot --help", "temporal-agents code review --help",
and "temporal-agents code develop --help".
`)
}

func developHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code develop — implement a prompt on a fresh branch

Runs a workflow that develops a change end to end on the current machine:

  - Creates the requested branch off the current HEAD. This requires a clean
    working tree; commit or stash local changes first.
  - Runs a Pi agent to implement your prompt and commit its work.
  - Confirms the agent advanced HEAD and left no uncommitted changes.
  - Triggers the local review loop (the same one as "code review") on the new
    branch, which keeps running after this command returns.

With --with-remote it goes further, and the workflow instead stays alive to
oversee the whole pipeline end to end. After development it supervises, in
order and waiting for each to finish: the local review loop, then a workflow
that opens the pull request and requests a Copilot review (succeeding if the PR
or the Copilot request already exists), then the pilot loop (the same one as
"code pilot", which loops until Copilot has no unresolved comments left).

USAGE
  temporal-agents code develop "<prompt>" --branch <name> [--summary] [--with-remote]

FLAGS
  --branch <name>   Name of the new branch to create and develop on (required)
  --summary         Before returning (on success or failure), summarize the last
                    Pi execution and send it as the webhook message (only the
                    webhook). Also propagated to the review loop this starts, so
                    a plain "develop --summary" runs two billable summaries
                    (develop and review). With --with-remote it is also
                    propagated to the pilot loop, for three (develop, review,
                    and pilot).
  --with-remote     After development and review, open the PR and request a
                    Copilot review, then run the pilot loop—this workflow
                    supervises each stage and stays alive until the pilot loop
                    finishes.

EXAMPLES
  temporal-agents code develop "add a rate limiter to the API client" --branch feat/rate-limit
  temporal-agents code develop "add a rate limiter" --branch feat/rate-limit --with-remote
  temporal-agents watch <workflow-id>
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
  temporal-agents code review [--summary]

FLAGS
  --summary   Before returning (on success or failure), summarize the last Pi
              execution and send it as the webhook message (only the webhook)

EXAMPLES
  temporal-agents code review
  temporal-agents code review --summary
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

The loop always chains: after a pass that addresses comments it continues,
folding in the fresh Copilot review, until a pass finds no unresolved comments
left.

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>] [--summary]

FLAGS
  --append <prompt>   Append extra instructions to the default prompt
  --replace <prompt>  Replace the default prompt entirely
  --summary           Before returning (on success or failure), summarize the
                      last Pi execution and send it as the webhook message (only
                      the webhook)

The --append and --replace flags are mutually exclusive. The unresolved
comments are always appended to whichever prompt is used.

EXAMPLES
  temporal-agents code pilot
  temporal-agents code pilot --append "prefer table-driven tests"
  temporal-agents code pilot --replace "only fix the comments about naming"
  temporal-agents code pilot --summary
`)
}
