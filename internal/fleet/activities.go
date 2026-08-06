package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/temporal"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/wfrecord"
)

// errInvalidPlan is the error type returned (non-retryable) when the agent's
// output cannot be parsed into a well-formed plan. Retrying the parse of the
// same output can never succeed, so it fails fast rather than exhausting the
// activity's attempts.
const errInvalidPlan = "InvalidPlan"

// errMissingWorktreesDir is the error type returned (non-retryable) when a fleet
// run is started without a WorktreesDir. It is required so parallel nodes never
// share a working tree; an empty value would send every child through the
// in-place branch path against the same WorkDir, so it fails fast rather than
// letting concurrent children mutate one working tree.
const errMissingWorktreesDir = "MissingWorktreesDir"

// errPlanningMutatedRepo is the error type returned (non-retryable) when the
// read-only planning contract's tripwire fires: the source repository changed
// while planning ran. Retrying cannot undo a mutation, so it fails fast.
const errPlanningMutatedRepo = "PlanningMutatedRepo"

// errPlanTooLarge is the error type returned (non-retryable) when the generated
// plan's document exceeds execstore.MaxPlanDocument. Storing the same oversized
// document again cannot succeed, so it fails fast.
const errPlanTooLarge = "PlanTooLarge"

// Activities bundles the driven adapters the fleet workflows orchestrate. It is
// registered with the Temporal worker; each exported method is an activity.
type Activities struct {
	Agent Agent
	Git   Git
	// Store is the durable execution history port driven by the
	// Persist<Type>WorkflowState activities in recording.go. A nil Store makes them
	// fail loudly rather than panic, since recording is a hard dependency.
	Store execstore.ExecutionWriter
	// Plans is the fleet plan store: where an approved plan is kept so it can be
	// reviewed and executed by handle. It is authoritative, not a cache — a write
	// that fails aborts planning rather than being swallowed.
	Plans execstore.PlanStore
}

// ResolveBaseRequest is the input to ResolveBase.
type ResolveBaseRequest struct {
	// WorkDir is the repository directory whose HEAD is read as the fleet base.
	WorkDir string
}

// ResolveBase reads the repository's current HEAD so the fleet can pin every
// node's worktree to that single commit. Capturing the base once when a run
// starts (rather than letting each child branch off whatever HEAD points at
// when it happens to start) gives every node a stable start point: a later
// layer branches from the same base as the first even if the user moves the
// checkout while earlier layers run. Each node's branch is then seeded by
// merging its dependency branches on top of this base, so a dependent inherits
// its prerequisites' committed work while its start point stays fixed.
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
// calls) from editing or committing files. Three mechanisms enforce it. First,
// the agent runs against a disposable, standalone clone — a throwaway copy with
// its own independent .git (refs and object database) the user's repository
// never sees — created and removed here. Because the clone shares no git storage
// with the source, nothing the agent does (including a bash-driven git
// branch/tag/commit) can reach the source repository's working tree, branch,
// index, refs, or objects. Second, the run uses a read-only tool policy
// (RunReadOnly) that denies the file-mutating tools outright. Finally a tripwire
// re-reads the source repository and fails non-retryably if it changed, so a
// plan is never returned from a run that escaped the sandbox.
//
// The clone's isolation is what makes ref and object writes impossible to leak;
// the tripwire therefore only needs to cover the source repository's working
// tree, index, and HEAD content, which the fingerprint captures.
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

	// Run the agent against a disposable clone so it operates in isolation. Always
	// discard it, even on failure, so a planning run leaves no sandbox behind.
	sandbox, err := a.Git.AddDisposableClone(ctx, req.WorkDir)
	if err != nil {
		return GeneratePlanResult{}, fmt.Errorf("create planning sandbox: %w", err)
	}
	defer func() { _ = a.Git.RemoveDisposableClone(ctx, sandbox) }()

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

// StorePlanRequest is the input to StorePlan.
type StorePlanRequest struct {
	// PlanID is the generated handle to store the plan under. It is generated by
	// the caller (not here) so the write is deterministic under retry: an activity
	// re-run stores the same plan under the same handle rather than minting a
	// second one.
	PlanID string
	// Name is the optional operator-chosen label, display-only metadata.
	Name string
	// Plan is the approved graph to store.
	Plan FleetPlan
}

// StorePlan persists a generated plan under its handle, making the store the sole
// source of truth for it (there is no plan file). The encoding to JSON happens
// here so the store keeps the plan opaque and the plan's schema stays owned by
// this core rather than by the database.
//
// The write is an idempotent upsert on the handle, so a retried activity that had
// already committed neither duplicates the plan nor changes its handle. A failure
// is surfaced, never swallowed: a plan that was not stored cannot be executed
// later, so planning must fail loudly instead of reporting a handle that resolves
// to nothing.
//
// The plan is agent-generated, so neither its goal nor its size is bounded by
// anything the CLI typed. The goal goes through the same redact-and-cap funnel as
// every other stored free text, while the document — which must stay decodable, so
// it can be neither trimmed nor rewritten — is size-guarded instead of capped.
func (a *Activities) StorePlan(ctx context.Context, req StorePlanRequest) error {
	if a.Plans == nil {
		return execstore.ErrNotConfigured
	}
	document, err := json.Marshal(req.Plan)
	if err != nil {
		return fmt.Errorf("encode fleet plan: %w", err)
	}
	if len(document) > execstore.MaxPlanDocument {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("the generated fleet plan is %d bytes, over the %d the store accepts",
				len(document), execstore.MaxPlanDocument), errPlanTooLarge, nil)
	}
	return a.Plans.SavePlan(ctx, execstore.Plan{
		ID:       req.PlanID,
		Name:     req.Name,
		Goal:     wfrecord.Sanitize(req.Plan.Goal),
		Nodes:    len(req.Plan.Nodes),
		Document: document,
	})
}
