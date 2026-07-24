package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
		requireArgs(1, "run \"<prompt>\"")
		startRun(os.Args[2])
	case "schedule":
		requireArgs(2, "schedule \"<interval|cron>\" \"<prompt>\"")
		startSchedule(os.Args[2], os.Args[3])
	case "list":
		listRunning()
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `temporal-agents - thin Temporal worker running a Pi agent

Usage:
  temporal-agents worker                                 Start the Temporal worker
  temporal-agents run "<prompt>"                         Start a workflow (does not wait)
  temporal-agents schedule "<interval|cron>" "<prompt>"  Schedule a workflow (skips overlaps)
  temporal-agents list                                   List running workflows and schedules`)
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
	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	return c
}

func runWorker() {
	c := dial()
	defer c.Close()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(PromptWorkflow)
	w.RegisterActivity(RunPiAgent)

	log.Printf("worker started on task queue %q", TaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker: %v", err)
	}
}

func startRun(prompt string) {
	c := dial()
	defer c.Close()

	id := "run-" + uuid.NewString()
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        id,
		TaskQueue: TaskQueue,
	}, PromptWorkflow, prompt)
	if err != nil {
		log.Fatalf("start workflow: %v", err)
	}
	fmt.Printf("run %s\n", we.GetID())
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
			Args:      []any{prompt},
			TaskQueue: TaskQueue,
		},
	})
	if err != nil {
		log.Fatalf("create schedule: %v", err)
	}
	fmt.Printf("schedule %s\n", id)
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

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tID")

	// Running workflows.
	var next []byte
	for {
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus='Running'",
			NextPageToken: next,
		})
		if err != nil {
			log.Fatalf("list workflows: %v", err)
		}
		for _, e := range resp.Executions {
			fmt.Fprintf(tw, "run\t%s\n", e.Execution.WorkflowId)
		}
		next = resp.NextPageToken
		if len(next) == 0 {
			break
		}
	}

	// Schedules.
	iter, err := c.ScheduleClient().List(ctx, client.ScheduleListOptions{})
	if err != nil {
		log.Fatalf("list schedules: %v", err)
	}
	for iter.HasNext() {
		s, err := iter.Next()
		if err != nil {
			log.Fatalf("list schedules: %v", err)
		}
		fmt.Fprintf(tw, "schedule\t%s\n", s.ID)
	}

	tw.Flush()
}
