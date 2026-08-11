package steeringtemporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/steering"
)

type workflowClientStub struct {
	workflowID string
	runID      string
	signal     string
	value      any
	err        error
}

func (c *workflowClientStub) SignalWorkflow(
	_ context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg any,
) error {
	c.workflowID, c.runID, c.signal, c.value = workflowID, runID, signalName, arg
	return c.err
}

func TestADecisionIsAddressedToTheStableWaitingSession(t *testing.T) {
	client := &workflowClientStub{}
	signaller, err := New(client)
	require.NoError(t, err)
	decision := steering.Decision{Choice: steering.ChoiceGuide, Guidance: "keep the retry", Principal: "ada"}

	err = signaller.SignalDecision(context.Background(), "steering-review-1", decision)

	require.NoError(t, err)
	require.Equal(t, "steering-review-1", client.workflowID)
	require.Empty(t, client.runID, "the stable workflow identity addresses its current run")
	require.Equal(t, steering.DecisionSignal, client.signal)
	require.Equal(t, decision, client.value)
}

func TestASignallerWithoutAnOrchestratorDoesNotBuild(t *testing.T) {
	_, err := New(nil)

	require.Error(t, err)
}
