package codereview

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/place"
	"temporal-agents/internal/setting"
	"temporal-agents/internal/steering"
)

// This file holds the two pause points of the review loops: where a round stops for
// the operator, and how the run says it is waiting while it does.
//
// There are exactly two, and both sit immediately before the agent acts on review
// material — after a local review has produced its findings, and after the pull
// request's unresolved comments have been fetched. Pausing there is what makes the
// operator's knowledge arrive in time to change the pass rather than after it; and
// pausing *before* the working tree is checkpointed is what keeps a human's thinking
// time from being spent with the developer's own changes held in a stash.

// steered reports whether this unit of work stops for the operator. The setting is
// resolved once at the loop's start and carried from there, so this is a read of
// what the loop decided when it began, never a fresh question.
//
// A loop that resolved nothing reads the shipped default, which is on: review
// findings wait for an operator unless a scoped setting or run input switches it off.
func steered(settings setting.Resolution) bool {
	return settings.Enabled(setting.KeySteeringEnabled)
}

// pause stops the loop on the operator and returns their decision.
//
// waiting is how the pausing loop reports that it needs a human: it is called with
// the moment the wait began and the session that is waiting, and again with nothing
// once the decision is in. The loop's own record is the only thing that changes — no
// item appears anywhere while a session waits, because the loop is the work and it
// must not be shown twice. The session travels with the wait so an interface can go
// straight from the run that is asking to the question it is asking.
func pause(
	ctx workflow.Context,
	round steering.Round,
	where place.Facts,
	material string,
	recipient string,
	waiting func(since time.Time, session string),
) (steering.Decision, error) {
	session := steering.SessionIDFor(workflow.GetInfo(ctx).WorkflowExecution.RunID, round)
	waiting(workflow.Now(ctx), session)
	decision, err := steering.Ask(ctx, steering.Pause{
		Recipient: recipient, Round: round, Place: where, Material: material,
	})
	// The run stops reporting that it needs input as soon as it stops needing it,
	// including when the session failed: a run nobody can answer must not keep asking.
	waiting(time.Time{}, "")
	if err != nil {
		return steering.Decision{}, err
	}
	return decision, nil
}
