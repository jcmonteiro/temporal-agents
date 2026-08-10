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
//
// A fixture has to be re-captured whenever a recorded workflow changes shape without
// a version gate; the procedure is written down in
// internal/wftest/replay-fixtures.md.

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

// The location probe added a second gate on top of the recording one, and it
// protects a different history: an execution started *after* recording was switched
// on but before the probe existed carries the recording marker and no probe marker.
// The fixtures below were captured from the code one commit before the probe, which
// is the shape of every execution in flight across that upgrade — including the
// develop run, where the probe is scheduled in the middle of the flow rather than
// before it.

func TestDevelopWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(DevelopWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/develop_workflow_before_location.json", "develop-fixture")

	require.NoError(t, err, "a develop run started before the probe must still replay")
}

func TestReviewWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ReviewWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/review_workflow_before_location.json", "review-fixture")

	require.NoError(t, err, "a review pass started before the probe must still replay")
}

func TestPilotWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PilotWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/pilot_workflow_before_location.json", "pilot-fixture")

	require.NoError(t, err, "a pilot pass started before the probe must still replay")
}

// Stored instructions added a third gate, and it protects a third kind of history:
// an execution started after recording and the probe, but before an instruction was
// ever resolved. Those histories carry both earlier markers and no resolution, which
// is the shape of every review and pilot pass in flight across that upgrade — and
// both loops are long-lived enough that being mid-flight is the norm.
//
// The fixtures were captured from the code one commit before the change, with the
// agent, git, GitHub and record activities stubbed under their real names.

func TestReviewWorkflow_ReplaysAHistoryFromBeforeStoredInstructions(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ReviewWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/review_workflow_before_instructions.json", "review-fixture")

	require.NoError(t, err, "a review pass started before instructions were stored must still replay")
}

func TestPilotWorkflow_ReplaysAHistoryFromBeforeStoredInstructions(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PilotWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/pilot_workflow_before_instructions.json", "pilot-fixture")

	require.NoError(t, err, "a pilot pass started before instructions were stored must still replay")
}

// Steering added a fourth gate, protecting a fourth kind of history: an execution
// started after recording, the probe and stored instructions, but before a setting
// was ever resolved. Such a history carries all three earlier markers and no
// settings marker, so replaying it must schedule neither the settings resolution
// nor a steering session — which is also what makes "steering off behaves exactly
// as before" true for the executions that were already running.
//
// The fixtures are the same pre-instructions histories: a history without the
// instructions marker necessarily has no settings marker either, so it is the
// oldest shape the gate must survive, and the one an in-flight loop is most likely
// to be in.

func TestReviewWorkflow_ReplaysAHistoryFromBeforeSteering(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ReviewWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/review_workflow_before_instructions.json", "review-fixture")

	require.NoError(t, err, "a review pass started before steering existed must still replay")
}

func TestPilotWorkflow_ReplaysAHistoryFromBeforeSteering(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(PilotWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/pilot_workflow_before_instructions.json", "pilot-fixture")

	require.NoError(t, err, "a pilot pass started before steering existed must still replay")
}
