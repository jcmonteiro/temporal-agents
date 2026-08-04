package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/fleet"
)

// defaultPlanFile is where `fleet plan` writes the generated plan and where
// `fleet execute` reads it from when --plan is not given. It lives in the
// current directory so the user can review and edit it between the two steps.
const defaultPlanFile = "fleet-plan.json"

// fleetCmd dispatches the "fleet" subcommands.
func fleetCmd(args []string) {
	if len(args) == 0 {
		fleetHelp(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		fleetHelp(os.Stdout)
	case "plan":
		if wantsHelp(args[1:]) {
			fleetPlanHelp(os.Stdout)
			return
		}
		prompt, out := parseFleetPlanFlags(args[1:])
		runFleetPlan(prompt, out)
	case "execute", "exec":
		if wantsHelp(args[1:]) {
			fleetExecuteHelp(os.Stdout)
			return
		}
		planFile, summary := parseFleetExecuteFlags(args[1:])
		runFleetExecute(planFile, summary)
	default:
		fatalf("unknown fleet subcommand %q (try: plan, execute)", args[0])
	}
}

// parseFleetPlanFlags reads the required prompt (positional) and the optional
// --out <file> (or --out=<file>) destination for the generated plan.
func parseFleetPlanFlags(args []string) (prompt, out string) {
	out = defaultPlanFile
	setPrompt := func(v string) {
		if prompt != "" {
			fatalf("unexpected argument %q", v)
		}
		prompt = v
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--out":
			if i+1 >= len(args) {
				fatalf("--out requires a file path")
			}
			out = args[i+1]
			i++
		case strings.HasPrefix(a, "--out="):
			out = strings.TrimPrefix(a, "--out=")
			if out == "" {
				fatalf("--out requires a file path")
			}
		case strings.HasPrefix(a, "--"):
			// Reject unknown flags rather than silently treating them as the prompt,
			// which would point a later "unexpected argument" error at the wrong token.
			fatalf("unknown flag %q", a)
		default:
			setPrompt(a)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		fatalf(`fleet plan requires a prompt`)
	}
	return prompt, out
}

// parseFleetExecuteFlags reads the optional --plan <file> (or --plan=<file>)
// plan source (defaulting to fleet-plan.json) plus the --summary toggle
// propagated to every node's develop workflow.
func parseFleetExecuteFlags(args []string) (planFile string, summary bool) {
	planFile = defaultPlanFile
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--summary":
			summary = true
		case a == "--with-remote":
			// The remote pipeline (PR open / Copilot pilot / tracking) is not yet
			// wired into FleetWorkflow (Phase 2). Reject the flag explicitly rather
			// than accept it and silently run local-review-only behavior.
			fatalf("--with-remote is not yet implemented (the remote pipeline lands in a later phase)")
		case a == "--plan":
			if i+1 >= len(args) {
				fatalf("--plan requires a file path")
			}
			planFile = args[i+1]
			i++
		case strings.HasPrefix(a, "--plan="):
			planFile = strings.TrimPrefix(a, "--plan=")
			if planFile == "" {
				fatalf("--plan requires a file path")
			}
		default:
			fatalf("unexpected argument %q", a)
		}
	}
	return planFile, summary
}

// runFleetPlan starts the planning workflow, waits for the generated dependency
// graph, writes it to the plan file for review, and prints a short overview.
func runFleetPlan(prompt, out string) {
	c := dial()
	defer c.Close()

	id := "fleet-plan-" + uuid.NewString()
	fmt.Println("Planning fleet…")
	fmt.Printf("  id:      %s\n", id)
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
	fmt.Printf("  watch:   temporal-agents watch %s\n", id)

	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, fleet.FleetPlanWorkflow, fleet.FleetPlanInput{Goal: prompt, WorkDir: cwd()})
	if err != nil {
		fatalf("Could not start fleet planning: %v", err)
	}

	var plan fleet.FleetPlan
	if err := we.Get(context.Background(), &plan); err != nil {
		fatalf("Fleet planning failed: %v", err)
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		fatalf("Could not encode plan: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fatalf("Could not write plan to %s: %v", out, err)
	}

	fmt.Printf("\nPlan written to %s (%d node(s)):\n", out, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if len(n.DependsOn) == 0 {
			fmt.Printf("  - %s\n", n.ID)
		} else {
			fmt.Printf("  - %s (depends on %s)\n", n.ID, strings.Join(n.DependsOn, ", "))
		}
	}
	fmt.Printf("\nReview/edit %s, then run:\n  temporal-agents fleet execute --plan %s\n", out, out)
}

