package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"temporal-agents/internal/codereview"
	"temporal-agents/internal/fleet"
	"temporal-agents/internal/ghcli"
	"temporal-agents/internal/gitcli"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/notify"
	"temporal-agents/internal/piagent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "worker":
		if wantsHelp(os.Args[2:]) {
			workerHelp(os.Stdout)
			return
		}
		runWorker(parseWorkerFlags(os.Args[2:]))
	case "run":
		pos, save, chain := parseFlags(os.Args[2:])
		if len(pos) < 1 {
			fmt.Fprintln(os.Stderr, `usage: temporal-agents run "<prompt>" [--save <name>] [--chain]`)
			os.Exit(2)
		}
		startRun(pos[0], save, chain)
	case "schedule":
		if wantsHelp(os.Args[2:]) {
			scheduleHelp(os.Stdout)
			return
		}
		pos, save, chain := parseFlags(os.Args[2:])
		if len(pos) < 2 {
			scheduleHelp(os.Stderr)
			os.Exit(2)
		}
		startSchedule(pos[0], pos[1], save, chain)
	case "template":
		templateCmd(os.Args[2:])
	case "code":
		codeCmd(os.Args[2:])
	case "fleet":
		fleetCmd(os.Args[2:])
	case "cleanup":
		cleanupCmd(os.Args[2:])
	case "watch":
		requireArgs(1, "watch <workflow-id>")
		watchRun(os.Args[2])
	case "list":
		listRunning()
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `temporal-agents — a thin Temporal worker that runs a Pi agent

USAGE
  temporal-agents <command> [arguments]

COMMANDS
  worker [--no-desktop] [--webhook <url>]
                                         Start the Temporal worker
  code <subcommand>                      Agent workflows for the current repo
  fleet <subcommand>                     Fan-out orchestration across a dependency graph
  cleanup                                Remove worktrees created by 'code develop --worktree'
  run "<prompt>" [--save <name>] [--chain]
                                         Start a workflow (returns immediately)
  schedule "<interval|cron>" "<prompt>" [--save <name>] [--chain]
                                         Schedule a workflow (overlaps are skipped)
  template <subcommand>                  Manage and run saved templates
  watch <workflow-id>                    Stream a workflow's live Pi progress
  list                                   List running workflows and schedules

EXAMPLES
  temporal-agents worker
  temporal-agents run "summarize the README"
  temporal-agents run "nightly triage" --save triage
  temporal-agents run "watch the queue forever" --chain
  temporal-agents schedule "0 9 * * *" "post the daily digest" --save digest
  temporal-agents template list
  temporal-agents template run triage
  temporal-agents fleet plan "expose the pricing domain via REST and gRPC"
  temporal-agents fleet execute --plan fleet-plan.json
  temporal-agents cleanup

FLAGS
  --save <name>  Save the invocation as a reusable template (see 'template')
  --chain        Re-trigger the same workflow on each successful completion

See "temporal-agents schedule --help" and "temporal-agents template --help".
`)
	os.Exit(2)
}

func requireArgs(n int, form string) {
	if len(os.Args) < 2+n {
		fmt.Fprintf(os.Stderr, "usage: temporal-agents %s\n", form)
		os.Exit(2)
	}
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// parseFlags splits out the optional "--save <name>" / "--save=<name>" and
// "--chain" flags, returning the remaining positional args, the template name
// ("" if absent), and whether chaining was requested.
func parseFlags(args []string) (positional []string, saveName string, chain bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--chain":
			chain = true
		case a == "--save":
			if i+1 >= len(args) {
				fatalf("--save requires a template name")
			}
			saveName = args[i+1]
			i++
		case strings.HasPrefix(a, "--save="):
			saveName = strings.TrimPrefix(a, "--save=")
			if saveName == "" {
				fatalf("--save requires a template name")
			}
		default:
			positional = append(positional, a)
		}
	}
	return positional, saveName, chain
}

