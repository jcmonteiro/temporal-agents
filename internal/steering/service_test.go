package steering_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

// What an operator's surface can do with a waiting round, tested against the fake
// store rather than a database: everything here is a rule of this context — validate,
// record, resume, and never report a decision that was not written.

// theRound is a session waiting for somebody, as the store holds it.
func theRound(opened time.Time) steering.Session {
	return steering.Session{
		ID:       "steering-review-1",
		ItemID:   "review-1",
		Round:    steering.RoundLocalReview,
		Material: "The error is swallowed in handler.go",
		OpenedAt: opened,
		State:    steering.StateWaiting,
	}
}

func newService(t *testing.T, store *steeringtest.Store, now time.Time) *steering.Service {
	t.Helper()
	service, err := steering.NewService(store, store)
	require.NoError(t, err)
	service.Now = func() time.Time { return now }
	return service
}

// The decision is durable before the loop moves. Recording it after would leave a
// pass running that nothing can prove was steered, and a store outage would then be
// discovered only by an operator wondering what happened to their guidance.
func TestADecisionIsRecordedBeforeTheRoundIsResumed(t *testing.T) {
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := steeringtest.New().WithSession(theRound(opened))
	decided := opened.Add(2 * time.Hour)
	service := newService(t, store, decided)

	session, err := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceGuide, Guidance: "leave the logging alone", Principal: "ada"})

	require.NoError(t, err)
	require.Equal(t, steering.StateDecided, session.State)
	require.Equal(t, steering.ChoiceGuide, session.Decision.Choice)
	require.Equal(t, "ada", session.Decision.Principal, "a contribution records who made it")
	require.Equal(t, decided, session.DecidedAt)
	require.Equal(t, []steeringtest.Delivery{{
		SessionID: "steering-review-1",
		Decision:  steering.Decision{Choice: steering.ChoiceGuide, Guidance: "leave the logging alone", Principal: "ada"},
	}}, store.Deliveries())
}

// Two browser tabs and a retried request are normal. The second decision must be
// answered with the first one rather than refused — and must not change what the
// loop was told.
func TestARepeatedDecisionReturnsTheOneThatWasRecorded(t *testing.T) {
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := steeringtest.New().WithSession(theRound(opened))
	service := newService(t, store, opened.Add(time.Hour))
	first := steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"}
	_, err := service.Decide(context.Background(), "steering-review-1", first)
	require.NoError(t, err)

	repeat, err := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceStop, Principal: "grace"})

	require.NoError(t, err, "a repeat is normal, not an error")
	require.Equal(t, first, repeat.Decision, "the first decision wins")
	deliveries := store.Deliveries()
	require.Len(t, deliveries, 2, "a repeat is delivered again, so a lost signal still lands")
	require.Equal(t, first, deliveries[1].Decision, "the waiting round is never told a second decision")
}

// An empty guidance block is indistinguishable from a mistake, and an over-long one
// must be refused with the bound named rather than truncated: truncation drops the
// sentences the operator cared enough to add last.
func TestADecisionThatCannotBeActedOnIsRefusedWithTheReason(t *testing.T) {
	cases := []struct {
		name     string
		decision steering.Decision
		says     string
	}{
		{"guiding with no guidance", steering.Decision{Choice: steering.ChoiceGuide}, string(steering.ChoiceSkip)},
		{"an unknown choice", steering.Decision{Choice: "maybe"}, "maybe"},
		{
			"guidance past the bound",
			steering.Decision{Choice: steering.ChoiceGuide, Guidance: strings.Repeat("x", steering.MaxGuidanceLength+1)},
			"limit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
			store := steeringtest.New().WithSession(theRound(opened))
			service := newService(t, store, opened)

			_, err := service.Decide(context.Background(), "steering-review-1", tc.decision)

			require.ErrorIs(t, err, steering.ErrInvalidDecision)
			require.Contains(t, err.Error(), tc.says)
			require.Empty(t, store.Deliveries(), "a refused decision resumes nothing")
			session, readErr := store.Session(context.Background(), "steering-review-1")
			require.NoError(t, readErr)
			require.True(t, session.Waiting(), "a refused decision leaves the round waiting")
		})
	}
}

// A decision the store did not take is not a decision. It fails in front of the
// operator, and the loop stays where it was.
func TestADecisionThatCannotBeRecordedFailsAndResumesNothing(t *testing.T) {
	store := steeringtest.New().WithSession(theRound(time.Now()))
	store.Failure = steeringtest.ErrStoreDown
	service := newService(t, store, time.Now())

	_, err := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"})

	require.ErrorIs(t, err, steering.ErrUnavailable)
	require.Empty(t, store.Deliveries())
}

