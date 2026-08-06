package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/fleet"
)

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
		fleetPlanCmd(args[1:])
	case "execute", "exec":
		if wantsHelp(args[1:]) {
			fleetExecuteHelp(os.Stdout)
			return
		}
		planID, summary := parseFleetExecuteFlags(args[1:])
		runFleetExecute(planID, summary)
	default:
		fatalf("unknown fleet subcommand %q (try: plan, execute)", args[0])
	}
}

// fleetPlanCmd dispatches "fleet plan": generating a plan from a prompt, or
// reviewing the plans already stored.
func fleetPlanCmd(args []string) {
	if wantsHelp(args) {
		fleetPlanHelp(os.Stdout)
		return
	}
	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			fleetPlanList()
			return
		case "show":
			if len(args) < 2 {
				fatalf("usage: temporal-agents fleet plan show <handle>")
			}
			fleetPlanShow(args[1])
			return
		}
	}
	prompt, name := parseFleetPlanFlags(args)
	runFleetPlan(prompt, name)
}

// parseFleetPlanFlags reads the required prompt (positional) and the optional
// --name <name> (or --name=<name>) label for the stored plan. The plan's handle
// is always generated; --name is display-only metadata, so there is deliberately
// no way to choose a handle (a name nothing keeps unique could not resolve a plan).
func parseFleetPlanFlags(args []string) (prompt, name string) {
	setPrompt := func(v string) {
		if prompt != "" {
			fatalf("unexpected argument %q", v)
		}
		prompt = v
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--name":
			if i+1 >= len(args) {
				fatalf("--name requires a value")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
			if name == "" {
				fatalf("--name requires a value")
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
	return prompt, name
}

// parseFleetExecuteFlags reads the required --plan-id <handle> (or
// --plan-id=<handle>) plan handle plus the --summary toggle propagated to every
// node's develop workflow. A plan is resolved from the store by handle only:
// there is no plan file to point at.
func parseFleetExecuteFlags(args []string) (planID string, summary bool) {
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
		case a == "--plan-id":
			if i+1 >= len(args) {
				fatalf("--plan-id requires a plan handle")
			}
			planID = args[i+1]
			i++
		case strings.HasPrefix(a, "--plan-id="):
			planID = strings.TrimPrefix(a, "--plan-id=")
			if planID == "" {
				fatalf("--plan-id requires a plan handle")
			}
		default:
			fatalf("unexpected argument %q", a)
		}
	}
	if strings.TrimSpace(planID) == "" {
		fatalf("fleet execute requires --plan-id <handle> (list stored plans with 'fleet plan list')")
	}
	return planID, summary
}

// newPlanHandle mints the handle a plan is stored and referred to by. It is short
// enough to type on the command line while staying collision-free in practice,
// and is generated up front so the CLI can print it before the (long) planning
// run finishes.
func newPlanHandle() string {
	return "plan-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// runFleetPlan starts the planning workflow, waits for the generated dependency
// graph, and prints the handle it was stored under. The workflow itself writes
// the plan to the store, so a store failure fails the run: the printed handle
// always resolves.
func runFleetPlan(prompt, name string) {
	c := dial()
	defer c.Close()

	id := "fleet-plan-" + uuid.NewString()
	handle := newPlanHandle()
	fmt.Println("Planning fleet…")
	fmt.Printf("  id:      %s\n", id)
	fmt.Printf("  plan:    %s\n", handle)
	if name != "" {
		fmt.Printf("  name:    %s\n", name)
	}
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
	fmt.Printf("  watch:   temporal-agents watch %s\n", id)

	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, fleet.FleetPlanWorkflow, fleet.FleetPlanInput{
		Goal:    prompt,
		WorkDir: cwd(),
		PlanID:  handle,
		Name:    name,
	})
	if err != nil {
		fatalf("Could not start fleet planning: %v", err)
	}

	var plan fleet.FleetPlan
	if err := we.Get(context.Background(), &plan); err != nil {
		fatalf("Fleet planning failed: %v", err)
	}

	fmt.Printf("\nPlan %s stored (%d node(s)):\n", handle, len(plan.Nodes))
	printPlanNodes(plan)
	fmt.Printf("\nReview it with:\n  temporal-agents fleet plan show %s\n", handle)
	fmt.Printf("Then run it with:\n  temporal-agents fleet execute --plan-id %s\n", handle)
}

// fleetPlanList prints the stored plans, newest first.
func fleetPlanList() {
	ctx := context.Background()
	store := openStore(ctx)
	defer store.Close()

	plans, err := store.ListPlans(ctx, 0)
	if err != nil {
		fatalf("Could not read the stored fleet plans: %v", err)
	}
	if len(plans) == 0 {
		fmt.Println("No fleet plans stored yet.")
		fmt.Println(`Create one with:  temporal-agents fleet plan "<prompt>"`)
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "HANDLE\tNAME\tNODES\tCREATED\tGOAL")
	fmt.Fprintln(tw, "──────\t────\t─────\t───────\t────")
	for _, p := range plans {
		name := p.Name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			p.ID, name, p.Nodes, formatStamp(p.CreatedAt), truncate(p.Goal, 50))
	}
	tw.Flush()
	fmt.Printf("\n%d plan(s)\n", len(plans))
}

