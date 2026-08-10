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
	"unicode/utf8"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"temporal-agents/internal/codereview"
	"temporal-agents/internal/fleet"
	"temporal-agents/internal/ghcli"
	"temporal-agents/internal/gitcli"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/notify"
	"temporal-agents/internal/piagent"
	"temporal-agents/internal/place"
	"temporal-agents/internal/setting"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/wfid"
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
	case "history":
		if err := historyCmd(os.Args[2:], os.Stdout, openExecutionReader); err != nil {
			fatalf("%v", err)
		}
	case "serve":
		if err := serveCmd(os.Args[2:]); err != nil {
			fatalf("%v", err)
		}
	case "migrate":
		if err := migrateCmd(os.Args[2:], os.Stdout); err != nil {
			fatalf("%v", err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `temporal-agents — a thin Temporal worker that runs a Pi agent

USAGE
  temporal-agents <command> [arguments]

COMMANDS
  migrate                                Apply every bounded context's schema
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
  list                                   List active work through the Agent Hub API
  history [--kind <kind>] [--limit <n>]  List durably recorded executions
  serve [--addr <host:port>]             Serve the Agent Hub REST API

EXAMPLES
  temporal-agents migrate
  temporal-agents worker
  temporal-agents run "summarize the README"
  temporal-agents run "nightly triage" --save triage
  temporal-agents run "watch the queue forever" --chain
  temporal-agents schedule "0 9 * * *" "post the daily digest" --save digest
  temporal-agents template list
  temporal-agents template run triage
  temporal-agents fleet plan "expose the pricing domain via REST and gRPC"
  temporal-agents fleet plan list
  temporal-agents fleet execute --plan-id <handle>
  temporal-agents history
  temporal-agents serve
  temporal-agents cleanup

FLAGS
  --save <name>  Save the invocation as a reusable template (see 'template')
  --chain        Re-trigger the same workflow on each successful completion

ENVIRONMENT
  AGENT_HUB_API_URL     Versioned API endpoint used by list
                        (default http://127.0.0.1:8973/api/v1)
  AGENT_HUB_AUTH_TOKEN  Bearer token sent by list when authentication is enabled
  DATABASE_URL          Postgres connection string (required by migrate, worker,
                        serve and history)
  TEMPORAL_ADDRESS      Temporal endpoint used by execution commands
                        (default localhost:17233)
  AGENT_HUB_OIDC_*      Identity provider serve signs people in with
                        (see 'serve --help'). Automation keeps using
                        AGENT_HUB_AUTH_TOKEN and needs no browser.

The schema is applied by 'temporal-agents migrate'. Run it before 'worker' or
'serve': both verify the schema at startup and refuse to run against an older one.
'history' only reads, so it does not verify: against a database the migrate step has
not reached it reports the database's own error rather than a schema failure.

See "temporal-agents schedule --help", "temporal-agents template --help", and
"temporal-agents serve --help".
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
  (never queued or run concurrently). A skipped firing starts no workflow, so it
  leaves no entry in the durable history either.

EXAMPLES
  temporal-agents schedule "1h" "check for new GitHub issues and summarize them"
  temporal-agents schedule "0 9 * * 1-5" "post the daily standup digest"
  temporal-agents schedule "*/15 * * * *" "poll the queue and report anomalies"

MANAGE
  temporal-agents list        show active schedules and running workflows

HISTORY
  Each fired run is durably recorded as a "run" carrying this schedule's ID (the
  schedule itself has no workflow, so it is not recorded). List what a schedule
  has produced with:

    temporal-agents history --schedule-id <schedule-id>
`)
}

// DefaultHostPort is the Temporal frontend address the CLI connects to. It uses
// a non-default port so the local dev server doesn't collide with other
// projects on 7233. Override with the TEMPORAL_ADDRESS env var.
const DefaultHostPort = "localhost:17233"

func dial() client.Client {
	c, err := connectTemporal()
	if err != nil {
		fatalf("%v", err)
	}
	return c
}

