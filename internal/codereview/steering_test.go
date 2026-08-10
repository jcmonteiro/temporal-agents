package codereview

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
	"temporal-agents/internal/scoped/scopedtest"
	"temporal-agents/internal/setting"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

// These tests are about the two pause points: what a loop does when an operator has
// asked to steer it, and — just as important — that a loop nobody asked to steer is
// untouched. They run the real session workflow rather than a stand-in, because
// "the first decision wins" is a fact about the session and the loop together.

// testRunID is the run ID the workflow test environment gives the workflow under
// test. The session's identity is derived from it (see steering.SessionID), so a
// test signals the waiting session by that name.
const testRunID = "default-test-run-id"

// newSteeredEnv builds a loop environment where steering is switched on for every
// place, which is what an operator asking to steer this repository looks like.
func newSteeredEnv(t *testing.T, store *execstoretest.Store, enabled bool) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	return newSteeredEnvReadableAt(t, store, steeringtest.New(), enabled)
}

// newSteeredEnvReadableAt is newSteeredEnv with the steering store in the test's
// hands, for the tests that read what an operator would be shown.
func newSteeredEnvReadableAt(
	t *testing.T,
	store *execstoretest.Store,
	sessions *steeringtest.Store,
	enabled bool,
) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	settings := scopedtest.New()
	settings.Store(setting.KeySteeringEnabled, setting.GlobalScope, setting.Format(enabled))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: store})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	env.RegisterActivity(&instruction.Activity{Store: scopedtest.New()})
	env.RegisterActivity(&setting.Activity{Resolver: setting.Resolver{Store: settings}})
	// The waiting round is written where an operator reads it, so the loop's pause
	// needs the steering store as much as the execution record.
	env.RegisterActivity(&steering.Activities{Store: store, Sessions: sessions})
	env.RegisterWorkflow(steering.SessionWorkflow)
	env.RegisterWorkflow(ReviewWorkflow)
	env.RegisterWorkflow(PilotWorkflow)
	return env
}

// decide sends one decision to the session the loop under test is waiting in,
// after the loop has had time to reach its pause point.
func decide(env *testsuite.TestWorkflowEnvironment, after time.Duration, decisions ...steering.Decision) {
	env.RegisterDelayedCallback(func() {
		for _, decision := range decisions {
			_ = env.SignalWorkflowByID(steering.SessionID(testRunID), steering.DecisionSignal, decision)
		}
	}, after)
}

// A steered round stops before the agent acts, and the operator's words reach the
// pass that implements the review — beside the review, not instead of it.
func TestASteeredReviewRoundImplementsWithTheOperatorsGuidance(t *testing.T) {
	env := newSteeredEnv(t, execstoretest.New(), true)
	var told RunImplementRequest
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { told = args.Get(1).(RunImplementRequest) }).
		Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceGuide, Guidance: "leave the public API alone"})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, "leave the public API alone", told.Guidance)
	require.Equal(t, "rename foo", told.Payload, "the review the guidance applies to is unchanged")
}

// Proceeding without guidance is a decision of its own, and it must leave the pass
// exactly as an unsteered pass would be.
func TestAnOperatorCanLetASteeredRoundProceedWithoutGuidance(t *testing.T) {
	env := newSteeredEnv(t, execstoretest.New(), true)
	var told RunImplementRequest
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { told = args.Get(1).(RunImplementRequest) }).
		Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceSkip})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	require.Empty(t, told.Guidance)
}

