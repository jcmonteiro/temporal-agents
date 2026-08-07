package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/hubclient"
)

func TestFormatActiveWorkListsRunningTopLevelWorkAndSchedules(t *testing.T) {
	items := []hubclient.WorkItem{
		{ID: "fleet-1", Kind: "fleet", Status: "in-progress"},
		{ID: "run-finished", Kind: "run", Status: "done"},
		{ID: "review-1", Kind: "review", Status: "in-progress"},
		{ID: "schedule-1", Kind: "schedule", Status: "done"},
	}

	got := formatActiveWork(items)

	require.Contains(t, got, "fleet-1")
	require.Contains(t, got, "review-1")
	require.Contains(t, got, "schedule-1")
	require.NotContains(t, got, "run-finished")
	require.True(t, strings.HasSuffix(got, "3 active\n"))
}

func TestFormatActiveWorkReportsWhenNothingIsActive(t *testing.T) {
	got := formatActiveWork([]hubclient.WorkItem{{ID: "run-1", Kind: "run", Status: "failed"}})

	require.Equal(t, "Nothing running.\n", got)
}
