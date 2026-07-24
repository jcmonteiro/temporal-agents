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
	if len(args) == 0 || wantsHelp(args) {
		codeHelp(os.Stdout)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}

	switch args[0] {
	case "pilot":
		if wantsHelp(args[1:]) {
			pilotHelp(os.Stdout)
			return
		}
		mode, text := parsePilotFlags(args[1:])
		startPilot(mode, text)
	default:
		fatalf("unknown code subcommand %q (try: pilot)", args[0])
	}
}

// parsePilotFlags reads the optional, mutually exclusive --append/--replace
// flags (each in "--flag value" or "--flag=value" form) and returns the prompt
// mode plus its text.
func parsePilotFlags(args []string) (codereview.PromptMode, string) {
	var mode codereview.PromptMode
	var text string
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
	return mode, text
}

// startPilot launches the PilotWorkflow for the current repository.
func startPilot(mode codereview.PromptMode, text string) {
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
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

func codeHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents code — agent workflows for the current repository

USAGE
  temporal-agents code pilot [--append <prompt> | --replace <prompt>]

SUBCOMMANDS
  pilot   Address the unresolved review comments on the current branch's PR

See "temporal-agents code pilot --help".
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

The two flags are mutually exclusive. The unresolved comments are always
appended to whichever prompt is used.

EXAMPLES
  temporal-agents code pilot
  temporal-agents code pilot --append "prefer table-driven tests"
  temporal-agents code pilot --replace "only fix the comments about naming"
`)
}