// connectTemporal opens the orchestration client without owning the process
// boundary. Commands that return errors (notably the long-running HTTP server) use
// it directly, while the older one-shot commands keep their existing dial helper
// and fatal behavior.
func connectTemporal() (client.Client, error) {
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = DefaultHostPort
	}
	c, err := client.Dial(client.Options{HostPort: hostPort, Logger: quietLogger{}})
	if err != nil {
		return nil, fmt.Errorf("could not connect to Temporal at %s: %w", hostPort, err)
	}
	return c, nil
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

	// The durable execution store is a hard dependency of every recorded workflow,
	// so the worker resolves it before accepting work: an unreachable store fails here
	// rather than failing each workflow at its first record write. Its schema is
	// verified, never applied — see migrate.go.
	store := openVerifiedStore(context.Background())
	defer store.Close()

	// What the tool is configured with per place lives in storage too. Opening
	// publishes what this build ships, so a worker that starts is a worker whose
	// defaults are the current ones, and every workflow resolves through the same
	// store afterwards.
	config := openPublishedConfiguration(context.Background())
	defer config.Close()

	// Where a paused round is written for an operator to read. It is opened before
	// the worker takes work for the same reason the record is: a loop that stops for
	// a human must leave something a human can find.
	sessions := openSteeringStore(context.Background())
	defer sessions.Close()

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
	// The root bundle carries PersistRunWorkflowState, PromptWorkflow's durable
	// recording activity, with the execution store injected as its driven adapter.
	w.RegisterActivity(&Activities{Store: store})

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
		Store: store,
	})

	// The steering session: the durable wait a review round pauses in when an
	// operator has switched steering on where the work runs. It is a child of the
	// loop that pauses, and records an execution row of its own so what it costs is
	// attributable without it becoming a second item on the overview.
	w.RegisterWorkflow(steering.SessionWorkflow)
	w.RegisterActivity(&steering.Activities{Store: store, Sessions: sessions})

	// The fleet workflows fan out over a dependency graph, reusing the codereview
	// develop workflow for each node. FleetWorkflow runs codereview.DevelopWorkflow
	// as children, which is already registered above.
	w.RegisterWorkflow(fleet.FleetPlanWorkflow)
	w.RegisterWorkflow(fleet.FleetWorkflow)
	w.RegisterActivity(&fleet.Activities{
		Agent: piagent.Agent{},
		Git:   gitcli.New(),
		Store: store,
		Plans: store,
	})

	// A single notification activity, shared by every workflow that notifies.
	w.RegisterActivity(&notification.Activity{Notifier: buildNotifier(opts)})

	// A single instruction resolution, shared by every workflow that runs an agent.
	// It resolves once per unit of work, at its start, and the resolved text travels
	// in the workflow's input from there (see wfinstruction).
	w.RegisterActivity(&instruction.Activity{Store: config})

	// A single setting resolution, shared by every workflow whose shape a setting
	// decides. It resolves through the same store and the same chain the instructions
	// do, so an operator learns one inheritance rule rather than two.
	w.RegisterActivity(&setting.Activity{Resolver: setting.Resolver{Store: config}})

	// A single location probe, shared by every workflow that owns a working
	// directory. It answers over the same git adapter the code workflows drive, so
	// where work runs is established by git rather than guessed from a path.
	w.RegisterActivity(&place.Activity{Prober: gitcli.New()})

	fmt.Printf("Worker ready · task queue %q", TaskQueue)
	fmt.Printf(" · desktop notifications %s", onOff(opts.desktop))
	// Report only whether the webhook is enabled: webhook URLs commonly embed
	// bearer-like secrets in their path or query, so printing the URL would leak
	// credentials into terminal captures and service logs.
	fmt.Printf(" · webhook %s", onOff(opts.webhookURL != ""))
	// Execution history is deliberately not reported: DATABASE_URL is required and
	// the worker has already failed fast without a reachable store, so recording is
	// always on by the time this line prints. (The DSN is never printed either — it
	// commonly embeds credentials, exactly like the webhook URL above.)
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
	}, PromptWorkflow, runRequest(prompt, cwd(), chain))
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
		Action:  scheduleAction(id, prompt, cwd(), chain),
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

// runRequest builds the input for a directly started run. It carries no schedule
// ID: nothing fired it, so its record is attributed to no schedule.
func runRequest(prompt, workDir string, chain bool) PromptRequest {
	return PromptRequest{Prompt: prompt, WorkDir: workDir, Chain: chain}
}

