package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

// The recording version gate (wfrecord.Enabled) protects every execution that was
// already in flight when the worker was upgraded to record: a chained run, a
// schedule and the pilot loop can all span that upgrade. A mistake in it does not
// fail a test, it fails those executions, nondeterministically, at replay time —
// which is why it is pinned here against a real history instead.
//
// testdata/*_before_recording.json are genuine histories captured from the code
// before recording existed (origin/main at df3f7a3), recorded against a Temporal
// dev server with the agent activities stubbed. Replaying them against the current
// workflows is exactly what a worker does after the upgrade, so a version gate that
// scheduled a record write the old history lacks would fail here.
func TestPromptWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PromptWorkflow)

	err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/prompt_workflow_before_recording.json")

	require.NoError(t, err, "an execution started before recording must still replay")
}

// The location probe is gated the same way, and against a different history: an
// execution started after recording was switched on but before the probe existed
// has the recording marker and no probe marker. The fixture below was captured from
// the code one commit before the probe, so a gate that scheduled a probe that
// history lacks fails here instead of in a chained run that has been looping for
// days.
func TestPromptWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PromptWorkflow)

	err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/prompt_workflow_before_location.json")

	require.NoError(t, err, "a run started before the probe must still replay")
}
