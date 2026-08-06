package codereview

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"temporal-agents/internal/execstore"
)

// The recording tests drive the real Persist<Type>WorkflowState activities
// against an in-memory stand-in for the execstore port, so what they assert on is
// the record that was written rather than which activity happened to be called.
// The port is a single method over plain record types, which makes a fake cheap
// and far more revealing than a mock here.

// fakeStore is an in-memory execstore.Store. Setting err makes every write fail,
// standing in for a store outage.
type fakeStore struct {
	mu    sync.Mutex
	saved []execstore.Execution
	err   error
}

func (f *fakeStore) SaveExecution(_ context.Context, e execstore.Execution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, e)
	return nil
}

func (f *fakeStore) ListExecutions(_ context.Context, _ execstore.Filter) ([]execstore.Execution, error) {
	return f.records(), nil
}

// records returns the executions written so far, in write order.
func (f *fakeStore) records() []execstore.Execution {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execstore.Execution{}, f.saved...)
}

// last returns the most recent write, which for a settled workflow is its
// terminal record.
func (f *fakeStore) last(t *testing.T) execstore.Execution {
	t.Helper()
	recs := f.records()
	require.NotEmpty(t, recs, "expected the workflow to record its state")
	return recs[len(recs)-1]
}

// storeFor picks the store an env constructor was given, or a fresh one when a
// test does not care about the records. The env constructors take it as a
// variadic parameter so the many tests that are not about recording stay
// untouched.
func storeFor(opts []*fakeStore) execstore.Store {
	if len(opts) > 0 {
		return opts[0]
	}
	return &fakeStore{}
}

func TestDevelopWorkflow_RecordsStartAndTerminalState(t *testing.T) {
	store := &fakeStore{}
	env := newDevelopEnv(t, store)

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
	recs := store.records()
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
	store := &fakeStore{}
	env := newDevelopEnv(t, store)

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
	end := store.last(t)
	require.Equal(t, "https://github.com/o/r/pull/7", end.Detail.PRURL)
	require.Equal(t, 500, end.Tokens, "the pipeline's children report their own usage")
}

func TestDevelopWorkflow_Failure_RecordsFailedState(t *testing.T) {
	store := &fakeStore{}
	env := newDevelopEnv(t, store)

	env.OnActivity(a.CreateBranch, mock.Anything, mock.Anything).
		Return(CreateBranchResult{}, errors.New("working tree is dirty"))

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	end := store.last(t)
	require.Equal(t, execstore.StatusFailed, end.Status)
	require.Contains(t, end.Detail.Error, "working tree is dirty")
	require.False(t, end.EndedAt.IsZero())
}

func TestDevelopWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	// Recording is a hard dependency, not best-effort: a store that cannot be
	// written fails the workflow rather than letting it run unrecorded.
	store := &fakeStore{err: errors.New("postgres is down")}
	env := newDevelopEnv(t, store)

	env.ExecuteWorkflow(DevelopWorkflow, DevelopInput{WorkDir: "/repo", Prompt: "do the thing"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	// The record comes first, so an unrecordable run never touches the repository.
	env.AssertNotCalled(t, "CreateBranch", mock.Anything, mock.Anything)
}

func TestReviewWorkflow_Converged_RecordsOwnTokensAndConvergence(t *testing.T) {
	store := &fakeStore{}
	env := newReviewEnv(t, store)

	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented", Tokens: 300}, nil)
	// No new commits: the implement pass found nothing left to change, so the loop
	// has converged.
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("no commits", errNoAdvance, nil))

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "feedback", Pass: 2, TokensSoFar: 9000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.last(t)
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
	store := &fakeStore{}
	env := newReviewEnv(t, store)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more feedback", Tokens: 50}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.last(t)
	require.NotNil(t, end.Detail.Converged)
	require.False(t, *end.Detail.Converged, "stopping at the pass cap is explicitly not convergence")
	require.Equal(t, 50, end.Tokens)
}

func TestReviewWorkflow_ContinuedPass_RecordsItselfAsSucceededWithoutDecidingConvergence(t *testing.T) {
	store := &fakeStore{}
	env := newReviewEnv(t, store)

	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "feedback", Tokens: 120}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	end := store.last(t)
	// Continuing as new is a control signal: this pass did its work and settled, and
	// the next pass is a row of its own. Convergence is not yet decided, so it must
	// not be recorded as "did not converge".
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Nil(t, end.Detail.Converged)
	require.Equal(t, 120, end.Tokens)
}

func TestReviewWorkflow_ChildReviewRecordsItsParent(t *testing.T) {
	store := &fakeStore{}
	env := newDevelopEnv(t, store)

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
	for _, r := range store.records() {
		if r.Kind == execstore.KindReview {
			reviews = append(reviews, r)
		}
	}
	require.NotEmpty(t, reviews)
	require.Equal(t, "default-test-workflow-id", reviews[0].ParentWorkflowID)
}

func TestPilotWorkflow_RecordsPassOutcomeWithPRAndOwnTokens(t *testing.T) {
	store := &fakeStore{}
	env := newEnv(t, store)

	pr := PullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", HeadRef: "feat/x"}
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	// No unresolved comments: the pass addressed nothing and ends the chain.
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).Return(LoadCommentsResult{}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", TokensSoFar: 5000})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	end := store.last(t)
	require.Equal(t, execstore.KindPilot, end.Kind)
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, "https://github.com/o/r/pull/7", end.Detail.PRURL)
	require.NotNil(t, end.Detail.Addressed)
	require.False(t, *end.Detail.Addressed)
	// The 5,000 tokens carried in from earlier passes belong to those passes' rows.
	require.Zero(t, end.Tokens)
}

func TestPilotWorkflow_RecordingFailure_FailsTheWorkflow(t *testing.T) {
	store := &fakeStore{err: errors.New("postgres is down")}
	env := newEnv(t, store)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	env.AssertNotCalled(t, "DeterminePR", mock.Anything, mock.Anything)
}