// Stopping ends the loop deliberately: nothing is implemented, and the ending says
// a human stopped it rather than that the branch converged.
func TestAnOperatorCanStopASteeredReviewLoop(t *testing.T) {
	store := execstoretest.New()
	env := newSteeredEnv(t, store, true)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceStop, Principal: "ada"})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var outcome ReviewOutcome
	require.NoError(t, env.GetWorkflowResult(&outcome))
	require.Equal(t, EndingOperatorStopped, outcome.Ending)
	require.False(t, outcome.Converged, "a loop a human stopped has not converged")
	env.AssertNotCalled(t, activityName(a.RunImplementAgent), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.MarkHeadAndStash), mock.Anything, mock.Anything)

	terminal := lastReviewRecord(t, store)
	require.Equal(t, string(EndingOperatorStopped), terminal.Detail.Ending)
	require.NotNil(t, terminal.Detail.Converged)
	require.False(t, *terminal.Detail.Converged)
}

// Two tabs, or a retried request, must not implement the review twice.
func TestARepeatedDecisionStartsOneImplementationPass(t *testing.T) {
	env := newSteeredEnv(t, execstoretest.New(), true)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Return(AgentResult{Output: "implemented"}, nil).Once()
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	decide(env, time.Hour,
		steering.Decision{Choice: steering.ChoiceGuide, Guidance: "the first word"},
		steering.Decision{Choice: steering.ChoiceStop},
	)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	env.AssertExpectations(t)
}

// While the round waits, the run itself says it needs input — and no second item
// appears anywhere, because the loop is the work.
func TestAWaitingRoundIsReportedOnTheRunThatIsWaiting(t *testing.T) {
	store := execstoretest.New()
	env := newSteeredEnv(t, store, true)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceSkip})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})
	require.True(t, env.IsWorkflowCompleted())

	var waited, stoppedWaiting bool
	for _, record := range store.Records() {
		if record.Kind != execstore.KindReview {
			continue
		}
		if record.Detail.WaitingSince != nil {
			waited = true
			continue
		}
		if waited {
			stoppedWaiting = true
		}
	}
	require.True(t, waited, "a run waiting for a human must say so while it waits")
	require.True(t, stoppedWaiting, "and must stop saying so once the decision is in")
	require.Nil(t, lastReviewRecord(t, store).Detail.WaitingSince,
		"a settled pass is not waiting for anybody")
}

// An operator cannot decide what they cannot read. The waiting round is written to
// the store the operator's surface reads, with the very material the agent would
// have acted on, and it stops waiting once the decision is in.
func TestAWaitingRoundIsReadableWithTheMaterialItIsAbout(t *testing.T) {
	sessions := steeringtest.New()
	env := newSteeredEnvReadableAt(t, execstoretest.New(), sessions, true)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	env.RegisterDelayedCallback(func() {
		waiting, err := sessions.WaitingSessions(context.Background())
		require.NoError(t, err)
		require.Len(t, waiting, 1, "a paused round must be findable while it waits")
		require.Equal(t, steering.RoundLocalReview, waiting[0].Round)
		require.Equal(t, "rename foo", waiting[0].Material,
			"an operator decides about the review the agent would have acted on")
		require.False(t, waiting[0].OpenedAt.IsZero(), "an unbounded wait has to say since when")
	}, time.Hour)
	decide(env, 2*time.Hour, steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	waiting, err := sessions.WaitingSessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, waiting, "an answered round must stop asking")
	session, err := sessions.Session(context.Background(), steering.SessionID(testRunID))
	require.NoError(t, err)
	require.Equal(t, steering.ChoiceSkip, session.Decision.Choice)
}

// The run that is waiting names the session it waits in, so a surface can go from
// the work an operator sees straight to the question it is asking.
func TestTheRunThatIsWaitingNamesTheSessionItWaitsIn(t *testing.T) {
	store := execstoretest.New()
	env := newSteeredEnv(t, store, true)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceSkip})

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})
	require.True(t, env.IsWorkflowCompleted())

	var named string
	for _, record := range store.Records() {
		if record.Kind == execstore.KindReview && record.Detail.WaitingSince != nil {
			named = record.Detail.WaitingSession
		}
	}
	require.Equal(t, steering.SessionID(testRunID), named)
	require.Empty(t, lastReviewRecord(t, store).Detail.WaitingSession,
		"a settled pass names no session, because it is waiting in none")
}

