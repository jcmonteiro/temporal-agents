package fleet

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// errInvalidPlan is the error type returned (non-retryable) when the agent's
// output cannot be parsed into a well-formed plan. Retrying the parse of the
// same output can never succeed, so it fails fast rather than exhausting the
// activity's attempts.
const errInvalidPlan = "InvalidPlan"

// Activities bundles the driven adapters the fleet workflows orchestrate. It is
// registered with the Temporal worker; each exported method is an activity.
type Activities struct {
	Agent Agent
}

// GeneratePlanRequest is the input to GeneratePlan.
type GeneratePlanRequest struct {
	// Goal is the high-level change to decompose into a dependency graph.
	Goal string
	// WorkDir is the repository directory the agent inspects while planning.
	WorkDir string
}

// GeneratePlan drives the Pi agent to decompose the goal into a dependency
// graph and returns the parsed, validated plan. Parsing and validation run here
// (rather than in the workflow) so a malformed graph is a non-retryable activity
// failure with a clear message. The agent makes no code changes; it only reads
// the repository to inform the decomposition.
func (a *Activities) GeneratePlan(ctx context.Context, req GeneratePlanRequest) (FleetPlan, error) {
	out, _, err := a.Agent.Run(ctx, BuildPlanPrompt(req.Goal), req.WorkDir)
	if err != nil {
		return FleetPlan{}, err
	}
	plan, err := ParsePlan(out)
	if err != nil {
		return FleetPlan{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("could not parse a fleet plan from the agent output: %v", err), errInvalidPlan, nil)
	}
	// The agent restates the goal, but keep the caller's original goal as the
	// authoritative one when the agent leaves it blank.
	if plan.Goal == "" {
		plan.Goal = req.Goal
	}
	if err := ValidatePlan(plan); err != nil {
		return FleetPlan{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("the agent produced an invalid fleet plan: %v", err), errInvalidPlan, nil)
	}
	return plan, nil
}
