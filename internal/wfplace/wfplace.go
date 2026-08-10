// Package wfplace holds the workflow-side half of the location probe: the version
// gate that keeps executions started before the probe existed replayable, and the
// one policy every workflow probes under. It depends on both the SDK-free place
// port and the Temporal SDK, exactly as wfnotify does for notifications, so the
// port itself stays SDK-free.
package wfplace

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/place"
)

// probeChangeID names the workflow change that introduced the location probe, so
// histories written before it replay unchanged. See Enabled.
const probeChangeID = "probe-location"

// Enabled reports whether the executing workflow probes where it runs.
//
// The probe adds an activity call to workflows that were already running, so it is
// gated behind a workflow version for the same reason recording is (see
// wfrecord.Enabled): an execution whose history predates the change would otherwise
// replay against code that schedules a probe its history lacks, and fail
// nondeterministically. Long-lived executions make this concrete — a chained run, a
// schedule and the pilot loop can all be in flight across the worker upgrade.
//
// The consequence is deliberate and harmless: an execution started before the
// upgrade never reports where it ran, which is the unknown place — the same answer
// an honest failed probe gives. Every execution started after it (including the next
// iteration of a chain, which begins a fresh history) reports its place.
func Enabled(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, probeChangeID, workflow.DefaultVersion, 1) == 1
}

// Probe establishes where work in dir runs, and never fails the workflow that asks.
//
// A place is bookkeeping: it decides how work is grouped on screen, and nothing
// about the work itself. So a probe that cannot answer — no git, no repository, a
// worker wired without a prober — is logged and answered with nothing established,
// which every consumer reads as the unknown place. Guessing (from the path, from a
// sibling execution) is the one thing this must not do.
//
// The budget is deliberately small: a couple of git commands cannot legitimately
// take longer, and a handful of attempts absorbs a transient failure without
// letting a bookkeeping step hold up an agent run.
func Probe(ctx workflow.Context, dir string) place.Facts {
	if !Enabled(ctx) {
		return place.Facts{}
	}
	if dir == "" {
		// Nothing to ask about. Scheduling the activity would spend a workflow task to
		// be told so.
		return place.Facts{}
	}
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    3,
		},
	})
	var a *place.Activity
	var facts place.Facts
	if err := workflow.ExecuteActivity(opts, a.Probe, dir).Get(opts, &facts); err != nil {
		workflow.GetLogger(ctx).Warn(
			"could not establish where this work runs; it stays in the unknown place", "error", err)
		return place.Facts{}
	}
	return facts
}
