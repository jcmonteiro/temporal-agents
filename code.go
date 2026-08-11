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
	"temporal-agents/internal/setting"
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
		mode, text, chain, summary, steering := parsePilotFlags(args[1:])
		startPilot(mode, text, chain, summary, steering)
	case "review":
		if wantsHelp(args[1:]) {
			reviewHelp(os.Stdout)
			return
		}
		summary, steering := parseReviewFlags(args[1:])
		startReview(summary, steering)
	case "develop":
		if wantsHelp(args[1:]) {
			developHelp(os.Stdout)
			return
		}
		prompt, branch, worktree, summary, withRemote, steering := parseDevelopFlags(args[1:])
		startDevelop(prompt, branch, worktree, summary, withRemote, steering)
	default:
		fatalf("unknown code subcommand %q (try: pilot, review, develop)", args[0])
	}
}

// parseReviewFlags reads the review command's --summary and --no-steering flags
// and rejects any other argument.
func parseReviewFlags(args []string) (summary, steering bool) {
	steering = true
	for _, a := range args {
		switch a {
		case "--summary":
			summary = true
		case "--no-steering":
			steering = false
		default:
			fatalf("unexpected argument %q", a)
		}
	}
	return summary, steering
}

// startReview launches the ReviewWorkflow for the current repository. It starts
// with no payload, so the first pass only reviews; actionable items drive
// subsequent implement-then-review passes via continue-as-new.
func startReview(summary, steering bool) {
	c := dial()
	defer c.Close()

	id := "review-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.ReviewWorkflow, codereview.ReviewInput{
		WorkDir:  cwd(),
		Summary:  summary,
		Settings: cliSteeringSettings(steering),
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
	if !steering {
		fmt.Printf("  steering: off (review feedback is handled autonomously)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// parseDevelopFlags reads the develop command's arguments: a required prompt
// (positional), an optional branch name (--branch <name> or --branch=<name>;
// defaults to a generated alias when omitted), and the optional --worktree,
// --summary, --with-remote and --no-steering flags.
func parseDevelopFlags(args []string) (prompt, branch string, worktree, summary, withRemote, steering bool) {
	steering = true
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
		case a == "--worktree":
			worktree = true
		case a == "--no-steering":
			steering = false
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
	if err := codereview.ValidateBranchName(branch); err != nil {
		fatalf("invalid branch name: %v", err)
	}
	return prompt, branch, worktree, summary, withRemote, steering
}

// startDevelop launches the DevelopWorkflow for the current repository. When
// worktree is set the workflow develops in a fresh git worktree created under
// the user config directory instead of switching the branch in the current
// working directory.
func startDevelop(prompt, branch string, worktree, summary, withRemote, steering bool) {
	c := dial()
	defer c.Close()

	var wtDir string
	if worktree {
		d, err := worktreesDir()
		if err != nil {
			fatalf("Could not locate worktrees directory: %v", err)
		}
		wtDir = d
	}

	id := "develop-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, codereview.DevelopWorkflow, codereview.DevelopInput{
		WorkDir:      cwd(),
		Branch:       branch,
		WorktreesDir: wtDir,
		Prompt:       prompt,
		Summary:      summary,
		WithRemote:   withRemote,
		Settings:     cliSteeringSettings(steering),
	})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	// The branch name is resolved by the workflow when omitted; report it as
	// auto-generated rather than printing an empty value.
	branchLabel := branch
	if branchLabel == "" {
		branchLabel = "(auto-generated)"
	}

	fmt.Println("Develop started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  branch:  %s\n", branchLabel)
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	if worktree {
		fmt.Printf("  workdir: %s (a new worktree is created here)\n", wtDir)
	} else {
		fmt.Printf("  workdir: %s\n", cwd())
	}
	if summary {
		fmt.Printf("  summary: on (webhook message summarizes the last Pi run)\n")
	}
	if withRemote {
		fmt.Printf("  remote:  on (after review, open the PR + Copilot, then run the pilot loop)\n")
	}
	if !steering {
		fmt.Printf("  steering: off (review feedback is handled autonomously)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// parsePilotFlags reads the optional, mutually exclusive --append/--replace
// flags (each in "--flag value" or "--flag=value" form) and returns the prompt
// mode plus its text, along with the --summary toggle. Chaining and steering are
// on by default and are disabled with --no-chain and --no-steering.
func parsePilotFlags(args []string) (mode codereview.PromptMode, text string, chain, summary, steering bool) {
	chain = true
	steering = true
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
		case a == "--no-chain":
			chain = false
		case a == "--summary":
			summary = true
		case a == "--no-steering":
			steering = false
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
	return mode, text, chain, summary, steering
}

// startPilot launches the PilotWorkflow for the current repository. Chaining is
// on by default: a pass that addresses comments continues as new, looping until
// a pass finds no unresolved comments left. --no-chain opts out, running a
// single pass. (The develop --with-remote pipeline sets Chain directly,
// independent of this standalone flag.)
func startPilot(mode codereview.PromptMode, text string, chain, summary, steering bool) {
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
		Summary:    summary,
		Settings:   cliSteeringSettings(steering),
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
		fmt.Printf("  chain:   on (loops until no unresolved comments remain)\n")
	} else {
		fmt.Printf("  chain:   off (single pass)\n")
	}
	if summary {
		fmt.Printf("  summary: on (webhook message summarizes the last Pi run)\n")
	}
	if !steering {
		fmt.Printf("  steering: off (review feedback is handled autonomously)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

// cliSteeringSettings leaves the scoped default in control unless the operator
// explicitly requests autonomous execution for this run.
func cliSteeringSettings(enabled bool) setting.Resolution {
	if enabled {
		return nil
	}
	return setting.Resolution{{Key: setting.KeySteeringEnabled, Enabled: false}}
}

func codeHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code — agent workflows for the current repository

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>]
  temporal-agents code review
  temporal-agents code develop "<prompt>" [--branch <name>] [--worktree]

SUBCOMMANDS
  pilot    Address the unresolved review comments on the current branch's PR
  review   Review the current branch locally, then implement + re-review in a loop
  develop  Create a branch, implement a prompt, then start a local review loop

All subcommands enable steering by default: each review round waits for operator
input before an agent acts. Use --no-steering for autonomous execution.

All subcommands accept --summary, which summarizes a Pi execution and sends
that summary as the webhook notification's body (only the webhook). For review
and develop this runs once before returning (on success or failure). For a
chained pilot (the default) it summarizes each addressing pass, so a multi-pass
loop sends one summary per pass that addresses comments, not a single summary at
the end; a --no-chain single-pass pilot summarizes that one pass.

See "temporal-agents code pilot --help", "temporal-agents code review --help",
and "temporal-agents code develop --help".
`)
}

func developHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code develop — implement a prompt on a fresh branch

Runs a workflow that develops a change end to end on the current machine:

  - Creates the branch to develop on off the current HEAD. Without --branch the
    name is auto-generated as <adjective>-<animal>-<date> (e.g.
    flaming-duck-2026-jul-29). By default the branch is checked out in the
    current working tree, which must be clean; commit or stash local changes
    first. With --worktree the branch is created in a fresh git worktree under
    your user config directory instead, so the current working tree is left
    untouched and need not be clean.
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
  temporal-agents code develop "<prompt>" [--branch <name>] [--worktree] [--summary] [--with-remote] [--no-steering]

FLAGS
  --branch <name>   Name of the new branch to create and develop on. Optional;
                    defaults to a generated <adjective>-<animal>-<date> alias
                    (e.g. flaming-duck-2026-jul-29).
  --worktree        Develop in a fresh git worktree created under your user
                    config directory (<config>/temporal-agents/worktrees/<branch>)
                    instead of switching the branch in the current directory, so
                    the current working tree is left untouched.
  --summary         Before returning (on success or failure), summarize the last
                    Pi execution and send it as the webhook message (only the
                    webhook). Also propagated to the review loop this starts, so
                    a plain "develop --summary" runs two billable summaries
                    (develop and review). With --with-remote it is also
                    propagated to the pilot loop, which summarizes on each pass
                    that addresses comments, for at least three (develop,
                    review, and one per pilot pass).
  --with-remote     After development and review, open the PR and request a
                    Copilot review, then run the pilot loop—this workflow
                    supervises each stage and stays alive until the pilot loop
                    finishes.
  --no-steering     Do not wait for operator input before acting on local or
                    Copilot review feedback.

EXAMPLES
  temporal-agents code develop "add a rate limiter to the API client" --branch feat/rate-limit
  temporal-agents code develop "add a rate limiter"
  temporal-agents code develop "add a rate limiter" --worktree
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

By default, each review round waits for operator input before the next implement
pass. Use --no-steering for autonomous execution.

USAGE
  temporal-agents code review [--summary] [--no-steering]

FLAGS
  --summary       Before returning (on success or failure), summarize the last Pi
                  execution and send it as the webhook message (only the webhook)
  --no-steering   Do not wait for operator input before acting on review feedback

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

By default a pilot keeps looping: after a pass that addresses comments it
continues, folding in the fresh Copilot review, until a pass finds no unresolved
comments left. With --no-chain it runs a single pass instead. Each pass waits for
operator input before acting on Copilot feedback unless --no-steering is set.

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>] [--no-chain] [--summary] [--no-steering]

FLAGS
  --append <prompt>   Append extra instructions to the default prompt
  --replace <prompt>  Replace the default prompt entirely
  --no-chain          Run a single pass rather than looping until no unresolved
                      comments remain
  --no-steering       Do not wait for operator input before acting on Copilot
                      review feedback
  --summary           Summarize the agent's work and send it as the webhook
                      message (only the webhook). When chaining (the default)
                      this runs on each pass that addresses comments (and on a
                      failure after the agent has run), not once at the end—the
                      terminal no-comments pass has no agent run to summarize.
                      With --no-chain it runs once for the single pass.

The --append and --replace flags are mutually exclusive. The unresolved
comments are always appended to whichever prompt is used.

EXAMPLES
  temporal-agents code pilot
  temporal-agents code pilot --no-chain
  temporal-agents code pilot --append "prefer table-driven tests"
  temporal-agents code pilot --replace "only fix the comments about naming"
  temporal-agents code pilot --summary
`)
}
