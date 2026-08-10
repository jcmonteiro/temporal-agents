package fleet

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"

	"temporal-agents/internal/wftest"
)

// The recording version gate (wfrecord.Enabled) protects every execution that was
// already in flight when the worker was upgraded to record. A mistake in it does
// not fail an ordinary test, it fails those executions, nondeterministically, at
// replay time — so both fleet workflows are pinned here against a real history
// captured from the code before recording existed (origin/main at df3f7a3),
// recorded against a Temporal dev server with the agent activities stubbed.
//
// A fixture has to be re-captured whenever a recorded workflow changes shape without
// a version gate; the procedure is written down in
// internal/wftest/replay-fixtures.md.

// A fleet run orchestrates every node's develop child and their review loops, so
// it lasts hours and is the least likely of all the workflows to be idle when the
// worker is upgraded. The fixture is a genuine run whose first node's child failed
// and whose dependent node was therefore skipped, so it covers the per-node
// breakdown path too.
func TestFleetWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FleetWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/fleet_workflow_before_recording.json", "fleet-fixture")

	require.NoError(t, err, "a fleet run started before recording must still replay")
}

// The planning workflow gained two commands, not one: the record writes and the
// StorePlan call that replaced fleet-plan.json. The fixture's input carries no
// plan handle — plans did not live in a store yet — so replaying it pins both
// gates: the recording version gate and the `in.PlanID != ""` guard around the
// store write.
func TestFleetPlanWorkflow_ReplaysAHistoryFromBeforeRecording(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FleetPlanWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/fleet_plan_workflow_before_recording.json", "fleet-plan-fixture")

	require.NoError(t, err, "a planning run started before recording must still replay")
}

// The location probe added a second gate on top of the recording one, and the case
// it protects is not the one above: an execution started *after* recording was
// switched on but before the probe existed has the recording marker in its history
// and no probe marker. The fixtures below are exactly that — captured from the code
// one commit before the probe — so a gate that scheduled a probe those histories
// lack fails here rather than in a fleet run that is hours into its graph.

func TestFleetWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FleetWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/fleet_workflow_before_location.json", "fleet-fixture")

	require.NoError(t, err, "a fleet run started before the probe must still replay")
}

func TestFleetPlanWorkflow_ReplaysAHistoryFromBeforeTheLocationProbe(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FleetPlanWorkflow)

	err := wftest.ReplayHistoryFile(t, replayer, "testdata/fleet_plan_workflow_before_location.json", "fleet-plan-fixture")

	require.NoError(t, err, "a planning run started before the probe must still replay")
}