// fleetPlanShow prints one stored plan in full.
func fleetPlanShow(handle string) {
	ctx := context.Background()
	store := openStore(ctx)
	defer store.Close()

	stored, err := store.Plan(ctx, handle)
	if err != nil {
		fatalf("%v", planReadError(handle, err))
	}
	plan := decodePlan(handle, stored.Document)

	fmt.Printf("handle:  %s\n", stored.ID)
	if stored.Name != "" {
		fmt.Printf("name:    %s\n", stored.Name)
	}
	fmt.Printf("created: %s\n", formatStamp(stored.CreatedAt))
	fmt.Printf("goal:    %s\n", plan.Goal)
	fmt.Printf("nodes:   %d\n", len(plan.Nodes))
	printPlanNodes(plan)
	fmt.Printf("\nRun it with:\n  temporal-agents fleet execute --plan-id %s\n", stored.ID)
}

// printPlanNodes lists a plan's nodes with their ordering dependencies.
func printPlanNodes(plan fleet.FleetPlan) {
	for _, n := range plan.Nodes {
		if len(n.DependsOn) == 0 {
			fmt.Printf("  - %s\n", n.ID)
		} else {
			fmt.Printf("  - %s (depends on %s)\n", n.ID, strings.Join(n.DependsOn, ", "))
		}
	}
}

// planReadError renders a failed plan lookup, distinguishing an unknown handle
// (the operator mistyped it, or the plan was never stored) from a store problem.
// Either way the operation aborts: the store is the only source of truth for a
// plan, so there is nothing to fall back on.
func planReadError(handle string, err error) error {
	if errors.Is(err, execstore.ErrNoSuchPlan) {
		return fmt.Errorf("No fleet plan with handle %q (list them with 'fleet plan list').", handle)
	}
	return fmt.Errorf("Could not read fleet plan %s: %v", handle, err)
}

// decodePlan decodes a stored plan document into the fleet's own plan type,
// strictly: an unknown field means the stored document does not match the plan
// schema this binary understands, which must fail loudly rather than silently drop
// (for example) a dependency edge.
func decodePlan(handle string, document []byte) fleet.FleetPlan {
	var plan fleet.FleetPlan
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		fatalf("Could not parse stored plan %s: %v", handle, err)
	}
	if dec.More() {
		fatalf("Could not parse stored plan %s: unexpected trailing data after the plan object", handle)
	}
	return plan
}

