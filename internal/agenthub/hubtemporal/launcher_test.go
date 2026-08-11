package hubtemporal

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/codereview"
)

// What the hub submits is asserted rather than described: the workflow function,
// the input, the identity and the conflict policy are the contract between this API
// and the worker that executes the work, and a stand-in client is what makes all
// four visible.

// submitted is one recorded submission.
type submitted struct {
	options  client.StartWorkflowOptions
	workflow any
	args     []any
}

// fakeStarter records what it was asked to submit.
type fakeStarter struct {
	calls []submitted
	err   error
}

func (f *fakeStarter) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	f.calls = append(f.calls, submitted{options: options, workflow: workflow, args: args})
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

func TestADevelopPassIsSubmittedAsTheWorkerExecutesIt(t *testing.T) {
	starter := &fakeStarter{}
	launcher, err := NewLauncher(starter, "agents", "/srv/worktrees")
	if err != nil {
		t.Fatalf("NewLauncher: %v", err)
	}

	err = launcher.Start(context.Background(), agenthub.StartSpec{
		WorkflowID: "develop-1",
		Kind:       agenthub.StartDevelop,
		Directory:  "/srv/repos/pricing",
		Prompt:     "make the flaky test pass",
	})

	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(starter.calls) != 1 {
		t.Fatalf("submissions = %d, want one", len(starter.calls))
	}
	call := starter.calls[0]
	// The very workflow `code develop` starts, with the input that command builds
	// for a prompt and a working directory.
	if !sameFunction(call.workflow, codereview.DevelopWorkflow) {
		t.Errorf("workflow = %T, want the develop workflow", call.workflow)
	}
	input, ok := call.args[0].(codereview.DevelopInput)
	if !ok {
		t.Fatalf("input = %T, want a develop input", call.args[0])
	}
	if input.WorkDir != "/srv/repos/pricing" || input.Prompt != "make the flaky test pass" {
		t.Errorf("input = %+v, want the resolved directory and the prompt", input)
	}
	if input.Branch != "" || input.WorktreesDir != "/srv/worktrees" || input.Summary || !input.WithRemote {
		t.Errorf("input = %+v, want a generated branch in a worktree with the remote pipeline", input)
	}
	if call.options.ID != "develop-1" || call.options.TaskQueue != "agents" {
		t.Errorf("options = %+v, want the minted identity on the worker's queue", call.options)
	}
	// Two requests that race each other must not both start work: the second finds
	// the execution already running and is answered with it.
	if call.options.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Errorf("conflict policy = %v, want the existing execution to be used",
			call.options.WorkflowIDConflictPolicy)
	}
}

func TestAReviewPassIsSubmittedWithNothingButThePlace(t *testing.T) {
	starter := &fakeStarter{}
	launcher, _ := NewLauncher(starter, "agents", "/srv/worktrees")

	err := launcher.Start(context.Background(), agenthub.StartSpec{
		WorkflowID: "review-1",
		Kind:       agenthub.StartReview,
		Directory:  "/srv/repos/pricing",
	})

	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	call := starter.calls[0]
	if !sameFunction(call.workflow, codereview.ReviewWorkflow) {
		t.Errorf("workflow = %T, want the review workflow", call.workflow)
	}
	input, ok := call.args[0].(codereview.ReviewInput)
	if !ok {
		t.Fatalf("input = %T, want a review input", call.args[0])
	}
	if input.WorkDir != "/srv/repos/pricing" || input.Summary {
		t.Errorf("input = %+v, want the resolved directory alone", input)
	}
}

func TestWorkTheHubCannotStartIsNeverSubmitted(t *testing.T) {
	starter := &fakeStarter{}
	launcher, _ := NewLauncher(starter, "agents", "/srv/worktrees")

	err := launcher.Start(context.Background(), agenthub.StartSpec{
		WorkflowID: "fleet-1", Kind: "fleet", Directory: "/srv/repos/pricing",
	})

	if err == nil {
		t.Fatal("a kind with no workflow was accepted")
	}
	if len(starter.calls) != 0 {
		t.Errorf("submissions = %d, want none", len(starter.calls))
	}
}

func TestAnOrchestratorThatRefusesTheSubmissionIsReported(t *testing.T) {
	starter := &fakeStarter{err: errors.New("the namespace is unavailable")}
	launcher, _ := NewLauncher(starter, "agents", "/srv/worktrees")

	err := launcher.Start(context.Background(), agenthub.StartSpec{
		WorkflowID: "review-1", Kind: agenthub.StartReview, Directory: "/srv/repos/pricing",
	})

	if err == nil {
		t.Fatal("a refused submission was reported as a start")
	}
}

func TestALauncherWithoutWhatItNeedsDoesNotBuild(t *testing.T) {
	if _, err := NewLauncher(nil, "agents", "/srv/worktrees"); err == nil {
		t.Error("a launcher with no client was built")
	}
	if _, err := NewLauncher(&fakeStarter{}, "  ", "/srv/worktrees"); err == nil {
		t.Error("a launcher with no task queue was built")
	}
	if _, err := NewLauncher(&fakeStarter{}, "agents", "  "); err == nil {
		t.Error("a launcher with no worktrees directory was built")
	}
}

// sameFunction reports whether two values are the same function. Function values
// are not comparable in Go, so they are compared by the name the runtime knows them
// under — which is also the name Temporal registers a workflow by.
func sameFunction(a, b any) bool {
	return functionName(a) == functionName(b)
}

func functionName(value any) string {
	return runtime.FuncForPC(reflect.ValueOf(value).Pointer()).Name()
}
