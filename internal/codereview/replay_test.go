package codereview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"

	"temporal-agents/internal/wftest"
)

// The recording version gate (wfrecord.Enabled) protects every execution that was
// already in flight when the worker was upgraded to record. A mistake in it does
// not fail an ordinary test, it fails those executions, nondeterministically, at
// replay time — so every recorded workflow is pinned here against a real history
// captured from the code before recording existed (origin/main at df3f7a3),
// recorded against a Temporal dev server with the agent activities stubbed.
// Replaying such a history is exactly what a worker does after the upgrade, so a
// gate that scheduled a record write the old history lacks fails here.

// The review loop is the longest-lived workflow here — every pass continues as new
// — so it is the most likely to be mid-flight when the worker is upgraded to
// record. The fixture is a genuine first pass, and it ends in a continue-as-new,
// which is the case the recording version gate must not disturb: the pass's own
// error is the control signal that starts the next one.
func TestReviewWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ReviewWorkflow)

	err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/review_workflow_before_recording.json")

	require.NoError(t, err, "a review pass started before recording must still replay")
}

// A develop run spans up to an hour of agent work plus its review (and, with
// --with-remote, its PR and pilot) children, so it too can straddle the upgrade.
// The fixture is a genuine local develop run: it creates the branch, develops it,
// verifies the commits, and spawns the abandoned review child.
func TestDevelopWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(DevelopWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/develop_workflow_before_recording.json", "develop-fixture")

	require.NoError(t, err, "a develop run started before recording must still replay")
}

// The pilot loop chains without a fixed end, so an in-flight pass is the norm
// rather than the exception. The fixture is a genuine pass that found no
// unresolved comments left, which is the loop's terminal path.
func TestPilotWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PilotWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/pilot_workflow_before_recording.json", "pilot-fixture")

	require.NoError(t, err, "a pilot pass started before recording must still replay")
}
