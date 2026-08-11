// Package wfsetting holds the workflow-side half of setting resolution: the version
// gate that keeps executions started before settings were consulted replayable, the
// one policy every workflow resolves under, and the rule that a unit of work
// resolves once.
//
// It is wfinstruction's twin, and deliberately so: a setting and an instruction
// share the scope chain, the store and the "resolve once per unit of work" rule, so
// the workflow-side halves differ only in which activity they schedule. Both depend
// on the SDK-free catalogue package and the Temporal SDK, so the catalogue stays
// SDK-free.
package wfsetting

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/place"
	"temporal-agents/internal/setting"
)

// resolveChangeID names the workflow change that introduced resolved settings, so
// histories written before it replay unchanged. See Enabled.
const resolveChangeID = "resolve-settings"

// Enabled reports whether the executing workflow resolves its settings from storage.
//
// Resolution adds an activity call to workflows that were already running, so it is
// gated behind a workflow version for the same reason recording, the location probe
// and instruction resolution are (see wfrecord.Enabled, wfplace.Enabled,
// wfinstruction.Enabled): an execution whose history predates the change would
// otherwise replay against code that schedules a resolution its history lacks, and
// fail nondeterministically. The review and pilot loops make this concrete — both
// can be mid-flight across the worker upgrade.
//
// The consequence is deliberate: an execution started before the upgrade carries no
// resolution. Steering treats that absent historical value as off, which is
// byte-for-byte how those executions already behave. New executions resolve the
// current shipped or scoped value before they test it.
func Enabled(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, resolveChangeID, workflow.DefaultVersion, 1) == 1
}

// Ensure returns the settings this unit of work runs under, resolving them once.
//
// resolved is what the unit of work already carries: empty on its first pass, and
// the values the first pass resolved on every later one, because a loop carries them
// across continue-as-new in its own input. Resolving again per pass would let a
// setting switched mid-loop change the loop's shape halfway through — a review loop
// that starts pausing for a human it never told anyone about, or stops pausing while
// an operator is still typing.
//
// where is the place the work runs, as the location probe established it; it decides
// the scope chain (see setting.Chain). Work whose place is unknown still resolves,
// through the installation's values.
//
// Like instruction resolution, this never degrades: a store that cannot answer fails
// the unit of work rather than letting it run under a value nobody chose.
func Ensure(ctx workflow.Context, resolved setting.Resolution, where place.Facts, keys ...setting.Key) (setting.Resolution, error) {
	if !Enabled(ctx) {
		return nil, nil
	}
	if len(resolved) > 0 {
		return resolved, nil
	}
	// The budget matches instruction resolution's and the durable record's: a single
	// indexed read cannot legitimately take longer, and a few minutes of backoff
	// absorbs a Postgres restart before the unit of work is failed for it.
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    10,
		},
	})
	request := setting.Request{Keys: keys, Scopes: setting.Chain(where.Directory, where.Repository)}
	var a *setting.Activity
	var resolution setting.Resolution
	if err := workflow.ExecuteActivity(opts, a.ResolveSettings, request).Get(opts, &resolution); err != nil {
		return nil, fmt.Errorf("resolve the settings this work runs under: %w", err)
	}
	return resolution, nil
}
