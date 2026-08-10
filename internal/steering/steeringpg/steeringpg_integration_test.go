package steeringpg

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
	"temporal-agents/internal/place"
	"temporal-agents/internal/steering"
)

// waitingRound is a session as the pausing loop opens it.
func waitingRound(id string, opened time.Time) steering.Session {
	return steering.Session{
		ID:       id,
		ItemID:   "review-" + id,
		Round:    steering.RoundLocalReview,
		Material: "The error is swallowed in handler.go",
		Place:    place.Facts{Directory: "/srv/repos/pricing"},
		OpenedAt: opened,
	}
}

// TestOpenRejectsAnEmptyDSN pins the fail-fast contract: a process must not start
// with a steering store it cannot reach, because the first thing an operator would
// learn about it is a review round that failed the moment it tried to pause.
func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	_, err := Open(context.Background(), "   ")
	require.Error(t, err)
}

// TestMigrateIsIdempotent pins that a restart is free.
func TestMigrateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.Migrate(context.Background()))
	require.NoError(t, store.Migrate(context.Background()))
}

// A round that stopped for a human must still be there hours later, with everything
// needed to decide it, however many processes have restarted meanwhile.
func TestAWaitingRoundSurvivesAndSaysWhatItIsAbout(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(context.Background()))
	_, err := first.OpenSession(context.Background(), waitingRound("steering-1", opened))
	require.NoError(t, err)

	// A second process — the API server beside the worker, or the same worker after a
	// restart — reads what was written.
	second := openTestStore(t, dsn)
	waiting, err := second.WaitingSessions(context.Background())

	require.NoError(t, err)
	require.Len(t, waiting, 1)
	require.Equal(t, "The error is swallowed in handler.go", waiting[0].Material)
	require.Equal(t, steering.RoundLocalReview, waiting[0].Round)
	require.Equal(t, "/srv/repos/pricing", waiting[0].Place.Directory)
	require.True(t, waiting[0].OpenedAt.Equal(opened), "an unbounded wait has to say since when")
	require.True(t, waiting[0].Waiting())
}

// The activity that opens a session is replayed. A replay must find the session it
// already opened rather than reopen one that has since been decided.
func TestOpeningASessionTwiceKeepsTheOneThatIsAlreadyThere(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-1", opened))
	require.NoError(t, err)
	_, err = store.RecordDecision(ctx, "steering-1",
		steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"}, opened.Add(time.Hour))
	require.NoError(t, err)

	replayed, err := store.OpenSession(ctx, waitingRound("steering-1", opened.Add(2*time.Hour)))

	require.NoError(t, err)
	require.Equal(t, steering.StateDecided, replayed.State, "a replay must not reopen a decided session")
	require.Equal(t, steering.ChoiceSkip, replayed.Decision.Choice)
	require.True(t, replayed.OpenedAt.Equal(opened))
}

// Two tabs decide at the same moment. Exactly one decision may be recorded, and both
// requests must be answered with that one — a second decision would start a second
// implementation pass.
func TestTwoOperatorsDecidingAtOnceRecordOneDecisionBetweenThem(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	store := openTestStore(t, dsn)
	require.NoError(t, store.Migrate(context.Background()))
	ctx := context.Background()
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-1", opened))
	require.NoError(t, err)

	decisions := []steering.Decision{
		{Choice: steering.ChoiceGuide, Guidance: "leave the logging alone", Principal: "ada"},
		{Choice: steering.ChoiceStop, Principal: "grace"},
	}
	answers := make([]steering.Session, len(decisions))
	failures := make([]error, len(decisions))
	var wait sync.WaitGroup
	for i, decision := range decisions {
		wait.Add(1)
		// Each writer is a connection of its own, which is what makes this a race the
		// database has to settle rather than one the pool serializes.
		writer := openTestStore(t, dsn)
		go func() {
			defer wait.Done()
			answers[i], failures[i] = writer.RecordDecision(ctx, "steering-1", decision, opened.Add(time.Hour))
		}()
	}
	wait.Wait()

	for _, err := range failures {
		require.NoError(t, err, "a repeat is answered, never refused")
	}
	require.Equal(t, answers[0].Decision, answers[1].Decision,
		"both requests must learn the same decision")
	stored, err := store.Session(ctx, "steering-1")
	require.NoError(t, err)
	require.Equal(t, answers[0].Decision, stored.Decision)
	require.True(t, stored.DecidedAt.Equal(opened.Add(time.Hour)))
}

// The guidance the decision carries is the guidance that was stored: two copies
// would eventually disagree, and the agent is handed exactly one of them.
func TestTheGuidanceThatWasDecidedIsTheGuidanceThatIsRead(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-1", opened))
	require.NoError(t, err)

	_, err = store.RecordDecision(ctx, "steering-1", steering.Decision{
		Choice: steering.ChoiceGuide, Guidance: "keep the retry, drop the log line", Principal: "ada",
	}, opened.Add(time.Hour))
	require.NoError(t, err)

	stored, err := store.Session(ctx, "steering-1")
	require.NoError(t, err)
	require.Equal(t, "keep the retry, drop the log line", stored.Guidance)
	require.Equal(t, "keep the retry, drop the log line", stored.Decision.Guidance)
	require.Equal(t, "ada", stored.Decision.Principal)
	require.False(t, stored.Waiting(), "a decided round is no longer waiting for anybody")
	waiting, err := store.WaitingSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, waiting)
}

