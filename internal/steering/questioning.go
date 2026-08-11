package steering

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/instruction"
)

// QuestioningAgent is the driven port for one read-only exchange. sessionID is
// stable across every turn and is the Pi conversation identity.
type QuestioningAgent interface {
	RunQuestioningTurn(
		ctx context.Context,
		prompt string,
		directory string,
		sessionID string,
	) (output string, tokens int, err error)
}

// QuestionTurnWorkflow bounds one exchange. The Pi adapter heartbeats throughout
// the activity, so worker loss retries the same stable conversational session rather
// than losing a partially completed turn.
func QuestionTurnWorkflow(ctx workflow.Context, turn QuestionTurn) error {
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})
	var activities *Activities
	if err := workflow.ExecuteActivity(opts, activities.RunQuestioningTurn, turn).Get(opts, nil); err != nil {
		return fmt.Errorf("complete the questioning turn: %w", err)
	}
	return nil
}

// ConversationTokens returns the agent cost accumulated in the authoritative
// transcript. The long-lived session reads this number only when it settles, so no
// conversation text enters its history.
func (a *Activities) ConversationTokens(ctx context.Context, sessionID string) (int, error) {
	if a.Conversation == nil {
		return 0, nil
	}
	messages, err := a.Conversation.Messages(ctx, sessionID, 0)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, message := range messages {
		total += message.Tokens
	}
	return total, nil
}

// conversationTokens reads only the accumulated number into orchestration history.
func conversationTokens(ctx workflow.Context, sessionID string) (int, error) {
	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    10,
		},
	})
	var activities *Activities
	var tokens int
	if err := workflow.ExecuteActivity(opts, activities.ConversationTokens, sessionID).Get(opts, &tokens); err != nil {
		return 0, fmt.Errorf("read the questioning token usage: %w", err)
	}
	return tokens, nil
}

// RunQuestioningTurn runs one exchange and appends its output. Its input references
// the operator message by sequence; the transcript and review material stay in the
// steering store and do not enter orchestration history.
func (a *Activities) RunQuestioningTurn(ctx context.Context, turn QuestionTurn) error {
	if a.Conversation == nil {
		return ErrNoStore
	}
	if a.QuestioningAgent == nil {
		return ErrQuestioningUnavailable
	}
	session, err := a.Conversation.Session(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	if !session.Waiting() {
		return fmt.Errorf("%w: the steering session is no longer waiting", ErrInvalidMessage)
	}
	messages, err := a.Conversation.Messages(ctx, turn.SessionID, 0)
	if err != nil {
		return err
	}
	operator, firstAgentTurn, err := operatorTurn(messages, turn.OperatorSequence)
	if err != nil {
		return err
	}

	prompt := operator.Text
	if firstAgentTurn {
		if a.Instructions == nil {
			return instruction.ErrNotConfigured
		}
		resolution, err := (&instruction.Activity{Store: a.Instructions}).ResolveInstructions(ctx,
			instruction.Request{
				Keys:   []instruction.Key{instruction.KeySteeringQuestion},
				Scopes: instruction.Chain(session.Place.Directory, session.Place.Repository),
			})
		if err != nil {
			return fmt.Errorf("resolve the questioning instruction: %w", err)
		}
		governed, err := instruction.Render(resolution, instruction.KeySteeringQuestion,
			instruction.Data{"Material": session.Material})
		if err != nil {
			return fmt.Errorf("render the questioning instruction: %w", err)
		}
		prompt = governed + "\n\nOperator: " + operator.Text
	}
	if turn.Finish {
		prompt += "\n\nFinish the questioning now. Return only the editable guidance draft."
	}

	output, tokens, err := a.QuestioningAgent.RunQuestioningTurn(
		ctx, prompt, session.Place.Directory, turn.SessionID,
	)
	if err != nil {
		return fmt.Errorf("run the questioning agent: %w", err)
	}
	if strings.TrimSpace(output) == "" {
		return errors.New("the questioning agent returned no text")
	}
	if _, err := a.Conversation.AppendMessage(ctx, Message{
		SessionID: turn.SessionID,
		Role:      RoleAgent,
		Text:      output,
		Tokens:    tokens,
		At:        time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("record the questioning agent's answer: %w", err)
	}
	if turn.Finish {
		if err := a.Conversation.SetGuidance(ctx, turn.SessionID, output); err != nil {
			return fmt.Errorf("save the guidance draft: %w", err)
		}
	}
	return nil
}

// operatorTurn finds the one stored turn the workflow input references and reports
// whether the questioning agent has answered before it.
func operatorTurn(messages []Message, sequence int64) (Message, bool, error) {
	firstAgentTurn := true
	var operator Message
	for _, message := range messages {
		if message.Role == RoleAgent {
			firstAgentTurn = false
		}
		if message.Sequence == sequence {
			operator = message
		}
	}
	if operator.Sequence == 0 || operator.Role != RoleOperator {
		return Message{}, false, fmt.Errorf("%w: sequence %d is not an operator turn",
			ErrInvalidMessage, sequence)
	}
	return operator, firstAgentTurn, nil
}
