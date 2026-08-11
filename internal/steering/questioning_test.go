package steering_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

type questionerFake struct {
	store *steeringtest.Store
	err   error
	turns []steering.QuestionTurn
}

func (q *questionerFake) RunQuestionTurn(ctx context.Context, turn steering.QuestionTurn) error {
	q.turns = append(q.turns, turn)
	if q.err != nil {
		return q.err
	}
	text := "What constraint makes the retry necessary?"
	if turn.Finish {
		text = "Keep the retry because callers depend on the original error."
	}
	_, err := q.store.AppendMessage(ctx, steering.Message{
		SessionID: turn.SessionID,
		Role:      steering.RoleAgent,
		Text:      text,
		Tokens:    75,
		At:        time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC),
	})
	if err != nil {
		return err
	}
	if turn.Finish {
		return q.store.SetGuidance(ctx, turn.SessionID, text)
	}
	return nil
}

func TestAnOperatorCanAskOneQuestioningTurnAtATime(t *testing.T) {
	store := steeringtest.New().WithSession(theRound(time.Now()))
	questioner := &questionerFake{store: store}
	service := newService(t, store, time.Now())
	service.Questioner = questioner

	conversation, err := service.Question(context.Background(), "steering-review-1",
		steering.QuestionRequest{Text: "Question me", Principal: "ada"})

	require.NoError(t, err)
	require.Len(t, questioner.turns, 1)
	require.Equal(t, int64(1), questioner.turns[0].OperatorSequence)
	require.Equal(t, []steering.Role{steering.RoleOperator, steering.RoleAgent}, []steering.Role{
		conversation.Messages[0].Role, conversation.Messages[1].Role,
	})
	require.Equal(t, "ada", conversation.Messages[0].Author)
	require.Equal(t, 75, conversation.Tokens())
}

func TestFinishingQuestioningProducesEditableGuidance(t *testing.T) {
	store := steeringtest.New().WithSession(theRound(time.Now()))
	questioner := &questionerFake{store: store}
	service := newService(t, store, time.Now())
	service.Questioner = questioner

	conversation, err := service.Question(context.Background(), "steering-review-1",
		steering.QuestionRequest{Text: "That is enough", Principal: "ada", Finish: true})

	require.NoError(t, err)
	require.Equal(t, "Keep the retry because callers depend on the original error.", conversation.Session.Guidance)
	require.True(t, conversation.Session.Waiting(), "a draft is not a decision")
}

func TestAQuestioningFailureDoesNotPreventANonConversationalDecision(t *testing.T) {
	store := steeringtest.New().WithSession(theRound(time.Now()))
	service := newService(t, store, time.Now())
	service.Questioner = &questionerFake{store: store, err: errors.New("agent unavailable")}

	_, questionErr := service.Question(context.Background(), "steering-review-1",
		steering.QuestionRequest{Text: "Question me", Principal: "ada"})
	_, decisionErr := service.Decide(context.Background(), "steering-review-1",
		steering.Decision{Choice: steering.ChoiceSkip, Principal: "ada"})

	require.ErrorIs(t, questionErr, steering.ErrQuestioningUnavailable)
	require.NoError(t, decisionErr, "skip, direct guidance, and stop do not depend on questioning")
}