// A session that was signalled directly — by hand, through orchestration tooling —
// still has to end with what it was told, while a decision already recorded through
// the API is the authoritative one and must not be overwritten.
func TestSettlingASessionKeepsTheDecisionThatWasAlreadyRecorded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"steering-1", "steering-2"} {
		_, err := store.OpenSession(ctx, waitingRound(id, opened))
		require.NoError(t, err)
	}
	_, err := store.RecordDecision(ctx, "steering-1",
		steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"}, opened.Add(time.Hour))
	require.NoError(t, err)

	require.NoError(t, store.CloseSession(ctx, "steering-1",
		steering.Decision{Choice: steering.ChoiceStop, Principal: "worker"}, opened.Add(2*time.Hour)))
	require.NoError(t, store.CloseSession(ctx, "steering-2",
		steering.Decision{Choice: steering.ChoiceStop, Principal: "grace"}, opened.Add(2*time.Hour)))

	answered, err := store.Session(ctx, "steering-1")
	require.NoError(t, err)
	require.Equal(t, steering.ChoiceSkip, answered.Decision.Choice, "the recorded decision wins")
	require.Equal(t, "ada", answered.Decision.Principal)
	signalled, err := store.Session(ctx, "steering-2")
	require.NoError(t, err)
	require.Equal(t, steering.ChoiceStop, signalled.Decision.Choice)
	require.Equal(t, steering.StateDecided, signalled.State)
}

// A loop that was cancelled takes its waiting round with it. The round is recorded
// as abandoned rather than left waiting: a question nobody can answer must stop
// asking, or it would sit on the operator's surface forever.
func TestARoundWhoseLoopIsGoneStopsAsking(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	opened := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-1", opened))
	require.NoError(t, err)

	require.NoError(t, store.CloseSession(ctx, "steering-1", steering.Decision{}, opened.Add(time.Hour)))

	abandoned, err := store.Session(ctx, "steering-1")
	require.NoError(t, err)
	require.Equal(t, steering.StateAbandoned, abandoned.State)
	require.False(t, abandoned.Decision.Made(), "nobody decided it")
	require.True(t, abandoned.DecidedAt.IsZero())
	waiting, err := store.WaitingSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, waiting)
}