// A loop nobody asked to steer must behave exactly as it did: no session, no wait,
// and nothing on its record about waiting for a human.
func TestAnUnsteeredRoundNeverStopsForAnybody(t *testing.T) {
	store := execstoretest.New()
	env := newSteeredEnv(t, store, false)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Payload: "rename foo", Pass: 1})

	require.True(t, env.IsWorkflowCompleted())
	for _, record := range store.Records() {
		require.NotEqual(t, execstore.KindSteering, record.Kind, "an unsteered loop opened a session")
		require.Nil(t, record.Detail.WaitingSince)
	}
}

// The remote round pauses too, in the same place relative to the agent: after the
// unresolved comments are known and before anything is committed.
func TestASteeredPilotRoundAddressesCommentsWithTheOperatorsGuidance(t *testing.T) {
	env := newSteeredEnv(t, execstoretest.New(), true)
	pr := PullRequest{Number: 7}
	var told RunAgentRequest
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: []ReviewThread{{ID: "t1", Body: "fix"}}}, nil)
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { told = args.Get(1).(RunAgentRequest) }).
		Return(AgentResult{Output: "done"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReplyAndResolve, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.RequestCopilotReview, mock.Anything, mock.Anything).Return(nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceGuide, Guidance: "ignore the naming comment"})

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "ignore the naming comment", told.Guidance)
}

// Stopping the remote round leaves the comments unresolved and the loop ended by a
// human, rather than chaining into another pass.
func TestAnOperatorCanStopASteeredPilotLoop(t *testing.T) {
	store := execstoretest.New()
	env := newSteeredEnv(t, store, true)
	pr := PullRequest{Number: 7}
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).Return(pr, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).
		Return(LoadCommentsResult{Threads: []ReviewThread{{ID: "t1", Body: "fix"}}}, nil)
	decide(env, time.Hour, steering.Decision{Choice: steering.ChoiceStop})

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo", Chain: true})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a chained pass a human stopped does not continue as new")
	env.AssertNotCalled(t, activityName(a.RunAgent), mock.Anything, mock.Anything)
	env.AssertNotCalled(t, activityName(a.ReplyAndResolve), mock.Anything, mock.Anything)

	var terminal execstore.Execution
	for _, record := range store.Records() {
		if record.Kind == execstore.KindPilot {
			terminal = record
		}
	}
	require.Equal(t, string(EndingOperatorStopped), terminal.Detail.Ending)
}

// lastReviewRecord returns the newest review row written, which is the terminal one.
func lastReviewRecord(t *testing.T, store *execstoretest.Store) execstore.Execution {
	t.Helper()
	var last execstore.Execution
	for _, record := range store.Records() {
		if record.Kind == execstore.KindReview {
			last = record
		}
	}
	require.NotEmpty(t, last.RunID, "the review pass recorded nothing")
	return last
}

// The implement pass is where the operator's words become part of the prompt, so
// this is where the promise is kept: the instruction is what it was, the review is
// what it was, and the guidance is a block of its own in front of the review.
func TestTheImplementAgentIsHandedTheGuidanceInFrontOfTheReview(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	agent := &fakeAgent{output: "implemented"}
	act := &Activities{Agent: agent}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.RunImplementAgent, RunImplementRequest{
		WorkDir: "/repo",
		Payload: "rename the widget",
		Instructions: instruction.Resolution{{
			Key:  instruction.KeyReviewImplement,
			Text: "Act on this review, then commit:\n\n{{.Review}}",
		}},
		Guidance: "leave the public API alone",
	})

	require.NoError(t, err)
	require.Equal(t, "Act on this review, then commit:\n\n"+
		"--- Operator guidance ---\nleave the public API alone\n--- End of operator guidance ---\n"+
		"rename the widget", agent.lastPrompt)
}
