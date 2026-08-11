package hubtemporal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/codereview"
)

// Starting work is the one thing this package does that is not a read.
//
// It stays here, at the edge, for the reason every other Temporal detail does: the
// core says what to run and where, and how a workflow is submitted — which workflow
// function, which task queue, which conflict policy — is the orchestrator's
// business. Nothing about what the work *does* is decided here: the workflows are
// the very ones the command line starts, with the input those commands build, so
// the hub cannot come to mean something different by "develop" than the CLI does.

// WorkflowStarter is the slice of the orchestration client the launcher needs,
// declared as narrowly as the read adapters declare theirs, so a test drives it
// with a stand-in instead of a server.
type WorkflowStarter interface {
	// ExecuteWorkflow submits a workflow and returns a handle to it.
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
}

// Launcher implements agenthub.Launcher over Temporal.
type Launcher struct {
	client       WorkflowStarter
	taskQueue    string
	worktreesDir string
}

// Compile-time proof the adapter satisfies the port it is injected as.
var _ agenthub.Launcher = (*Launcher)(nil)

// NewLauncher returns the launcher, on the task queue the worker listens to.
// Hub-started development uses worktreesDir so the registered checkout remains
// untouched while the full review pipeline runs.
func NewLauncher(c WorkflowStarter, taskQueue, worktreesDir string) (*Launcher, error) {
	if c == nil {
		return nil, errors.New("the orchestration client is required")
	}
	if strings.TrimSpace(taskQueue) == "" {
		return nil, errors.New("the task queue is required")
	}
	if strings.TrimSpace(worktreesDir) == "" {
		return nil, errors.New("the worktrees directory is required")
	}
	return &Launcher{client: c, taskQueue: taskQueue, worktreesDir: worktreesDir}, nil
}

// Start implements agenthub.Launcher.
//
// The submission is idempotent on the workflow ID the core minted: a request that
// arrives twice — a retried fetch, two browser tabs, a reload — finds the execution
// already running and is answered with it rather than refused. That is what
// "UseExisting" says, and it is the server's own guarantee: two requests that race
// each other cannot both win.
func (l *Launcher) Start(ctx context.Context, spec agenthub.StartSpec) error {
	workflow, input, err := submission(spec, l.worktreesDir)
	if err != nil {
		return err
	}
	_, err = l.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       spec.WorkflowID,
		TaskQueue:                l.taskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflow, input)
	if err != nil {
		return fmt.Errorf("submit the %s workflow: %w", spec.Kind, err)
	}
	return nil
}

// submission maps what the core asked for onto the workflow that does it and the
// input it takes. Hub-started development is isolated in a worktree and owns the
// complete review pipeline; a direct review still runs in the selected place.
func submission(spec agenthub.StartSpec, worktreesDir string) (any, any, error) {
	switch spec.Kind {
	case agenthub.StartDevelop:
		return codereview.DevelopWorkflow, codereview.DevelopInput{
			Initiator:    spec.StartedBy,
			WorkDir:      spec.Directory,
			WorktreesDir: worktreesDir,
			Prompt:       spec.Prompt,
			WithRemote:   true,
		}, nil
	case agenthub.StartReview:
		return codereview.ReviewWorkflow, codereview.ReviewInput{
			Initiator: spec.StartedBy,
			WorkDir:   spec.Directory,
		}, nil
	default:
		return nil, nil, fmt.Errorf("%q is not something this hub can start", spec.Kind)
	}
}