// scheduleAction builds the schedule's action: it starts the very same
// PromptWorkflow that `run` does, with the schedule's own ID threaded into the
// input. That is what lets each fired run record itself as a run attributable to
// this schedule, so no separate schedule kind (or a record of the schedule
// itself, which has no workflow) is needed.
//
// Nothing is recorded for a firing that is skipped by the overlap policy: no
// workflow starts, and every write happens inside a workflow, so history shows no
// misleading entry for a run that never happened.
func scheduleAction(scheduleID, prompt, workDir string, chain bool) *client.ScheduleWorkflowAction {
	req := runRequest(prompt, workDir, chain)
	req.ScheduleID = scheduleID
	return &client.ScheduleWorkflowAction{
		ID:        scheduleID + "-wf",
		Workflow:  PromptWorkflow,
		Args:      []any{req},
		TaskQueue: TaskQueue,
	}
}

// parseSpec treats the arg as a Go duration interval when possible, otherwise a cron expression.
func parseSpec(spec string) client.ScheduleSpec {
	if d, err := time.ParseDuration(spec); err == nil {
		return client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: d}}}
	}
	return client.ScheduleSpec{CronExpressions: []string{spec}}
}

// classifyWorkflow labels a workflow by its ID so watch can decode structured
// results. The convention itself lives in the wfid package because the HTTP read
// API reconstructs a fleet's node tree from the same rule.
func classifyWorkflow(id string) string {
	return string(wfid.Classify(id))
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

	out, err := workflowResult(ctx, c.GetWorkflow(ctx, id, ""), id)
	if err != nil {
		fatalf("Workflow ended with error: %v", err)
	}
	fmt.Println("\n─── result ───")
	fmt.Println(out)
}

// workflowResult decodes a completed workflow's result into the concrete type
// that workflow returns and renders it for display. Most workflows return a
// plain string summary, but a few return structured values — FleetPlanWorkflow a
// fleet.FleetPlan and OpenPRWorkflow a codereview.OpenPRResult — that cannot be
// decoded into a string. It picks the decode target from the workflow-ID class
// (see classifyWorkflow) so `watch` also works for the structured-result
// workflows advertised by `fleet plan` and the open-PR stage.
func workflowResult(ctx context.Context, run client.WorkflowRun, id string) (string, error) {
	switch classifyWorkflow(id) {
	case "fleet-plan":
		var plan fleet.FleetPlan
		if err := run.Get(ctx, &plan); err != nil {
			return "", err
		}
		return formatFleetPlan(plan), nil
	case "open-pr":
		var res codereview.OpenPRResult
		if err := run.Get(ctx, &res); err != nil {
			// A pre-change open-pr workflow completed with a plain string result, which
			// cannot decode into OpenPRResult. Fall back to string decode so watching a
			// legacy completed run still renders instead of erroring.
			var out string
			if serr := run.Get(ctx, &out); serr != nil {
				return "", err
			}
			return out, nil
		}
		if res.URL != "" {
			return res.Summary + "\n" + res.URL, nil
		}
		return res.Summary, nil
	case "review":
		var outcome codereview.ReviewOutcome
		if err := run.Get(ctx, &outcome); err != nil {
			// A pre-change review workflow completed with a plain string result, which
			// cannot decode into ReviewOutcome. Fall back to string decode so watching a
			// legacy completed run still renders instead of erroring.
			var out string
			if serr := run.Get(ctx, &out); serr != nil {
				return "", err
			}
			return out, nil
		}
		return outcome.Summary, nil
	default:
		var out string
		if err := run.Get(ctx, &out); err != nil {
			return "", err
		}
		return out, nil
	}
}

// formatFleetPlan renders a fleet plan's goal and its node list (with each
// node's ordering dependencies) for display.
func formatFleetPlan(plan fleet.FleetPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fleet plan (%d node(s)) for: %s\n", len(plan.Nodes), plan.Goal)
	for _, n := range plan.Nodes {
		if len(n.DependsOn) == 0 {
			fmt.Fprintf(&b, "  - %s\n", n.ID)
		} else {
			fmt.Fprintf(&b, "  - %s (depends on %s)\n", n.ID, strings.Join(n.DependsOn, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
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

// truncate shortens s to at most max bytes, marking the cut with an ellipsis.
//
// The cut is moved back to a rune boundary, so a shortened value stays valid UTF-8.
// The text it is applied to is agent-written or agent-recorded (a failure reason, a
// plan goal, a prompt), which carries arrows, ellipses and accented characters
// routinely — cutting one of those in half would print replacement characters and
// undo the rune-safe capping wfrecord already does one layer down.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max - 1
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
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
