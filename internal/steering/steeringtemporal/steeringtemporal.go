// Package steeringtemporal adapts the orchestration client to steering's decision
// signal port.
package steeringtemporal

import (
	"context"
	"errors"
	"fmt"

	"temporal-agents/internal/steering"
)

// WorkflowClient is the one orchestration operation this adapter needs.
type WorkflowClient interface {
	SignalWorkflow(
		ctx context.Context,
		workflowID string,
		runID string,
		signalName string,
		arg any,
	) error
}

// Signaller delivers a durable decision to the session that waits for it.
type Signaller struct {
	client WorkflowClient
}

// New builds a decision signaller.
func New(client WorkflowClient) (*Signaller, error) {
	if client == nil {
		return nil, errors.New("the orchestration client is required")
	}
	return &Signaller{client: client}, nil
}

// SignalDecision addresses the current run of the stable session workflow. An empty
// run ID is deliberate: a worker restart must not make a recorded decision target a
// stale run.
func (s *Signaller) SignalDecision(
	ctx context.Context,
	sessionID string,
	decision steering.Decision,
) error {
	if err := s.client.SignalWorkflow(ctx, sessionID, "", steering.DecisionSignal, decision); err != nil {
		return fmt.Errorf("signal steering session %s: %w", sessionID, err)
	}
	return nil
}

var _ steering.DecisionSignaller = (*Signaller)(nil)
