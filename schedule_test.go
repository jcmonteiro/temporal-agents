package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A schedule is not a recorded kind of its own: it has no workflow, and every
// write happens inside a workflow. What makes a fired run attributable is the
// schedule ID threaded into the same PromptWorkflow input `run` uses.

func TestScheduleAction_FiredRunsCarryTheirScheduleID(t *testing.T) {
	action := scheduleAction("schedule-9", "post the digest", "/repo", true)

	require.Equal(t, "schedule-9-wf", action.ID)
	require.Len(t, action.Args, 1)
	req, ok := action.Args[0].(PromptRequest)
	require.True(t, ok)
	require.Equal(t, "schedule-9", req.ScheduleID)
	require.Equal(t, "post the digest", req.Prompt)
	require.Equal(t, "/repo", req.WorkDir)
	require.True(t, req.Chain)
}

func TestRunRequest_DirectRunHasNoSchedule(t *testing.T) {
	// A directly started run must be attributed to no schedule, so filtering
	// history by a schedule cannot pick it up.
	req := runRequest("summarize", "/repo", false)

	require.Empty(t, req.ScheduleID)
	require.Equal(t, "summarize", req.Prompt)
}
