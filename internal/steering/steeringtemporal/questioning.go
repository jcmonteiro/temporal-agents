package steeringtemporal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/client"

	"temporal-agents/internal/steering"
)

// QuestionClient is the one orchestration operation a bounded questioning turn
// needs.
type QuestionClient interface {
	ExecuteWorkflow(
		ctx context.Context,
		options client.StartWorkflowOptions,
		workflow any,
		args ...any,
	) (client.WorkflowRun, error)
}

// Questioner starts one turn on the worker queue and waits until its durable writes
// complete.
type Questioner struct {
	client    QuestionClient
	taskQueue string
}

// NewQuestioner builds the adapter used by the HTTP-facing service.
func NewQuestioner(client QuestionClient, taskQueue string) (*Questioner, error) {
	if client == nil {
		return nil, errors.New("the orchestration client is required")
	}
	if strings.TrimSpace(taskQueue) == "" {
		return nil, errors.New("the worker task queue is required")
	}
	return &Questioner{client: client, taskQueue: taskQueue}, nil
}

// RunQuestionTurn starts one uniquely named turn and waits for its agent output to be
// durable before the operator receives success.
func (q *Questioner) RunQuestionTurn(ctx context.Context, turn steering.QuestionTurn) error {
	workflowID := fmt.Sprintf("%s-turn-%d", turn.SessionID, turn.OperatorSequence)
	run, err := q.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: q.taskQueue,
	}, steering.QuestionTurnWorkflow, turn)
	if err != nil {
		return fmt.Errorf("start questioning turn: %w", err)
	}
	if err := run.Get(ctx, nil); err != nil {
		return fmt.Errorf("wait for questioning turn: %w", err)
	}
	return nil
}

var _ steering.Questioner = (*Questioner)(nil)
