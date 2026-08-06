package codereview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
)

// The recording tests drive the real Persist<Type>WorkflowState activities
// against execstoretest.Store, an in-memory stand-in for the execstore port, so
// what they assert on is the record that was written rather than which activity
// happened to be called.

func TestDevelopWorkflow_RecordsStartAndTerminalState(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvWithStore(t, store)

	// The branch is auto-generated, so the start record cannot name it; the
	// terminal record must carry the branch actually developed on.
	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "flaming-duck", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done", Tokens: 700}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).
		Return(ReviewOutcome{Summary: "reviewed", Converged: true}, nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	recs := store.Records()
	require.Len(t, recs, 2)

	start := recs[0]
	require.Equal(t, execstore.KindDevelop, start.Kind)
	require.Equal(t, execstore.StatusRunning, start.Status)
	require.Equal(t, "do the thing", start.Prompt)
	require.False(t, start.StartedAt.IsZero())
	require.True(t, start.EndedAt.IsZero())

	end := recs[1]
	require.Equal(t, start.RunID, end.RunID, "both writes key on the run ID, so the second upserts the first")
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.False(t, end.EndedAt.IsZero())
	require.Equal(t, "flaming-duck", end.Detail.Branch)
	// Only the develop step's own usage: the review child records its own row, so
	// summing rows must not count the same tokens twice.
	require.Equal(t, 700, end.Tokens)
}

func TestDevelopWorkflow_WithRemote_RecordsThePRURL(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvWithStore(t, store)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done", Tokens: 500}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).
		Return(ReviewOutcome{Summary: "reviewed", Converged: true}, nil)
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).
		Return(OpenPRResult{Summary: "PR #7 is open", URL: "https://github.com/o/r/pull/7"}, nil)
	env.OnWorkflow(PilotWorkflow, mock.Anything, mock.Anything).Return("piloted", nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do it", WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Open-pr is not an execution of its own: it runs only inside this pipeline, so
	// its outcome is folded into the develop record.
	end := store.Last(t)
	require.Equal(t, "https://github.com/o/r/pull/7", end.Detail.PRURL)
	require.Equal(t, 500, end.Tokens, "the pipeline's children report their own usage")
}

func TestDevelopWorkflow_Failure_RecordsFailedState(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvWithStore(t, store)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{}, errors.New("working tree is dirty"))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	end := store.Last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	require.Contains(t, end.Detail.Error, "working tree is dirty")
	require.False(t, end.EndedAt.IsZero())
}

func TestDevelopWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	// Recording is a hard dependency, not best-effort: a store that cannot be
	// written fails the workflow rather than letting it run unrecorded.
	store := execstoretest.Failing(errors.New("postgres is down"))
	env := newDevelopEnvWithStore(t, store)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	// The record comes first, so an unrecordable run never touches the repository.
	env.AssertNotCalled(t, "CreateBranch", mock.Anything, mock.Anything)
}

func TestReviewWorkflow_Converged_RecordsOwnTokensAndConvergence(t *testing.T) {
	store := execstoretest.New()
	env := newReviewEnvWithStore(t, store)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented", Tokens: 300}, nil)
	// No new commits: the implement pass found nothing left to change, so the loop
	// has converged.
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "feedback", Pass: 2, TokensSoFar: 9000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.Last(t)
	require.Equal(t, execstore.KindReview, end.Kind)
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, 2, end.Detail.Pass)
	require.NotNil(t, end.Detail.Converged)
	require.True(t, *end.Detail.Converged)
	// This pass's own usage only, not the 9,000 tokens carried in from earlier
	// passes and its parent develop run.
	require.Equal(t, 300, end.Tokens)
}

func TestReviewWorkflow_PassCapped_RecordsThatItDidNotConverge(t *testing.T) {
	store := execstoretest.New()
	env := newReviewEnvWithStore(t, store)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more feedback", Tokens: 50}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.Last(t)
	require.NotNil(t, end.Detail.Converged)
	require.False(t, *end.Detail.Converged, "stopping at the pass cap is explicitly not convergence")
	require.Equal(t, 50, end.Tokens)
}