func templateCmd(args []string) {
	if len(args) == 0 || wantsHelp(args) {
		templateHelp(os.Stdout)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}

	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		templateList()
	case "show":
		if len(rest) < 1 {
			fatalf("usage: temporal-agents template show <name>")
		}
		templateShow(rest[0])
	case "delete", "rm":
		if len(rest) < 1 {
			fatalf("usage: temporal-agents template delete <name>")
		}
		templateDelete(rest[0])
	case "run", "exec":
		if len(rest) < 1 {
			fatalf("usage: temporal-agents template run <name>")
		}
		templateRun(rest[0])
	default:
		fatalf("unknown template subcommand %q (try: list, show, run, delete)", sub)
	}
}

func templateList() {
	cfg, path, err := loadConfig()
	if err != nil {
		fatalf("Could not read templates: %v", err)
	}
	if len(cfg.Templates) == 0 {
		fmt.Printf("No templates saved yet (%s).\n", path)
		fmt.Println(`Create one with:  temporal-agents run "<prompt>" --save <name>`)
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tWHEN\tPROMPT")
	fmt.Fprintln(tw, "────\t────\t────\t──────")
	for _, t := range cfg.Templates {
		when := "-"
		if t.Kind == "schedule" {
			when = t.Spec
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Name, t.Kind, when, truncate(t.Prompt, 50))
	}
	tw.Flush()
	fmt.Printf("\n%d template(s) · %s\n", len(cfg.Templates), path)
}

func templateShow(name string) {
	t, err := getTemplate(name)
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("name:   %s\n", t.Name)
	fmt.Printf("kind:   %s\n", t.Kind)
	if t.Kind == "schedule" {
		fmt.Printf("when:   %s\n", t.Spec)
	}
	fmt.Printf("chain:  %t\n", t.Chain)
	fmt.Printf("prompt: %s\n", t.Prompt)
}

func templateDelete(name string) {
	path, err := deleteTemplate(name)
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("Deleted template %q (%s).\n", name, path)
}

func templateRun(name string) {
	t, err := getTemplate(name)
	if err != nil {
		fatalf("%v", err)
	}
	switch t.Kind {
	case "run":
		startRun(t.Prompt, "", t.Chain)
	case "schedule":
		startSchedule(t.Spec, t.Prompt, "", t.Chain)
	default:
		fatalf("template %q has unknown kind %q", name, t.Kind)
	}
}

func templateHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents template — manage and run saved templates

Templates are saved with the --save flag on "run" or "schedule" and stored in
your user config directory. They remember the prompt (and schedule spec), but
not the working directory: a template always runs in the current directory.

USAGE
  temporal-agents template list             List all saved templates
  temporal-agents template show <name>      Show one template's details
  temporal-agents template run <name>       Execute a template (run or schedule)
  temporal-agents template delete <name>    Delete a template

EXAMPLES
  temporal-agents run "nightly triage" --save triage
  temporal-agents template run triage
  temporal-agents template delete triage
`)
}

func workerHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents worker — start the Temporal worker

Runs the worker that executes the workflows and their activities, including the
notifications that fire when a workflow finishes or fails.

USAGE
  temporal-agents worker [--no-desktop] [--webhook <url>]

FLAGS
  --no-desktop     Disable the macOS desktop notification (enabled by default on
                   macOS only)
  --webhook <url>  POST completion notifications as JSON to <url> (disabled by
                   default)

EXAMPLES
  temporal-agents worker
  temporal-agents worker --no-desktop
  temporal-agents worker --webhook https://hooks.example.com/notify
  temporal-agents worker --no-desktop --webhook https://hooks.example.com/notify
`)
}

func scheduleHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents schedule — run a workflow on a recurring schedule

USAGE
  temporal-agents schedule "<interval|cron>" "<prompt>"

The first argument decides how often the workflow runs. It is read in two ways:

  1. INTERVAL — a Go duration (any string time.ParseDuration accepts).
     Runs repeatedly, spaced by that duration.

       30s      every 30 seconds
       5m       every 5 minutes
       1h       every hour
       1h30m    every 90 minutes
       24h      every day

  2. CRON — a standard 5-field cron expression (used when the value is
     not a valid duration). Fields: minute hour day-of-month month day-of-week.

       "* * * * *"       every minute
       "0 * * * *"       at the top of every hour
       "0 9 * * *"       every day at 09:00
       "0 9 * * 1-5"     weekdays at 09:00
       "*/15 * * * *"    every 15 minutes
       "0 0 1 * *"       midnight on the 1st of each month

     Cron runs in UTC by default. Prefix a timezone with CRON_TZ to change it:
       "CRON_TZ=Europe/Copenhagen 0 9 * * *"