// The record survives a delivery that fails, so the operator is told to send it
// again and the repeat resumes the round with what was already recorded.
func TestADecisionThatCannotBeDeliveredIsReportedAndCanBeSentAgain(t *testing.T) {
	opened := time.Now()
	store := steeringtest.New().WithSession(theRound(opened))
	store.SignalFailure = steeringtest.ErrStoreDown
	service := newService(t, store, opened)
	decision := steering.Decision{Choice: steering.ChoiceGuide, Guidance: "rename the port", Principal: "ada"}

	_, err := service.Decide(context.Background(), "steering-review-1", decision)
	require.ErrorIs(t, err, steering.ErrUnavailable)

	store.SignalFailure = nil
	again, err := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceStop, Principal: "ada"})
	require.NoError(t, err)
	require.Equal(t, decision, again.Decision)
	require.Equal(t, decision, store.Deliveries()[0].Decision)
}

// A session nobody opened is not an outage: the surface answers "there is nothing
// here" rather than "something is broken".
func TestAnUnknownSessionIsNotAnOutage(t *testing.T) {
	service := newService(t, steeringtest.New(), time.Now())

	_, decideErr := service.Decide(context.Background(), "steering-nothing",
		steering.Decision{Choice: steering.ChoiceSkip})
	_, readErr := service.Conversation(context.Background(), "steering-nothing")

	require.ErrorIs(t, decideErr, steering.ErrNoSuchSession)
	require.ErrorIs(t, readErr, steering.ErrNoSuchSession)
}

// A decision names the session it answers.
func TestADecisionWithNoSessionIsRefused(t *testing.T) {
	service := newService(t, steeringtest.New(), time.Now())

	_, err := service.Decide(context.Background(), "  ", steering.Decision{Choice: steering.ChoiceSkip})

	require.ErrorIs(t, err, steering.ErrInvalidDecision)
}

// The round that has been waiting longest is the one most in need of an answer, so
// that is the one a reader sees first.
func TestTheRoundsWaitingLongestAreListedFirst(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	older := theRound(now.Add(-3 * time.Hour))
	newer := theRound(now.Add(-time.Hour))
	newer.ID, newer.ItemID = "steering-review-2", "review-2"
	answered := theRound(now.Add(-5 * time.Hour))
	answered.ID, answered.State = "steering-review-0", steering.StateDecided
	store := steeringtest.New().WithSession(newer).WithSession(older).WithSession(answered)
	service := newService(t, store, now)

	waiting, err := service.Waiting(context.Background())

	require.NoError(t, err)
	require.Len(t, waiting, 2, "a session that has been answered is no longer waiting")
	require.Equal(t, older.ID, waiting[0].ID)
	require.Equal(t, newer.ID, waiting[1].ID)
}

// What the decision is about, what it has cost so far, and who has taken part are
// one read: an operator deciding must not have to assemble them.
func TestOneSessionReadsAsWhatItIsAboutAndWhatItHasCost(t *testing.T) {
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := steeringtest.New().WithSession(theRound(opened))
	ctx := context.Background()
	for _, message := range []steering.Message{
		{SessionID: "steering-review-1", Role: steering.RoleOperator, Author: "ada", Text: "question me", At: opened},
		{SessionID: "steering-review-1", Role: steering.RoleAgent, Text: "why keep the handler?", Tokens: 120, At: opened},
		{SessionID: "steering-review-1", Role: steering.RoleOperator, Author: "grace", Text: "it is load bearing", At: opened},
	} {
		_, err := store.AppendMessage(ctx, message)
		require.NoError(t, err)
	}
	service := newService(t, store, opened)

	conversation, err := service.Conversation(ctx, "steering-review-1")

	require.NoError(t, err)
	require.Equal(t, "The error is swallowed in handler.go", conversation.Session.Material)
	require.Equal(t, 120, conversation.Tokens(), "only the questioning costs anything")
	require.Equal(t, []string{"ada", "grace"}, conversation.Contributors())
	require.Equal(t, []int64{1, 2, 3}, sequences(conversation.Messages), "the conversation is read in order")
}

// A session that is answered lists who decided it among its contributors, even when
// they never wrote a turn: proceeding without guidance is a contribution.
func TestTheOperatorWhoDecidedIsAContributor(t *testing.T) {
	opened := time.Now()
	store := steeringtest.New().WithSession(theRound(opened))
	service := newService(t, store, opened)
	_, err := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceSkip, Principal: "grace"})
	require.NoError(t, err)

	conversation, err := service.Conversation(context.Background(), "steering-review-1")

	require.NoError(t, err)
	require.Equal(t, []string{"grace"}, conversation.Contributors())
}

// A surface that could read a waiting decision but never deliver one would offer an
// operator a button that quietly does nothing.
func TestAServiceThatCannotResumeTheRoundDoesNotBuild(t *testing.T) {
	_, noStore := steering.NewService(nil, steeringtest.New())
	_, noSignals := steering.NewService(steeringtest.New(), nil)

	require.Error(t, noStore)
	require.Error(t, noSignals)
}

// sequences renders the messages' sequences for an assertion.
func sequences(messages []steering.Message) []int64 {
	out := make([]int64, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Sequence)
	}
	return out
}
