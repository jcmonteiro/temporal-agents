package steering

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
)

// The session tests exercise observable behaviour — what the session returns, what
// it records, and what it does with a second decision — against the real recording
// activity over an in-memory store. Nothing here mocks the session's own halves.

func newSessionEnv(t *testing.T, store *execstoretest.Store) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterWorkflow(SessionWorkflow)
	return env
}

// The three decisions are the session's whole vocabulary, and each is returned to
// the loop verbatim: the loop decides what to do about it, the session does not.
type notificationCapture struct {
	mu    sync.Mutex
	items []notification.Notification
}

func (c *notificationCapture) Notify(_ context.Context, item notification.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, item)
	return nil
}

func TestRemindersRepeatDailyWithoutACapAndStopOnDecision(t *testing.T) {
	env := newSessionEnv(t, execstoretest.New())
	capture := &notificationCapture{}
	env.RegisterActivity(&notification.Activity{Notifier: capture})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceSkip})
	}, 3*24*time.Hour+time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{ItemID: "review-1", Recipient: "ada", Round: RoundLocalReview})

	require.NoError(t, env.GetWorkflowError())
	capture.mu.Lock()
	defer capture.mu.Unlock()
	require.Len(t, capture.items, 4, "the first notice plus one reminder per full day")
	for _, item := range capture.items {
		require.Equal(t, "ada", item.Recipient)
	}
}

func TestASessionReturnsTheDecisionThatWasSent(t *testing.T) {
	cases := []struct {
		name string
		sent Decision
	}{
		{"guidance travels with the decision", Decision{Choice: ChoiceGuide, Guidance: "keep the API", Principal: "ada"}},
		{"proceeding without guidance", Decision{Choice: ChoiceSkip, Principal: "ada"}},
		{"stopping the loop", Decision{Choice: ChoiceStop, Principal: "ada"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newSessionEnv(t, execstoretest.New())
			env.RegisterDelayedCallback(func() {
				env.SignalWorkflow(DecisionSignal, tc.sent)
			}, time.Hour)

			env.ExecuteWorkflow(SessionWorkflow, SessionInput{Round: RoundLocalReview})

			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			var got Decision
			require.NoError(t, env.GetWorkflowResult(&got))
			require.Equal(t, tc.sent, got)
		})
	}
}

// Two tabs, or a retried request, must not decide twice: whatever arrives after the
// first decision is dropped, and the decision that won is what the session reports.
func TestTheFirstDecisionWins(t *testing.T) {
	env := newSessionEnv(t, execstoretest.New())
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceGuide, Guidance: "first"})
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceStop})
	}, time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{Round: RoundLocalReview})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got Decision
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, Decision{Choice: ChoiceGuide, Guidance: "first"}, got)
}

// A decision that claims guidance and carries none is not a decision. The session
// keeps waiting for a real one instead of failing, because the operator is still
// there and a failed session would take the paused loop down with it.
func TestADecisionThatClaimsGuidanceAndCarriesNoneIsNotADecision(t *testing.T) {
	env := newSessionEnv(t, execstoretest.New())
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceGuide})
	}, time.Hour)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceSkip})
	}, 2*time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{Round: RoundLocalReview})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got Decision
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, ChoiceSkip, got.Choice)
}

// Nothing the tool owns may end the wait. The session is still waiting after a week
// of workflow time, and says so, because the only thing that ends it is a human.
func TestTheWaitIsUnbounded(t *testing.T) {
	env := newSessionEnv(t, execstoretest.New())
	env.RegisterDelayedCallback(func() {
		var state SessionState
		value, err := env.QueryWorkflow(DecisionQuery)
		require.NoError(t, err)
		require.NoError(t, value.Get(&state))
		require.True(t, state.Waiting, "a week of waiting is still waiting")
		require.False(t, state.Decision.Made())
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceSkip})
	}, 7*24*time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{Round: RoundLocalReview})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// The session is recorded as an execution of its own so what it costs is
// attributable to it — and it records where the paused work runs, so its row lands
// in the same place as the loop it paused.
func TestASessionRecordsItselfAsAnExecutionOfItsOwn(t *testing.T) {
	store := execstoretest.New()
	env := newSessionEnv(t, store)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceGuide, Guidance: "keep the API", Principal: "ada"})
	}, time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{
		Round: RoundRemoteComments,
		Place: place.Facts{Directory: "/src/agents"},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	records := store.Records()
	require.Len(t, records, 2, "the session records itself when it starts waiting and when it settles")

	start := records[0]
	require.Equal(t, execstore.KindSteering, start.Kind)
	require.Equal(t, execstore.StatusRunning, start.Status)
	require.Equal(t, string(RoundRemoteComments), start.Detail.Round)
	require.Equal(t, "/src/agents", start.Detail.Directory)
	require.Empty(t, start.Detail.Decision, "a waiting session has decided nothing")

	end := records[1]
	require.Equal(t, start.RunID, end.RunID, "both writes key on the run ID, so the second upserts the first")
	require.Equal(t, execstore.StatusSucceeded, end.Status)
	require.Equal(t, string(ChoiceGuide), end.Detail.Decision)
	require.Equal(t, "ada", end.Detail.Principal)
	require.NotContains(t, end.Detail.Decision, "keep the API",
		"the guidance is the round's input, not something the session's row copies")
}

// The session's kind is deliberately not one the overview draws items from: the
// loop it paused is the work, and one piece of work must not appear twice.
func TestASessionIsNeverAnItemOfItsOwn(t *testing.T) {
	store := execstoretest.New()
	env := newSessionEnv(t, store)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(DecisionSignal, Decision{Choice: ChoiceSkip})
	}, time.Hour)

	env.ExecuteWorkflow(SessionWorkflow, SessionInput{Round: RoundLocalReview})
	require.True(t, env.IsWorkflowCompleted())

	chains, err := store.ListExecutionChains(t.Context(), execstore.ChainFilter{
		Kinds: []execstore.Kind{
			execstore.KindRun, execstore.KindDevelop, execstore.KindReview,
			execstore.KindPilot, execstore.KindFleetPlan, execstore.KindFleet,
		},
	})
	require.NoError(t, err)
	require.Empty(t, chains, "no collection an operator reads may contain a steering session")
}
