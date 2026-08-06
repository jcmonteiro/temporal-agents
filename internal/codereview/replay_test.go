package codereview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

// The review loop is the longest-lived workflow here — every pass continues as new
// — so it is the most likely to be mid-flight when the worker is upgraded to
// record. The fixture is a genuine first pass captured from the code before
// recording existed (origin/main at df3f7a3), and it ends in a continue-as-new,
// which is the case the recording version gate must not disturb: the pass's own
// error is the control signal that starts the next one.
func TestReviewWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ReviewWorkflow)

	err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/review_workflow_before_recording.json")

	require.NoError(t, err, "a review pass started before recording must still replay")
}
