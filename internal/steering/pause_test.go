package steering_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/place"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

// Pausing a loop has two halves that must not drift apart: the wait itself, and the
// row an operator reads to answer it. These tests drive both through the real
// activity over the in-memory store, because "there is something to find while the
// loop waits" is the whole promise of the pause.

// theRunID is the run ID the workflow test environment gives the workflow under
// test. The session's identity is derived from it.
const theRunID = "default-test-run-id"

// pausingLoop stands in for a review round: it pauses once and returns what the
// operator decided.
func pausingLoop(ctx workflow.Context, material string) (steering.Decision, error) {
	return steering.Ask(ctx, steering.Pause{
		Round:    steering.RoundLocalReview,
		Place:    place.Facts{Directory: "/srv/repos/pricing"},
		Material: material,
	})
}

func newPauseEnv(t *testing.T, sessions *steeringtest.Store) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&steering.Activities{Store: execstoretest.New(), Sessions: sessions})
	env.RegisterWorkflow(steering.SessionWorkflow)
	env.RegisterWorkflow(pausingLoop)
	return env
}

// The round is readable the whole time it waits: opened before anything waits on it,
// carrying what the decision is about, and settled with the decision that won.
func TestAPausedRoundIsStoredBeforeItWaitsAndSettledWhenItIsAnswered(t *testing.T) {
	sessions := steeringtest.New()
	env := newPauseEnv(t, sessions)
	env.RegisterDelayedCallback(func() {
		waiting, err := sessions.WaitingSessions(context.Background())
		require.NoError(t, err)
		require.Len(t, waiting, 1)
		require.Equal(t, "the error is swallowed", waiting[0].Material)
		require.Equal(t, "/srv/repos/pricing", waiting[0].Place.Directory)
		require.Equal(t, steering.RoundLocalReview, waiting[0].Round)
		require.False(t, waiting[0].OpenedAt.IsZero())
	}, time.Hour)
	env.RegisterDelayedCallback(func() {
		_ = env.SignalWorkflowByID(steering.SessionID(theRunID), steering.DecisionSignal,
			steering.Decision{Choice: steering.ChoiceGuide, Guidance: "keep the retry", Principal: "ada"})
	}, 2*time.Hour)

	env.ExecuteWorkflow(pausingLoop, "the error is swallowed")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	settled, err := sessions.Session(context.Background(), steering.SessionID(theRunID))
	require.NoError(t, err)
	require.Equal(t, steering.StateDecided, settled.State)
	require.Equal(t, steering.ChoiceGuide, settled.Decision.Choice)
	require.Equal(t, "ada", settled.Decision.Principal)
}

// A decision recorded through the operator's surface is the authoritative one. The
// session settles with what was recorded, never with a second copy of it.
func TestSettlingKeepsTheDecisionTheOperatorsSurfaceRecorded(t *testing.T) {
	sessions := steeringtest.New()
	env := newPauseEnv(t, sessions)
	env.RegisterDelayedCallback(func() {
		// This is what the API does: record first, then resume the waiting round.
		_, err := sessions.RecordDecision(context.Background(), steering.SessionID(theRunID),
			steering.Decision{Choice: steering.ChoiceSkip, Principal: "grace"}, time.Now())
		require.NoError(t, err)
		_ = env.SignalWorkflowByID(steering.SessionID(theRunID), steering.DecisionSignal,
			steering.Decision{Choice: steering.ChoiceSkip, Principal: "grace"})
	}, time.Hour)

	env.ExecuteWorkflow(pausingLoop, "the error is swallowed")

	require.True(t, env.IsWorkflowCompleted())
	settled, err := sessions.Session(context.Background(), steering.SessionID(theRunID))
	require.NoError(t, err)
	require.Equal(t, "grace", settled.Decision.Principal)
	require.Equal(t, steering.ChoiceSkip, settled.Decision.Choice)
}

// A pause that cannot be written is a loop that would wait for a decision nobody
// could ever find, so it fails instead of waiting.
func TestARoundThatCannotBeStoredDoesNotWaitForAnybody(t *testing.T) {
	sessions := steeringtest.New()
	sessions.Failure = steeringtest.ErrStoreDown
	env := newPauseEnv(t, sessions)

	env.ExecuteWorkflow(pausingLoop, "the error is swallowed")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(),
		"a round waiting in a session nobody can read is a loop stuck with no visible cause")
}
