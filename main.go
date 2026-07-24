package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "worker":
		runWorker()
	case "run":
		requireArgs(1, `run "<prompt>"`)
		startRun(os.Args[2])
	case "schedule":
		requireArgs(2, `schedule "<interval|cron>" "<prompt>"`)
		startSchedule(os.Args[2], os.Args[3])
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
  worker                                 Start the Temporal worker
  run "<prompt>"                         Start a workflow (returns immediately)
  schedule "<interval|cron>" "<prompt>"  Schedule a workflow (overlaps are skipped)
  list                                   List running workflows and schedules

EXAMPLES
  temporal-agents worker
  temporal-agents run "summarize the README"
  temporal-agents schedule "1h" "check for new issues"
  temporal-agents schedule "0 9 * * *" "post the daily digest"
`)
	os.Exit(2)
}

func requireArgs(n int, form string) {
	if len(os.Args) < 2+n {
		fmt.Fprintf(os.Stderr, "usage: temporal-agents %s\n", form)
		os.Exit(2)
	}
}

func dial() client.Client {
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
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

func runWorker() {
	c := dial()
	defer c.Close()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(PromptWorkflow)
	w.RegisterActivity(RunPiAgent)

	fmt.Printf("Worker ready · task queue %q · press Ctrl+C to stop\n", TaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		fatalf("Worker stopped with error: %v", err)
	}
	fmt.Println("Worker stopped.")
}

func startRun(prompt string) {
	c := dial()
	defer c.Close()

	id := "run-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, PromptWorkflow, PromptRequest{Prompt: prompt, WorkDir: cwd()})
	if err != nil {
		fatalf("Could not start workflow: %v", err)
	}

	fmt.Println("Workflow started.")
	fmt.Printf("  id:      %s\n", we.GetID())
	fmt.Printf("  prompt:  %s\n", truncate(prompt, 60))
	fmt.Printf("  workdir: %s\n", cwd())
}

func startSchedule(spec, prompt string) {
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
			Args:      []any{PromptRequest{Prompt: prompt, WorkDir: cwd()}},
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

	// Running workflows.
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
			rows = append(rows, row{"run", e.Execution.WorkflowId})
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

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
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