// runFleetExecute resolves the approved plan by handle and starts the fleet
// workflow, returning immediately (like `run`) with a watch hint.
func runFleetExecute(planID string, summary bool) {
	ctx := context.Background()
	store := openStore(ctx)
	defer store.Close()

	stored, err := store.Plan(ctx, planID)
	if err != nil {
		fatalf("%v", planReadError(planID, err))
	}
	plan := decodePlan(planID, stored.Document)
	// The validation gate still runs before any child workflow starts, exactly as
	// it did when the plan came from a file.
	if err := fleet.ValidatePlan(plan); err != nil {
		fatalf("Plan %s is invalid: %v", planID, err)
	}

	wtDir, err := worktreesDir()
	if err != nil {
		fatalf("Could not locate worktrees directory: %v", err)
	}

	c := dial()
	defer c.Close()

	id := "fleet-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, fleet.FleetWorkflow, fleet.FleetInput{
		Plan:         plan,
		WorkDir:      cwd(),
		WorktreesDir: wtDir,
		Summary:      summary,
		// Recorded on the run, so an execution is traceable back to the plan it came
		// from.
		PlanID: stored.ID,
	})
	if err != nil {
		fatalf("Could not start fleet: %v", err)
	}

	fmt.Println("Fleet started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  plan:    %s\n", stored.ID)
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

Plans live in the store (Postgres), not in a file: planning prints a handle, and
that handle is how a plan is reviewed and executed later.

USAGE
  temporal-agents fleet plan "<prompt>" [--name <name>]
  temporal-agents fleet plan list
  temporal-agents fleet plan show <handle>
  temporal-agents fleet execute --plan-id <handle> [--summary]

SUBCOMMANDS
  plan            Have the agent decompose a prompt into a dependency graph,
                  stored under a printed handle for you to review and approve.
  plan list       List the stored plans.
  plan show       Print one stored plan.
  execute         Orchestrate a stored plan: run a develop workflow per node in
                  dependency order and aggregate the results.

See "temporal-agents fleet plan --help" and
"temporal-agents fleet execute --help".
`)
}

func fleetPlanHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents fleet plan — decompose a change into a dependency graph

Runs a Pi agent that reads the repository (making no code changes) and produces
a "fleet plan": a dependency graph of small, independently reviewable slices. The
plan is stored in Postgres under a generated handle and printed, so you can
review it and then execute it by that handle. There is no plan file.

The handle is the only way to select a plan. --name is display-only metadata for
the listing: nothing keeps a name unique, so it could not resolve a plan.

USAGE
  temporal-agents fleet plan "<prompt>" [--name <name>]
  temporal-agents fleet plan list
  temporal-agents fleet plan show <handle>

FLAGS
  --name <name>   Label shown next to the plan in "fleet plan list"

EXAMPLES
  temporal-agents fleet plan "expose the pricing domain via REST and gRPC"
  temporal-agents fleet plan "add multi-tenant support" --name tenancy
  temporal-agents fleet plan list
  temporal-agents fleet plan show plan-1a2b3c4d5e6f
`)
}

func fleetExecuteHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents fleet execute — orchestrate a stored plan

Resolves an approved plan by its handle and runs a develop workflow per node,
processing the graph in dependency layers: independent nodes run in parallel, and
a node only starts once every node it depends on has succeeded. A node whose
dependency did not succeed is skipped. Every node develops in its own git
worktree under your user config directory, so parallel nodes never share a
working tree. When all nodes settle, a single summary notification aggregates
each node's status and develop-step token usage.

The plan is read from the store, which is authoritative: a plan that cannot be
read aborts the run rather than falling back to anything.

USAGE
  temporal-agents fleet execute --plan-id <handle> [--summary]

FLAGS
  --plan-id <handle>   Stored plan to execute (see "fleet plan list")
  --summary            Propagate --summary to each node's develop workflow

EXAMPLES
  temporal-agents fleet plan list
  temporal-agents fleet execute --plan-id plan-1a2b3c4d5e6f
  temporal-agents fleet execute --plan-id plan-1a2b3c4d5e6f --summary
  temporal-agents watch <workflow-id>
`)
}
