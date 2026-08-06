// Package wftest holds the small helpers the workflow test suites share, in the
// same spirit as execstoretest: one home for a stand-in or a helper every package
// needs, instead of a copy per package that drifts.
//
// It is a normal (non _test) package because Go cannot import another package's
// test files.
package wftest

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ActivityName returns the Temporal-registered activity name for an activity
// method value (e.g. a.RunDevelopAgent).
//
// Negative assertions take a method-name string — testify's AssertNotCalled passes
// for any name it does not find, so a typo would silently defeat the assertion.
// Deriving the name from the method symbol makes a typo a compile error instead.
func ActivityName(method any) string {
	full := runtime.FuncForPC(reflect.ValueOf(method).Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	return full
}

// ReplayHistoryFile replays the recorded history in file against the workflows
// registered on replayer, under the workflow ID the history was captured with,
// and returns the replay result.
//
// The captured ID has to be restored explicitly: the replayer otherwise substitutes
// the workflow ID "ReplayId", and a workflow that derives a child workflow ID from
// its own (develop derives its review child, fleet derives one child per node) then
// produces a child command the history cannot match. The replay would fail for that
// substitution instead of for a real nondeterminism, which is exactly the false
// alarm a replay guard must not raise.
func ReplayHistoryFile(t testing.TB, replayer worker.WorkflowReplayer, file, workflowID string) error {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("could not open the replay fixture %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()
	history, err := client.HistoryFromJSON(f, client.HistoryJSONOptions{})
	if err != nil {
		t.Fatalf("could not read the replay fixture %s: %v", file, err)
	}
	return replayer.ReplayWorkflowHistoryWithOptions(nil, history, worker.ReplayWorkflowHistoryOptions{
		OriginalExecution: workflow.Execution{ID: workflowID},
	})
}
