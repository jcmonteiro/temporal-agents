package steering_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/scoped/scopedtest"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

func TestAQuestioningExchangeRunsAsOneBoundedWorkflow(t *testing.T) {
	store := steeringtest.New().WithSession(theRound(time.Now()))
	operator, err := store.AppendMessage(t.Context(), steering.Message{
		SessionID: "steering-review-1", Role: steering.RoleOperator,
		Author: "ada", Text: "Question me", At: time.Now(),
	})
	require.NoError(t, err)
	agent := &questioningAgentFake{output: "Which callers need the original error?", tokens: 42}
	config := scopedtest.New()
	config.Store(instruction.KeySteeringQuestion, instruction.GlobalScope,
		instruction.Resolution{}.Text(instruction.KeySteeringQuestion))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(steering.QuestionTurnWorkflow)
	env.RegisterActivity(&steering.Activities{
		Conversation: store, Instructions: config, QuestioningAgent: agent,
	})

	env.ExecuteWorkflow(steering.QuestionTurnWorkflow, steering.QuestionTurn{
		SessionID: "steering-review-1", OperatorSequence: operator.Sequence,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	messages, err := store.Messages(t.Context(), "steering-review-1", 0)
	require.NoError(t, err)
	require.Len(t, messages, 2)
}
