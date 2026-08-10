package main

import (
	"context"
	"time"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/piagent"
	"temporal-agents/internal/place"
	"temporal-agents/internal/wfrecord"
)

// RunPiAgent runs the Pi agent for req.Prompt in req.WorkDir. The heavy lifting
// (subprocess management, JSON event streaming, and heartbeating) lives in the
// piagent package so it can be shared across workflows. It returns the agent's
// final message alongside the session's total token usage.
func RunPiAgent(ctx context.Context, req PromptRequest) (piagent.Result, error) {
	return piagent.Run(ctx, req.Prompt, req.WorkDir)
}

// Activities is the root activity bundle: the driven adapters PromptWorkflow
// needs beyond the plain RunPiAgent function. It is registered with the worker
// and its Store is injected from main, exactly like the codereview and fleet
// bundles' Git/PRs/Agent adapters.
type Activities struct {
	// Store is the durable execution history port. A nil Store makes the persist
	// activity fail loudly rather than panic, since recording is a hard dependency.
	Store execstore.ExecutionWriter
}

// RunState is the typed input to PersistRunWorkflowState: everything
// PromptWorkflow knows about its own execution. The activity — not the workflow —
// maps it onto the shared executions record, so the type boundary lives in code
// while the schema stays a single table.
type RunState struct {
	// WorkflowID and RunID are the Temporal correlation handles. A --chain run
	// loops via continue-as-new, so each iteration shares the workflow ID and
	// brings its own run ID, which is the key the write upserts on.
	WorkflowID string
	RunID      string
	// FirstRunID identifies one complete execution chain and distinguishes schedule
	// firings that reuse the same workflow ID.
	FirstRunID string
	// ParentWorkflowID is set when this run was started as a child workflow, and
	// empty for the usual top-level run.
	ParentWorkflowID string
	// Prompt is the instruction the run was given.
	Prompt string
	// ScheduleID is the schedule that fired this run, or empty for a direct `run`.
	// A schedule-fired run is not a kind of its own; it is a run attributable to
	// its schedule.
	ScheduleID string
	// StartedAt is when the workflow began, from its deterministic clock.
	StartedAt time.Time
	// Place is where the run runs, as the location probe established it. It is the
	// zero value when nothing could be established, which the read path publishes as
	// the unknown place.
	Place place.Facts
	// EndedAt is when it settled, or the zero time on the initial "started" write.
	EndedAt time.Time
	// Status is StatusRunning on the initial write and the terminal outcome after.
	Status execstore.Status
	// Tokens is this iteration's own token usage, never the inclusive
	// TokensSoFar total carried across a chain, so summing rows cannot
	// double-count.
	Tokens int
	// Error is the failure text when the run failed, so the record says why.
	Error string
}

// PersistRunWorkflowState records a PromptWorkflow execution's state. It is
// called twice per iteration — once when the run starts and once when it settles
// — and each write is an idempotent upsert on the run ID, so a Temporal retry of
// an activity that already committed neither duplicates the row nor corrupts it.
//
// The prompt passes through wfrecord.Sanitize at this boundary, like every other
// free text the record carries: a prompt is operator-written ("use ghp_… to fetch
// the issues" is plausible) and has no length bound of its own, and the record is
// long-lived, so it goes through the one funnel rather than straight into the
// column.
func (a *Activities) PersistRunWorkflowState(ctx context.Context, in RunState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		FirstRunID:       in.FirstRunID,
		Kind:             execstore.KindRun,
		Prompt:           wfrecord.Sanitize(in.Prompt),
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ScheduleID:       in.ScheduleID,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail: execstore.Detail{
			Error:      in.Error,
			Directory:  in.Place.Directory,
			Repository: in.Place.Repository,
		},
	})
}
