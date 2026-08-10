package agenthub_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
)

// The read path's answer to "where does this work run?". The facts arrive on the
// recorded execution exactly as an outcome does; what the API publishes is the place
// derived from them, so these tests drive the service and assert on the satellites.

func TestARunReportsThePlaceItsExecutionRecorded(t *testing.T) {
	id := "run-" + uuidLike("70")
	recorded := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(time.Hour))
	recorded.Place = agenthub.RecordedPlace{
		Directory:  "/srv/worktrees/pricing-fix",
		Repository: "/srv/repos/pricing",
	}
	source := agenthubtest.New().WithRecorded(recorded)

	runs, err := newService(t, source).Runs(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, runs, 1)
	directory, hasDirectory := runs[0].Location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/worktrees/pricing-fix", directory)
	parent, hasParent := runs[0].Location.Parent()
	require.True(t, hasParent, "a run in a worktree must report its repository as the place it belongs to")
	require.Equal(t, "pricing", parent.Label())
}

func TestAChainKeepsThePlaceAnEarlierIterationRecorded(t *testing.T) {
	// A chain loops through continue-as-new, and an iteration written before the
	// probe existed (or one whose probe failed) records no place. The chain is one
	// satellite in one place, so the fact one iteration established stands.
	id := "run-" + uuidLike("71")
	first := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(3*time.Hour))
	first.RunID = "iteration-1"
	first.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	latest := agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(time.Hour))
	latest.RunID = "iteration-2"
	source := agenthubtest.New().WithRecorded(first, latest)

	runs, err := newService(t, source).Runs(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, runs, 1)
	directory, hasDirectory := runs[0].Location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/repos/pricing", directory)
}

func TestWorkWhoseExecutionRecordedNoPlaceIsPublishedAsUnknown(t *testing.T) {
	id := "run-" + uuidLike("72")
	source := agenthubtest.New().
		WithRecorded(agenthubtest.Run(id, "watch the queue", agenthub.OutcomeSucceeded, ago(time.Hour)))

	runs, err := newService(t, source).Runs(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, agenthub.LocationUnknown, runs[0].Location.Kind(),
		"a run whose place was never recorded must be unknown, never guessed")
}

func TestAFleetNodeReportsItsOwnWorktreeAndTheFleetItsRepository(t *testing.T) {
	// A node develops in a worktree of its own, so it genuinely runs somewhere else
	// than the fleet that orchestrates it.
	fleetID := "fleet-" + uuidLike("73")
	parent := agenthubtest.Fleet(fleetID, agenthub.OutcomeRunning, ago(time.Hour))
	parent.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	node := agenthubtest.Node(fleetID, "api", agenthub.OutcomeRunning, ago(30*time.Minute))
	node.Place = agenthub.RecordedPlace{
		Directory:  "/srv/worktrees/api",
		Repository: "/srv/repos/pricing",
	}
	source := agenthubtest.New().
		WithRecorded(parent, node).
		WithRunning(parent, node).
		WithPlan(fleetID, agenthub.Plan{Goal: "expose pricing", Nodes: []agenthub.PlanNode{
			{ID: "api", Prompt: "the REST layer"},
		}})

	fleets, err := newService(t, source).Fleets(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, fleets, 1)
	fleetDirectory, _ := fleets[0].Location.Directory()
	require.Equal(t, "/srv/repos/pricing", fleetDirectory)
	require.Len(t, fleets[0].Nodes, 1)
	nodeDirectory, _ := fleets[0].Nodes[0].Location.Directory()
	require.Equal(t, "/srv/worktrees/api", nodeDirectory)
	nodeParent, hasParent := fleets[0].Nodes[0].Location.Parent()
	require.True(t, hasParent)
	require.Equal(t, fleets[0].Location.ID(), nodeParent.ID(),
		"the node's worktree must hang under the very place the fleet reports")
}

func TestAScheduleReportsThePlaceItsRunsRecorded(t *testing.T) {
	// A schedule runs nothing itself. It sits where the work it fires runs, so the
	// place comes from its most recent firing that recorded one.
	scheduleID := "sched-" + uuidLike("74")
	action := agenthubtest.Run("run-"+uuidLike("75"), "nightly sweep", agenthub.OutcomeSucceeded, ago(time.Hour))
	action.ScheduleID = scheduleID
	action.Place = agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}
	source := agenthubtest.New().
		WithRecorded(action).
		WithSchedules(agenthub.ScheduleState{ID: scheduleID, Spec: "every day"})

	schedules, err := newService(t, source).Schedules(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, schedules, 1)
	directory, hasDirectory := schedules[0].Location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/repos/pricing", directory)
}