OVERLAP
  If a run is still going when the next one is due, the next run is SKIPPED
  (never queued or run concurrently).

EXAMPLES
  temporal-agents schedule "1h" "check for new GitHub issues and summarize them"
  temporal-agents schedule "0 9 * * 1-5" "post the daily standup digest"
  temporal-agents schedule "*/15 * * * *" "poll the queue and report anomalies"

MANAGE
  temporal-agents list        show active schedules and running workflows
`)
}

// DefaultHostPort is the Temporal frontend address the CLI connects to. It uses
// a non-default port so the local dev server doesn't collide with other
// projects on 7233. Override with the TEMPORAL_ADDRESS env var.
const DefaultHostPort = "localhost:17233"

func dial() client.Client {
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = DefaultHostPort
	}
	c, err := client.Dial(client.Options{HostPort: hostPort, Logger: quietLogger{}})
	if err != nil {
		fatalf("Could not connect to Temporal at %s: %v", hostPort, err)
	}
	return c
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fatalf("Could not determine working directory: %v", err)
	}
	return dir
}

// notifyOptions captures the notification adapters enabled at worker start.
type notifyOptions struct {
	// desktop enables the macOS desktop notifier (on by default on macOS only,
	// since it shells out to the macOS-only osascript).
	desktop bool
	// webhookURL, when non-empty, enables the webhook notifier posting to it.
	webhookURL string
}

// parseWorkerFlags reads the worker's notification flags: --no-desktop disables
// the macOS desktop notifier (enabled by default on macOS only, as it shells
// out to the macOS-only osascript), and --webhook <url> (or --webhook=<url>)
// enables the webhook notifier posting to that URL (disabled by default).
func parseWorkerFlags(args []string) notifyOptions {
	opts := notifyOptions{desktop: runtime.GOOS == "darwin"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-desktop":
			opts.desktop = false
		case a == "--webhook":
			if i+1 >= len(args) {
				fatalf("--webhook requires a URL")
			}
			opts.webhookURL = args[i+1]
			i++
		case strings.HasPrefix(a, "--webhook="):
			opts.webhookURL = strings.TrimPrefix(a, "--webhook=")
			if opts.webhookURL == "" {
				fatalf("--webhook requires a URL")
			}
		default:
			fatalf("unexpected argument %q", a)
		}
	}
	return opts
}

// buildNotifier assembles the fan-out notifier from the enabled adapters. When
// none are enabled the returned Multi is empty and Notify is a no-op.
func buildNotifier(opts notifyOptions) notification.Notifier {
	var notifiers notify.Multi
	if opts.desktop {
		notifiers = append(notifiers, notify.NewDesktop())
	}
	if opts.webhookURL != "" {
		notifiers = append(notifiers, notify.NewWebhook(opts.webhookURL))
	}
	return notifiers
}

func runWorker(opts notifyOptions) {
	c := dial()
	defer c.Close()

	// Flush heartbeats promptly so `watch` sees near-real-time Pi progress
	// instead of the SDK's default ~30s throttle.
	//
	// SysInfoProvider surfaces CPU/memory usage in the worker heartbeats the SDK
	// sends to Temporal, which populates the Worker Health / host resource
	// reporting view in the Temporal UI. Without it, heartbeats report 0 for
	// resource usage and the UI flags a missing dependency.
	w := worker.New(c, TaskQueue, worker.Options{
		MaxHeartbeatThrottleInterval:     time.Second,
		DefaultHeartbeatThrottleInterval: time.Second,
		SysInfoProvider:                  sysinfo.SysInfoProvider(),
	})
	w.RegisterWorkflow(PromptWorkflow)
	w.RegisterActivity(RunPiAgent)

	// The "code pilot" and "code review" workflows and their port-backed
	// activities (both share the same Activities bundle).
	w.RegisterWorkflow(codereview.PilotWorkflow)
	w.RegisterWorkflow(codereview.ReviewWorkflow)
	w.RegisterWorkflow(codereview.DevelopWorkflow)
	w.RegisterWorkflow(codereview.OpenPRWorkflow)
	w.RegisterActivity(&codereview.Activities{
		Git:   gitcli.New(),
		PRs:   ghcli.New(),
		Agent: piagent.Agent{},
	})

	// The fleet workflows fan out over a dependency graph, reusing the codereview
	// develop workflow for each node. FleetWorkflow runs codereview.DevelopWorkflow
	// as children, which is already registered above.
	w.RegisterWorkflow(fleet.FleetPlanWorkflow)
	w.RegisterWorkflow(fleet.FleetWorkflow)
	w.RegisterActivity(&fleet.Activities{Agent: piagent.Agent{}})

	// A single notification activity, shared by every workflow that notifies.
	w.RegisterActivity(&notification.Activity{Notifier: buildNotifier(opts)})

	fmt.Printf("Worker ready · task queue %q", TaskQueue)
	fmt.Printf(" · desktop notifications %s", onOff(opts.desktop))
	// Report only whether the webhook is enabled: webhook URLs commonly embed
	// bearer-like secrets in their path or query, so printing the URL would leak
	// credentials into terminal captures and service logs.
	fmt.Printf(" · webhook %s", onOff(opts.webhookURL != ""))
	fmt.Printf(" · press Ctrl+C to stop\n")
	if err := w.Run(worker.InterruptCh()); err != nil {
		fatalf("Worker stopped with error: %v", err)
	}
	fmt.Println("Worker stopped.")
}

func startRun(prompt, saveName string, chain bool) {
	c := dial()
	defer c.Close()

	id := "run-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, PromptWorkflow, PromptRequest{Prompt: prompt, WorkDir: cwd(), Chain: chain})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Workflow started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
	if chain {
		fmt.Printf("  chain:   on (re-triggers on each success)\n")
	}
	fmt.Printf("  watch:   temporal-agents watch %s\n", we.GetID())
	maybeSave(saveName, Template{Name: saveName, Kind: "run", Prompt: prompt, Chain: chain})
}

func startSchedule(spec, prompt, saveName string, chain bool) {
	c := dial()
	defer c.Close()

	id := "schedule-" + uuid.NewString()
	_, err := c.ScheduleClient().Create(context.Background(), client.ScheduleOptions{
		ID:      id,
		Spec:    parseSpec(spec),
		Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP,
		Action: &client.ScheduleWorkflowAction{
			ID:        id + "-wf",
			Workflow:  PromptWorkflow,
			Args:      []any{PromptRequest{Prompt: prompt, WorkDir: cwd(), Chain: chain}},
			TaskQueue: TaskQueue,
		},
	})
	if err != nil {
		fatalf("Could not create schedule: %v", err)
	}

	fmt.Println("Schedule created.")
	fmt.Printf("  id:      %s\n", id)
	fmt.Printf("  when:    %s\n", spec)
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
	if chain {
		fmt.Printf("  chain:   on (each fired run re-triggers on success)\n")
	}
	maybeSave(saveName, Template{Name: saveName, Kind: "schedule", Spec: spec, Prompt: prompt, Chain: chain})
}

// maybeSave persists a template when --save was provided.
func maybeSave(saveName string, t Template) {
	if saveName == "" {
		return
	}
	path, err := saveTemplate(t)
	if err != nil {
		fatalf("Could not save template: %v", err)
	}
	fmt.Printf("  saved:   template %q → %s\n", saveName, path)
}

// parseSpec treats the arg as a Go duration interval when possible, otherwise a cron expression.
func parseSpec(spec string) client.ScheduleSpec {
	if d, err := time.ParseDuration(spec); err == nil {
		return client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: d}}}
	}
	return client.ScheduleSpec{CronExpressions: []string{spec}}
}

func listRunning() {
	c := dial()
	defer c.Close()
	ctx := context.Background()

	type row struct{ kind, id string }
	var rows []row

	// Running workflows (excluding the schedule client's internal executions is not needed here).
	var next []byte
	for {
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus='Running'",
			NextPageToken: next,
		})
		if err != nil {
			fatalf("Could not list workflows: %v", err)
		}
		for _, e := range resp.Executions {
			rows = append(rows, row{classifyWorkflow(e.Execution.WorkflowId), e.Execution.WorkflowId})
		}
		next = resp.NextPageToken
		if len(next) == 0 {
			break
		}
	}

	// Schedules.
	iter, err := c.ScheduleClient().List(ctx, client.ScheduleListOptions{})
	if err != nil {
		fatalf("Could not list schedules: %v", err)
	}
	for iter.HasNext() {
		s, err := iter.Next()
		if err != nil {
			fatalf("Could not list schedules: %v", err)
		}
		rows = append(rows, row{"schedule", s.ID})
	}

	if len(rows) == 0 {
		fmt.Println("Nothing running.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tID")
	fmt.Fprintln(tw, "────\t──")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\n", r.kind, r.id)
	}
	tw.Flush()
	fmt.Printf("\n%d active\n", len(rows))
}

// classifyWorkflow labels a workflow row by its ID prefix so `list` surfaces
// what each running workflow is — including fleet parents and their per-node
// develop children ("fleet-<uuid>-<nodeid>"), which share the "fleet-" prefix.
func classifyWorkflow(id string) string {
	switch {
	case strings.HasPrefix(id, "fleet-plan-"):
		return "fleet-plan"
	case strings.HasPrefix(id, "fleet-"):
		return "fleet"
	case strings.HasPrefix(id, "develop-"):
		return "develop"
	case strings.HasPrefix(id, "review-"):
		return "review"
	case strings.HasPrefix(id, "pilot-"):
		return "pilot"
	case strings.HasPrefix(id, "open-pr-"):
		return "open-pr"
	case strings.HasPrefix(id, "schedule-"):
		return "schedule"
	default:
		return "run"
	}
}

// watchRun polls the workflow and prints Pi's live progress (from activity
// heartbeat details), then prints the final result on completion.
func watchRun(id string) {
	c := dial()
	defer c.Close()
	ctx := context.Background()

	fmt.Printf("Watching %s (Ctrl+C to stop)\n", id)
	// The heartbeat detail is the full transcript so far; print only the lines
	// that are new since the previous poll so output reads as a live stream.
	var printed []string
	for {
		desc, err := c.DescribeWorkflowExecution(ctx, id, "")
		if err != nil {
			fatalf("Could not describe workflow: %v", err)
		}
		for _, pa := range desc.GetPendingActivities() {
			if d := decodeHeartbeat(pa.GetHeartbeatDetails()); d != "" {
				printed = printNewLines(printed, strings.Split(d, "\n"))
			}
		}
		if desc.GetWorkflowExecutionInfo().GetStatus() != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			break
		}
		time.Sleep(time.Second)
	}

	var out string
	if err := c.GetWorkflow(ctx, id, "").Get(ctx, &out); err != nil {
		fatalf("Workflow ended with error: %v", err)
	}
	fmt.Println("\n─── result ───")
	fmt.Println(out)
}

// printNewLines prints lines from cur that differ from the already-printed
// prefix and returns the new printed set. The last line of a transcript may
// grow in place (streaming assistant text), so it is reprinted when it changes.
func printNewLines(prev, cur []string) []string {
	i := 0
	for i < len(prev) && i < len(cur) && prev[i] == cur[i] {
		i++
	}
	for _, line := range cur[i:] {
		if line != "" {
			fmt.Printf("  … %s\n", line)
		}
	}
	return cur
}

func decodeHeartbeat(p *commonpb.Payloads) string {
	if p == nil || len(p.GetPayloads()) == 0 {
		return ""
	}
	var s string
	if err := converter.GetDefaultDataConverter().FromPayloads(p, &s); err != nil {
		return ""
	}
	return s
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// onOff renders a boolean as a human-readable on/off state for worker startup
// output.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// quietLogger suppresses the SDK's info/debug chatter so CLI output stays clean.
type quietLogger struct{}

func (quietLogger) Debug(string, ...any) {}
func (quietLogger) Info(string, ...any)  {}
func (quietLogger) Warn(msg string, kv ...any) {
	log.Printf("warn: %s", msg)
}
func (quietLogger) Error(msg string, kv ...any) {
	log.Printf("error: %s", msg)
}