// A decision against a session nobody opened is not an outage, and neither is a
// read of one: the surface must be able to say "there is nothing here".
func TestASessionNobodyOpenedIsReportedAsSuch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, decideErr := store.RecordDecision(ctx, "steering-nothing",
		steering.Decision{Choice: steering.ChoiceSkip}, time.Now())
	_, readErr := store.Session(ctx, "steering-nothing")
	_, messagesErr := store.Messages(ctx, "steering-nothing", 0)
	closeErr := store.CloseSession(ctx, "steering-nothing", steering.Decision{Choice: steering.ChoiceSkip}, time.Now())
	_, appendErr := store.AppendMessage(ctx, steering.Message{
		SessionID: "steering-nothing", Role: steering.RoleOperator, Author: "ada", Text: "hello",
	})

	require.ErrorIs(t, decideErr, steering.ErrNoSuchSession)
	require.ErrorIs(t, readErr, steering.ErrNoSuchSession)
	require.ErrorIs(t, messagesErr, steering.ErrNoSuchSession)
	require.ErrorIs(t, closeErr, steering.ErrNoSuchSession)
	require.ErrorIs(t, appendErr, steering.ErrNoSuchSession)
}

// The conversation is what a reload, a second tab and a sleeping laptop resume from,
// so its sequence must be dense and monotonic however many turns arrive at once.
func TestAConversationsSequenceIsDenseAndMonotonicUnderConcurrentAppends(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	store := openTestStore(t, dsn)
	require.NoError(t, store.Migrate(context.Background()))
	ctx := context.Background()
	_, err := store.OpenSession(ctx, waitingRound("steering-1", time.Now().UTC()))
	require.NoError(t, err)

	const turns = 12
	var wait sync.WaitGroup
	failures := make(chan error, turns)
	for i := range turns {
		wait.Add(1)
		writer := openTestStore(t, dsn)
		go func() {
			defer wait.Done()
			_, err := writer.AppendMessage(ctx, steering.Message{
				SessionID: "steering-1",
				Role:      steering.RoleOperator,
				Author:    "ada",
				Text:      fmt.Sprintf("turn %d", i),
			})
			failures <- err
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	messages, err := store.Messages(ctx, "steering-1", 0)
	require.NoError(t, err)
	require.Len(t, messages, turns)
	for i, message := range messages {
		require.Equal(t, int64(i+1), message.Sequence, "sequences must be dense and never reused")
	}

	// A reader that has seen the first half asks for what came after it, and gets
	// exactly that: the stream resumes by sequence, not by time.
	rest, err := store.Messages(ctx, "steering-1", messages[turns/2-1].Sequence)
	require.NoError(t, err)
	require.Len(t, rest, turns/2)
	require.Equal(t, int64(turns/2+1), rest[0].Sequence)
}

// What the questioning cost, and who said it, are read back as they were written:
// the cost is operator-driven and has to be visible while the conversation grows.
func TestATurnKeepsItsAuthorAndItsCost(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-1", at))
	require.NoError(t, err)

	appended, err := store.AppendMessage(ctx, steering.Message{
		SessionID: "steering-1", Role: steering.RoleAgent,
		Text: "why does the handler swallow it?", Tokens: 120, At: at,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), appended.Sequence)

	messages, err := store.Messages(ctx, "steering-1", 0)
	require.NoError(t, err)
	require.Equal(t, steering.RoleAgent, messages[0].Role)
	require.Equal(t, 120, messages[0].Tokens)
	require.Empty(t, messages[0].Author, "the agent's turns have no author")
	require.True(t, messages[0].At.Equal(at))
}

// The rounds waiting longest come first: an unbounded wait must make the oldest
// decision the most visible one.
func TestTheRoundsWaitingLongestComeFirst(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	_, err := store.OpenSession(ctx, waitingRound("steering-newer", now.Add(-time.Hour)))
	require.NoError(t, err)
	_, err = store.OpenSession(ctx, waitingRound("steering-older", now.Add(-4*time.Hour)))
	require.NoError(t, err)

	waiting, err := store.WaitingSessions(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"steering-older", "steering-newer"}, ids(waiting))
}

// ids renders the sessions' identities for an assertion.
func ids(sessions []steering.Session) []string {
	out := make([]string, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.ID)
	}
	return out
}
