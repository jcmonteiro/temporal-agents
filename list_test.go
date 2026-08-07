package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/workoverview"
)

type overviewReaderStub struct {
	items []workoverview.Item
	err   error
}

func (r overviewReaderStub) Overview(context.Context) ([]workoverview.Item, error) {
	return r.items, r.err
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestFormatActiveWorkUsesExecutionLivenessAndListsSchedules(t *testing.T) {
	items := []workoverview.Item{
		{ID: "fleet-todo", Kind: workoverview.KindFleet, Status: workoverview.StatusTodo, Running: true},
		{ID: "fleet-failed", Kind: workoverview.KindFleet, Status: workoverview.StatusFailed, Running: true},
		{ID: "run-finished", Kind: workoverview.KindRun, Status: workoverview.StatusDone},
		{ID: "review-1", Kind: workoverview.KindReview, Status: workoverview.StatusInProgress, Running: true},
		{ID: "schedule-1", Kind: workoverview.KindSchedule, Status: workoverview.StatusDone},
	}

	got := formatActiveWork(items)

	require.Equal(t, "TYPE       ID\n────       ──\nfleet      fleet-todo\nfleet      fleet-failed\nreview     review-1\nschedule   schedule-1\n\n4 active\n", got)
}

func TestFormatActiveWorkReportsWhenNothingIsActive(t *testing.T) {
	got := formatActiveWork([]workoverview.Item{{ID: "run-1", Kind: workoverview.KindRun, Status: workoverview.StatusFailed}})

	require.Equal(t, "Nothing running.\n", got)
}

func TestListCmdReturnsReaderFailuresWithCommandContext(t *testing.T) {
	err := listCmd(context.Background(), failingWriter{}, overviewReaderStub{err: errors.New("connection refused")})

	require.ErrorContains(t, err, "could not list work through Agent Hub")
	require.ErrorContains(t, err, "connection refused")
}

func TestListCmdReturnsWriterFailures(t *testing.T) {
	err := listCmd(context.Background(), failingWriter{}, overviewReaderStub{})

	require.ErrorContains(t, err, "write active work")
	require.ErrorContains(t, err, "disk full")
}
