package codereview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// ValidateReviewJSON is the deterministic gate between structuring and looping,
// so its behavior — canonical output, item count, and rejecting bad input — is
// worth exercising directly.

func TestValidateReviewJSON_CanonicalizesAndCountsItems(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	act := &Activities{}
	env.RegisterActivity(act)

	// Prose + code fence + non-canonical spacing on the way in.
	in := "Sure:\n```json\n{ \"review\" : [ {\"itemName\": \"rename foo\"} ] }\n```"
	val, err := env.ExecuteActivity(act.ValidateReviewJSON, ValidateReviewRequest{Payload: in})
	require.NoError(t, err)

	var got ValidateReviewResult
	require.NoError(t, val.Get(&got))
	require.Equal(t, 1, got.ItemCount)
	// The stored payload is canonical JSON, ready to hand to the next pass.
	require.Equal(t, `{"review":[{"itemName":"rename foo"}]}`, got.Payload)
}

func TestValidateReviewJSON_RejectsInvalidWithNonRetryableError(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	act := &Activities{}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.ValidateReviewJSON, ValidateReviewRequest{Payload: "no json here"})
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.True(t, appErr.NonRetryable())
	require.Equal(t, errInvalidReviewJSON, appErr.Type())
}
