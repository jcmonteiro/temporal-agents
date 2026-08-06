package main

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/piagent"
	"temporal-agents/internal/wfnotify"
	"temporal-agents/internal/wfrecord"
)

// TaskQueue is the single, default task queue used by everything.
const TaskQueue = "default"

// PromptRequest is the input to PromptWorkflow.
type PromptRequest struct {
	// Prompt is the instruction handed to the Pi agent.
	Prompt string
	// WorkDir is the directory the CLI was invoked from; the Pi agent runs there.
	WorkDir string
	// Chain, when true, immediately re-triggers the same workflow (via
	// continue-as-new) after each successful run, looping indefinitely.
	Chain bool
	// TokensSoFar carries the accumulated total token usage from prior runs of a
	// chained workflow, so a terminal run's result reports the whole chain's
	// usage.
	TokensSoFar int
	// ScheduleID is the schedule that fired this run, set by `schedule` and left
	// empty by `run`. A schedule fires the same workflow `run` uses, so it is not
	// a distinct kind of execution: it is a run attributable to its schedule.
	ScheduleID string
}

// PromptWorkflow runs the Pi agent activity for the given prompt and returns its
// output. When req.Chain is set, a successful run continues as new with the
// same input, chaining the workflow indefinitely.
//
// The run is also durably recorded: it persists a "started" record before the
// agent runs and a terminal record once it settles, so the execution survives
// Temporal's retention and a reset of Temporal's own state. Recording is a hard
// dependency — a record that cannot be written fails the workflow (see
// persistRunState).
func PromptWorkflow(ctx workflow.Context, req PromptRequest) (out string, err error) {
	// Notify best-effort when the run fails. Continue-as-new is a control signal
	// (chained runs), not a failure, so NotifyFailureBestEffort excludes it.
	defer func() { wfnotify.NotifyFailureBestEffort(ctx, "Run failed", err) }()

	// Record the run as started before any work happens, so an execution that
	// later fails, is cancelled, or is lost to a worker crash is still visible in
	// the durable history.
	id := wfrecord.Of(ctx)
	rec := RunState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		Prompt:           req.Prompt,
		ScheduleID:       req.ScheduleID,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
	}
	if perr := persistRunState(ctx, rec); perr != nil {
		return "", fmt.Errorf("record the run as started: %w", perr)
	}

	agentCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		// The activity streams Pi's progress via heartbeats; if it stops
		// heartbeating for this long, Temporal treats it as failed.
		HeartbeatTimeout: time.Minute,
	})

	var res piagent.Result
	if aerr := workflow.ExecuteActivity(agentCtx, RunPiAgent, req).Get(agentCtx, &res); aerr != nil {
		// The run has failed; record that terminal state and surface the original
		// failure. A record write that also fails is not allowed to replace the more
		// informative agent error — the workflow fails either way, which is the
		// must-succeed guarantee.
		if perr := finishRunState(ctx, rec, 0, aerr); perr != nil {
			workflow.GetLogger(ctx).Error("could not record the run's terminal state", "error", perr)
		}
		return "", aerr
	}

	// Fold this run's usage into the running total carried across chained runs.
	total := req.TokensSoFar + res.Tokens

	if req.Chain {
		// Re-run the same workflow with the same input, carrying the accumulated
		// token usage forward. Continue-as-new keeps the event history bounded
		// across iterations.
		//
		// This iteration has settled successfully, so record it before returning the
		// control signal: each iteration is its own row, keyed on its own run ID, and
		// carries only its own token usage.
		if perr := finishRunState(ctx, rec, res.Tokens, nil); perr != nil {
			return "", perr
		}
		next := req
		next.TokensSoFar = total
		return "", workflow.NewContinueAsNewError(ctx, PromptWorkflow, next)
	}

	// A non-chained run has reached its terminal step: append the total token
	// usage to the result and notify best-effort. This runs only here (never
	// before continue-as-new, which would cancel the in-flight activity).
	if perr := finishRunState(ctx, rec, res.Tokens, nil); perr != nil {
		return "", perr
	}
	result := res.Output + "\n\n" + piagent.FormatTokenTotal(total)
	wfnotify.NotifyBestEffort(ctx, notification.Notification{Title: "Run complete", Body: result})
	return result, nil
}

// persistRunState writes rec via the PersistRunWorkflowState activity under the
// shared must-succeed policy: Temporal's retries absorb a transient store outage
// and an exhausted policy surfaces as an error the caller must propagate.
func persistRunState(ctx workflow.Context, rec RunState) error {
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	return workflow.ExecuteActivity(opts, a.PersistRunWorkflowState, rec).Get(opts, nil)
}

// finishRunState records the run's terminal state: its outcome, its own
// incremental token usage, and any failure text. It writes on a disconnected
// context so a cancelled run still settles its record instead of being left
// "running" forever.
func finishRunState(ctx workflow.Context, rec RunState, tokens int, err error) error {
	rec.EndedAt = workflow.Now(ctx)
	rec.Status = wfrecord.StatusOf(err)
	rec.Tokens = tokens
	rec.Error = wfrecord.FailureText(err)

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistRunWorkflowState, rec).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the run's terminal state: %w", perr)
	}
	return nil
}