func TestReviewWorkflow_ContinuedPass_RecordsItselfAsSucceededWithoutDecidingConvergence(t *testing.T) {
	store := execstoretest.New()
	env := newReviewEnvWithStore(t, store)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "feedback", Tokens: 120}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	end := store.Last(t)
	// Continuing as new is a control signal: this pass did its work and settled, and
	// the next pass is a row of its own. Convergence is not yet decided, so it must
	// not be recorded as "did not converge".
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Nil(t, end.Detail.Converged)
	require.Equal(t, 120, end.Tokens)
}

func TestReviewWorkflow_ChildReviewRecordsItsParent(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvWithStore(t, store)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	// Let the real review child run (capped at one pass) so it records itself.
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "feedback"}, nil)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{
		WorkDir: "/repo", Branch: "feat/x", Prompt: "do it", AwaitReview: true})

	require.True(t, env.IsWorkflowCompleted())
	// A child review carries its parent's workflow ID, which is what tells it apart
	// from a standalone `code review` and makes the develop→review tree
	// reconstructable.
	var reviews []execstore.Execution
	for _, r := range store.Records() {
		if r.Kind == execstore.KindReview {
			reviews = append(reviews, r)
		}
	}
	require.NotEmpty(t, reviews)
	require.Equal(t, "default-test-workflow-id", reviews[0].ParentWorkflowID)
}

func TestPilotWorkflow_RecordsPassOutcomeWithPRAndOwnTokens(t *testing.T) {
	store := execstoretest.New()
	env := newEnvWithStore(t, store)

	pr := PullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", HeadRef: "feat/x"}
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	// No unresolved comments: the pass addressed nothing and ends the chain.
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).Return(LoadCommentsResult{}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", TokensSoFar: 5000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.Last(t)
	require.Equal(t, execstore.KindPilot, end.Kind)
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, "https://github.com/o/r/pull/7", end.Detail.PRURL)
	require.NotNil(t, end.Detail.Addressed)
	require.False(t, *end.Detail.Addressed)
	// The 5,000 tokens carried in from earlier passes belong to those passes' rows.
	require.Zero(t, end.Tokens)
}

func TestPilotWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	store := execstoretest.Failing(errors.New("postgres is down"))
	env := newEnvWithStore(t, store)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	env.AssertNotCalled(t, "DeterminePR", mock.Anything, mock.Anything)
}

func TestDevelopWorkflow_WithRemote_FailedPipelineStillRecordsTheOpenedPR(t *testing.T) {
	store := execstoretest.New()
	env := newDevelopEnvWithStore(t, store)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{Branch: "feat/x", WorkDir: "/repo", BaseSHA: "base"}, nil)
	env.OnActivity(a.RunDevelopAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureDeveloped, mock.Anything, mock.Anything).Return([]string{"sha1"}, nil)
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).
		Return(ReviewOutcome{Summary: "reviewed", Converged: true}, nil)
	env.OnWorkflow(OpenPRWorkflow, mock.Anything, mock.Anything).
		Return(OpenPRResult{Summary: "PR #7 is open", URL: "https://github.com/o/r/pull/7"}, nil)
	// The pipeline fails after the PR was opened.
	env.OnWorkflow(PilotWorkflow, mock.Anything, mock.Anything).Return("", errors.New("pilot exploded"))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Branch: "feat/x", Prompt: "do it", WithRemote: true})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	end := store.Last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	// A pipeline that failed after opening the PR still points at it, so the record
	// leads somewhere useful.
	require.Equal(t, "https://github.com/o/r/pull/7", end.Detail.PRURL)
	require.Contains(t, end.Detail.Error, "pilot exploded")
}

func TestPilotWorkflow_FailedPass_LeavesAddressedUndecided(t *testing.T) {
	store := execstoretest.New()
	env := newEnvWithStore(t, store)

	// The pass fails before it ever learns whether there are comments to address.
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).
		Return(PullRequest{}, errors.New("no open PR for this branch"))

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	end := store.Last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	// "Addressed nothing" and "never reached that decision" are different facts, so
	// a pass that failed first must not be recorded as having decided anything.
	require.Nil(t, end.Detail.Addressed)
	require.Contains(t, end.Detail.Error, "no open PR")
}
