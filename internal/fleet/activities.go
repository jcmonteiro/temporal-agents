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

// errPlanningMutatedRepo is the error type returned (non-retryable) when the
// read-only planning contract's tripwire fires: the source repository changed
// while planning ran. Retrying cannot undo a mutation, so it fails fast.
const errPlanningMutatedRepo = "PlanningMutatedRepo"

// Activities bundles the driven adapters the fleet workflows orchestrate. It is
// registered with the Temporal worker; each exported method is an activity.
type Activities struct {
	Agent Agent
	Git   Git
}

// ResolveBaseRequest is the input to ResolveBase.
type ResolveBaseRequest struct {
	// WorkDir is the repository directory whose HEAD is read as the fleet base.
	WorkDir string
}

// ResolveBase reads the repository's current HEAD so the fleet can pin every
// node's worktree to that single commit. Capturing the base once when a run
// starts (rather than letting each child branch off whatever HEAD points at
// when it happens to start) keeps the dependency graph ordering-only: a later
// layer branches from the same base as the first even if the user moves the
// checkout, and never inherits a prerequisite node's commits.
func (a *Activities) ResolveBase(ctx context.Context, req ResolveBaseRequest) (string, error) {
	head, err := a.Git.Head(ctx, req.WorkDir)
	if err != nil {
		return "", fmt.Errorf("read repository base: %w", err)
	}
	return head, nil
}

// GeneratePlanRequest is the input to GeneratePlan.
type GeneratePlanRequest struct {
	// Goal is the high-level change to decompose into a dependency graph.
	Goal string
	// WorkDir is the repository directory the agent inspects while planning.
	WorkDir string
}

// GeneratePlanResult is the output of GeneratePlan: the parsed, validated plan
// and the token usage the read-only planning agent spent producing it, kept
// separate from FleetPlan so the plan written to disk is not polluted with
// run-specific accounting.
type GeneratePlanResult struct {
	// Plan is the parsed, validated dependency graph.
	Plan FleetPlan
	// Tokens is the planning agent session's total token usage.
	Tokens int
}

// GeneratePlan drives the Pi agent to decompose the goal into a dependency
// graph and returns the parsed, validated plan. Parsing and validation run here
// (rather than in the workflow) so a malformed graph is a non-retryable activity
// failure with a clear message.
//
// Planning is contracted to be read-only, and that contract is enforced rather
// than merely requested: a prompt cannot stop an agent (or one of its bash tool
// calls) from editing or committing files. Two mechanisms enforce it. First, the
// agent runs against a disposable, detached worktree — a throwaway copy of the
// repository the user's working tree, branch, and index never see — created and
// removed here. Second, the run uses a read-only tool policy (RunReadOnly) that
// denies the file-mutating tools outright. Finally a tripwire re-reads the source
// repository and fails non-retryably if it changed, so a plan is never returned
// from a run that escaped the sandbox.
func (a *Activities) GeneratePlan(ctx context.Context, req GeneratePlanRequest) (GeneratePlanResult, error) {
	// Snapshot the source repository's complete content up front so the tripwire
	// below can confirm planning left it exactly where it started. A content
	// fingerprint (not a dirty/clean flag) is required: a repository that starts
	// dirty must still detect a mutation to an already-modified file, which a
	// boolean comparison would miss because both snapshots would read "dirty".
	before, err := a.Git.Fingerprint(ctx, req.WorkDir)
	if err != nil {
		return GeneratePlanResult{}, fmt.Errorf("read repository state: %w", err)
	}

	// Run the agent against a disposable copy so it operates in isolation. Always
	// discard it, even on failure, so a planning run leaves no worktree behind.
	sandbox, err := a.Git.AddDisposableWorktree(ctx, req.WorkDir)
	if err != nil {
		return GeneratePlanResult{}, fmt.Errorf("create planning sandbox: %w", err)
	}
	defer func() { _ = a.Git.RemoveWorktree(ctx, req.WorkDir, sandbox) }()

	out, tokens, err := a.Agent.RunReadOnly(ctx, BuildPlanPrompt(req.Goal), sandbox)
	if err != nil {
		return GeneratePlanResult{}, err
	}

	// Tripwire: the sandbox should have absorbed any changes, so the source repo
	// must be where it started. A mismatch means the read-only contract was
	// violated; fail non-retryably rather than return a plan produced by a run that
	// touched the user's repository.
	after, err := a.Git.Fingerprint(ctx, req.WorkDir)
	if err != nil {
		return GeneratePlanResult{}, fmt.Errorf("verify repository state: %w", err)
	}
	if after != before {
		return GeneratePlanResult{}, temporal.NewNonRetryableApplicationError(
			"planning changed the repository despite its read-only contract", errPlanningMutatedRepo, nil)
	}

	plan, err := ParsePlan(out)
	if err != nil {
		return GeneratePlanResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("could not parse a fleet plan from the agent output: %v", err), errInvalidPlan, nil)
	}
	// The agent restates the goal, but keep the caller's original goal as the
	// authoritative one when the agent leaves it blank.
	if plan.Goal == "" {
		plan.Goal = req.Goal
	}
	if err := ValidatePlan(plan); err != nil {
		return GeneratePlanResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("the agent produced an invalid fleet plan: %v", err), errInvalidPlan, nil)
	}
	return GeneratePlanResult{Plan: plan, Tokens: tokens}, nil
}
