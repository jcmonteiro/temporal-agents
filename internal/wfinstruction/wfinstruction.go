// Package wfinstruction holds the workflow-side half of instruction resolution: the
// version gate that keeps executions started before instructions were stored
// replayable, the one policy every workflow resolves under, and the rule that a unit
// of work resolves once.
//
// It depends on the SDK-free instruction port, the place port and the Temporal SDK,
// exactly as wfplace does for the location probe, so both ports stay SDK-free and
// the mapping from "where this work runs" to "the scopes it inherits through" lives
// in one place.
package wfinstruction

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/place"
)

// resolveChangeID names the workflow change that introduced stored instructions, so
// histories written before it replay unchanged. See Enabled.
const resolveChangeID = "resolve-instructions"

// Enabled reports whether the executing workflow resolves its instructions from
// storage.
//
// Resolution adds an activity call to workflows that were already running, so it is
// gated behind a workflow version for the same reason recording and the location
// probe are (see wfrecord.Enabled, wfplace.Enabled): an execution whose history
// predates the change would otherwise replay against code that schedules a
// resolution its history lacks, and fail nondeterministically. The review and pilot
// loops make this concrete — both can be mid-flight across the worker upgrade.
//
// The consequence is deliberate: an execution started before the upgrade keeps using
// the instructions this build ships (see instruction.Resolution.Text), which is
// byte-for-byte what it was already using, and records no provenance. Every
// execution started after it — including the next iteration of a loop, which begins
// a fresh history — resolves and records.
func Enabled(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, resolveChangeID, workflow.DefaultVersion, 1) == 1
}

// Ensure returns the instructions this unit of work uses, resolving them once.
//
// resolved is what the unit of work already carries: empty on its first pass, and
// the values the first pass resolved on every later one, because a loop carries them
// across continue-as-new in its own input. Resolving again per pass would let an
// instruction edited mid-loop change what a later pass did, while the passes already
// recorded name a different version — so the loop would have run under two
// instructions with nothing saying so.
//
// where is the place the work runs, as the location probe established it; it decides
// the scope chain (see instruction.Chain). Work whose place is unknown still
// resolves, through the installation's values.
//
// Unlike the probe, this never degrades: a store that cannot answer fails the unit
// of work. Substituting a default silently would change what the agent is told with
// no record that it happened, which is the one outcome stored instructions exist to
// prevent.
func Ensure(ctx workflow.Context, resolved instruction.Resolution, where place.Facts, keys ...instruction.Key) (instruction.Resolution, error) {
	if !Enabled(ctx) {
		return nil, nil
	}
	if len(resolved) > 0 {
		return resolved, nil
	}
	// The budget matches the durable record's: a single indexed read cannot
	// legitimately take longer, and a few minutes of backoff absorbs a Postgres
	// restart before the unit of work is failed for it.
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    10,
		},
	})
	request := instruction.Request{Keys: keys, Scopes: instruction.Chain(where.Directory, where.Repository)}
	var a *instruction.Activity
	var resolution instruction.Resolution
	if err := workflow.ExecuteActivity(opts, a.ResolveInstructions, request).Get(opts, &resolution); err != nil {
		return nil, fmt.Errorf("resolve the instructions this work runs under: %w", err)
	}
	return resolution, nil
}
