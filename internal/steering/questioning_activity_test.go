package steering_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/place"
	"temporal-agents/internal/scoped/scopedtest"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

type questioningAgentFake struct {
	prompts     []string
	directories []string
	sessions    []string
	models      []string
	output      string
	tokens      int
	err         error
}

func (a *questioningAgentFake) RunQuestioningTurn(
	_ context.Context,
	prompt string,
	directory string,
	sessionID string,
	model string,
) (string, int, error) {
	a.prompts = append(a.prompts, prompt)
	a.directories = append(a.directories, directory)
	a.sessions = append(a.sessions, sessionID)
	a.models = append(a.models, model)
	return a.output, a.tokens, a.err
}

func TestAQuestioningTurnUsesTheGovernedPromptAndWritesItsOutput(t *testing.T) {
	ctx := context.Background()
	opened := time.Now()
	sessions := steeringtest.New().WithSession(steering.Session{
		ID: "steering-review-1", ItemID: "review-1", Round: steering.RoundLocalReview,
		Material: "the retry hides the error", Place: place.Facts{Directory: "/src/pricing"},
		OpenedAt: opened, State: steering.StateWaiting,
	})
	operator, err := sessions.AppendMessage(ctx, steering.Message{
		SessionID: "steering-review-1", Role: steering.RoleOperator, Author: "ada", Text: "Question me", At: opened,
	})
	require.NoError(t, err)
	config := scopedtest.New()
	config.Store(instruction.KeySteeringQuestion, instruction.GlobalScope,
		"Ask one useful question. {{.Material}}")
	agent := &questioningAgentFake{output: "Why must callers see the original error?", tokens: 120}
	activities := &steering.Activities{Conversation: sessions, Instructions: config, QuestioningAgent: agent}

	err = activities.RunQuestioningTurn(ctx, steering.QuestionTurn{
		SessionID: "steering-review-1", OperatorSequence: operator.Sequence,
	})

	require.NoError(t, err)
	require.Len(t, agent.prompts, 1)
	require.Contains(t, agent.prompts[0], "Ask one useful question")
	require.Contains(t, agent.prompts[0], "the retry hides the error")
	require.Contains(t, agent.prompts[0], "Question me")
	require.Equal(t, []string{"/src/pricing"}, agent.directories)
	require.Equal(t, []string{"steering-review-1"}, agent.sessions,
		"every exchange must resume the same conversational identity")
	conversation, err := sessions.Messages(ctx, "steering-review-1", 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), conversation[1].Sequence)
	require.Equal(t, 120, conversation[1].Tokens)
}

func TestFinishingATurnWritesTheAgentOutputAsGuidance(t *testing.T) {
	ctx := context.Background()
	sessions := steeringtest.New().WithSession(steering.Session{
		ID: "steering-review-1", ItemID: "review-1", Round: steering.RoundLocalReview,
		Material: "review", OpenedAt: time.Now(), State: steering.StateWaiting,
	})
	operator, err := sessions.AppendMessage(ctx, steering.Message{
		SessionID: "steering-review-1", Role: steering.RoleOperator, Author: "ada", Text: "Finish", At: time.Now(),
	})
	require.NoError(t, err)
	agent := &questioningAgentFake{output: "Keep the retry and preserve the wrapped cause.", tokens: 90}
	activities := &steering.Activities{Conversation: sessions, Instructions: scopedtest.New(), QuestioningAgent: agent}

	err = activities.RunQuestioningTurn(ctx, steering.QuestionTurn{
		SessionID: "steering-review-1", OperatorSequence: operator.Sequence, Finish: true,
	})

	require.NoError(t, err)
	require.True(t, strings.Contains(agent.prompts[0], "guidance draft"))
	session, err := sessions.Session(ctx, "steering-review-1")
	require.NoError(t, err)
	require.Equal(t, "Keep the retry and preserve the wrapped cause.", session.Guidance)
}

func TestAFailedQuestioningTurnAppendsNoAgentAnswer(t *testing.T) {
	ctx := context.Background()
	sessions := steeringtest.New().WithSession(steering.Session{
		ID: "steering-review-1", ItemID: "review-1", Round: steering.RoundLocalReview,
		Material: "review", OpenedAt: time.Now(), State: steering.StateWaiting,
	})
	operator, err := sessions.AppendMessage(ctx, steering.Message{
		SessionID: "steering-review-1", Role: steering.RoleOperator, Author: "ada", Text: "Question me", At: time.Now(),
	})
	require.NoError(t, err)
	activities := &steering.Activities{
		Conversation: sessions, Instructions: scopedtest.New(),
		QuestioningAgent: &questioningAgentFake{err: errors.New("model unavailable")},
	}

	err = activities.RunQuestioningTurn(ctx, steering.QuestionTurn{
		SessionID: "steering-review-1", OperatorSequence: operator.Sequence,
	})

	require.Error(t, err)
	messages, readErr := sessions.Messages(ctx, "steering-review-1", 0)
	require.NoError(t, readErr)
	require.Len(t, messages, 1)
}
