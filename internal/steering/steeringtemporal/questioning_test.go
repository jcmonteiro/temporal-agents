package steeringtemporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/steering"
)

type workflowRunStub struct{ err error }

func (r workflowRunStub) GetID() string                  { return "turn-workflow" }
func (r workflowRunStub) GetRunID() string               { return "turn-run" }
func (r workflowRunStub) Get(context.Context, any) error { return r.err }
func (r workflowRunStub) GetWithOptions(context.Context, any, client.WorkflowRunGetOptions) error {
	return r.err
}

type questionClientStub struct {
	options  client.StartWorkflowOptions
	workflow any
	args     []any
	run      client.WorkflowRun
	err      error
}

func (c *questionClientStub) ExecuteWorkflow(
	_ context.Context,
	options client.StartWorkflowOptions,
	workflow any,
	args ...any,
) (client.WorkflowRun, error) {
	c.options, c.workflow, c.args = options, workflow, args
	return c.run, c.err
}

func TestAQuestionTurnRunsOnceOnTheWorkerQueue(t *testing.T) {
	clientStub := &questionClientStub{run: workflowRunStub{}}
	questioner, err := NewQuestioner(clientStub, "agents")
	require.NoError(t, err)
	turn := steering.QuestionTurn{SessionID: "steering-review-1", OperatorSequence: 3, Finish: true}

	err = questioner.RunQuestionTurn(context.Background(), turn)

	require.NoError(t, err)
	require.Equal(t, "agents", clientStub.options.TaskQueue)
	require.Equal(t, "steering-review-1-turn-3", clientStub.options.ID)
	require.NotNil(t, clientStub.workflow)
	require.Equal(t, turn, clientStub.args[0])
}
