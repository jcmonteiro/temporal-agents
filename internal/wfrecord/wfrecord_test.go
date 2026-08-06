package wfrecord

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
)

func TestStatusOf_SuccessAndFailure(t *testing.T) {
	require.Equal(t, execstore.StatusSucceeded, StatusOf(nil))
	require.Equal(t, execstore.StatusFailed, StatusOf(errors.New("pi crashed")))
}

func TestStatusOf_ContinueAsNewIsASuccessfulIteration(t *testing.T) {
	// Continuing as new is a control signal, not a failure: the iteration emitting
	// it did its own work and settled, and the next iteration is a row of its own.
	err := fmt.Errorf("wrapped: %w", &workflow.ContinueAsNewError{})

	require.Equal(t, execstore.StatusSucceeded, StatusOf(err))
	require.True(t, IsContinueAsNew(err))
	require.Empty(t, FailureText(err), "a chained iteration has no failure to record")
}

func TestFailureText_OnlyForRealFailures(t *testing.T) {
	require.Empty(t, FailureText(nil))
	require.Equal(t, "pi crashed", FailureText(errors.New("pi crashed")))
}