// runFleetExecute reads the approved plan file and starts the fleet workflow,
// returning immediately (like `run`) with a watch hint.
func runFleetExecute(planFile string, summary bool) {
	data, err := os.ReadFile(planFile)
	if err != nil {
		fatalf("Could not read plan %s: %v", planFile, err)
	}
	// Decode strictly: reject unknown fields (a misspelled "dependsOn" must fail
	// rather than silently drop the ordering constraint) while still rejecting any
	// trailing data after the plan object, as json.Unmarshal did.
	var plan fleet.FleetPlan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		fatalf("Could not parse plan %s: %v", planFile, err)
	}
	if dec.More() {
		fatalf("Could not parse plan %s: unexpected trailing data after the plan object", planFile)
	}
	if err := fleet.ValidatePlan(plan); err != nil {
		fatalf("Plan %s is invalid: %v", planFile, err)
	}

	wtDir, err := worktreesDir()
	if err != nil {
		fatalf("Could not locate worktrees directory: %v", err)
	}

	c := dial()
	defer c.Close()

	id := "fleet-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, fleet.FleetWorkflow, fleet.FleetInput{
		Plan:         plan,
		WorkDir:      cwd(),
		WorktreesDir: wtDir,
		Summary:      summary,
	})
	if err != nil {
		fatalf("Could not start fleet: %v", err)
	}

	fmt.Println("Fleet started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  goal:    %s\n", truncate(plan.Goal, 60))
	fmt.Printf("  nodes:   %d\n", len(plan.Nodes))
	fmt.Printf("  workdir: %s (each node develops in its own worktree under %s)\n", cwd(), wtDir)
	if summary {
		fmt.Printf("  summary: on (propagated to each node)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
}

func fleetHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents fleet — fan-out orchestration across a dependency graph

Break a larger change into a dependency graph of small, independently reviewable
slices, then orchestrate a develop workflow per slice, respecting the graph so a
dependent slice only starts once every slice it depends on has succeeded.
Dependencies control both execution order and code layering: each slice develops
on its own branch, seeded with the committed work of the slices it depends on, so
a dependent is developed on top of its prerequisites' reviewed code.

USAGE
  temporal-agents fleet plan "<prompt>" [--out <file>]
  temporal-agents fleet execute [--plan <file>] [--summary]

SUBCOMMANDS
  plan     Have the agent decompose a prompt into a dependency graph and write
           it to a file for you to review and approve.
  execute  Orchestrate an approved plan file: run a develop workflow per node in
           dependency order and aggregate the results.

See "temporal-agents fleet plan --help" and
"temporal-agents fleet execute --help".
`)
}

func fleetPlanHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents fleet plan — decompose a change into a dependency graph

Runs a Pi agent that reads the repository (making no code changes) and produces
a "fleet plan": a dependency graph of small, independently reviewable slices.
The plan is written to a JSON file (default fleet-plan.json) for you to review
and edit before approving it with "fleet execute".

USAGE
  temporal-agents fleet plan "<prompt>" [--out <file>]

FLAGS
  --out <file>   Where to write the generated plan (default: fleet-plan.json)

EXAMPLES
  temporal-agents fleet plan "expose the pricing domain via REST and gRPC"
  temporal-agents fleet plan "add multi-tenant support" --out tenant-plan.json
`)
}

func fleetExecuteHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents fleet execute — orchestrate an approved plan

Reads an approved plan file and runs a develop workflow per node, processing the
graph in dependency layers: independent nodes run in parallel, and a node only
starts once every node it depends on has succeeded. A node whose dependency did
not succeed is skipped. Every node develops in its own git worktree under your
user config directory, so parallel nodes never share a working tree. When all
nodes settle, a single summary notification aggregates each node's status and
develop-step token usage.

USAGE
  temporal-agents fleet execute [--plan <file>] [--summary]

FLAGS
  --plan <file>   Plan file to execute (default: fleet-plan.json)
  --summary       Propagate --summary to each node's develop workflow

EXAMPLES
  temporal-agents fleet execute
  temporal-agents fleet execute --plan tenant-plan.json
  temporal-agents watch <workflow-id>
`)
}
