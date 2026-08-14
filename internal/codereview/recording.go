package codereview

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/place"
	"temporal-agents/internal/wfplace"
	"temporal-agents/internal/wfrecord"
)

// This file is the recording half of the code workflows: the typed state each
// workflow persists, the activities that write it through the execstore port, and
// the workflow-side helpers that call them. Every write happens in an activity, so
// the workflows stay deterministic, and no SQL or driver type reaches this
// package — only the port's record types.
//
// Recording is not best-effort like notification: Temporal's retries absorb a
// transient store outage, and an exhausted policy is never ignored. A start write
// that cannot land fails the workflow (nothing has happened yet); a terminal one is
// reported without discarding the work it was recording (see
// wfrecord.TerminalWriteFailed). Each write is an idempotent upsert on the Temporal
// run ID, so a retried activity that had already committed neither duplicates the
// row nor corrupts it.

// DevelopState is the typed input to PersistDevelopWorkflowState.
//
// Open-pr is deliberately not a state of its own: OpenPRWorkflow runs only inside
// the --with-remote develop pipeline, so its outcome (the PR URL) is folded in
// here as a field rather than recorded as a separate execution.
type DevelopState struct {
	// WorkflowID and RunID are the Temporal correlation handles; RunID is the key
	// every write upserts on.
	WorkflowID string
	RunID      string
	// ParentWorkflowID is the fleet run that started this node, or empty for a
	// standalone `code develop`.
	ParentWorkflowID string
	// Prompt is the change the develop agent was asked to implement.
	Prompt string
	// Branch is the branch developed on, resolved once created (it may be a
	// generated alias, so the start write can carry none).
	Branch string
	// PRURL is the pull request the --with-remote pipeline opened, or empty.
	PRURL string
	// StartedAt and EndedAt come from the workflow's deterministic clock; EndedAt
	// is the zero time on the start write.
	StartedAt time.Time
	EndedAt   time.Time
	// Status is StatusRunning on the start write and the outcome afterwards.
	Status execstore.Status
	// Tokens is the develop step's own token usage (including any conflict
	// resolution while seeding), never the inclusive total, so this row and its
	// child review row do not double-count.
	Tokens int
	// Place is where the develop run runs. It is the worktree the run develops in,
	// not the directory the run was started from, so a fleet node reports its own
	// place rather than its fleet's.
	Place place.Facts
	// Error is the failure text when the workflow failed.
	Error string
}

// ReviewState is the typed input to PersistReviewWorkflowState. Each pass of the
// loop continues as new and is therefore a row of its own.
type ReviewState struct {
	WorkflowID string
	RunID      string
	// Detached reports that the parent does not supervise this review's lifecycle,
	// so the review remains independently visible after the parent closes.
	Detached bool
	// ParentWorkflowID is the develop run that spawned this review, or empty for a
	// standalone `code review`. It is what tells the two apart in history.
	ParentWorkflowID string
	// Pass is which pass of the review loop this row is.
	Pass int
	// Resets is how often an operator renewed the pass budget.
	Resets int
	// StartedAt and EndedAt come from the workflow's deterministic clock.
	StartedAt time.Time
	EndedAt   time.Time
	Status    execstore.Status
	// Tokens is this pass's own usage (its implement and review sessions), never
	// the inclusive TokensSoFar the loop carries forward for its result text.
	Tokens int
	// Converged reports whether the loop ended because the agent found nothing
	// left to change, as opposed to hitting the pass cap. It is nil until the loop
	// settles: an intermediate pass that continues as new has not answered the
	// question, and recording it as "did not converge" would misreport it.
	Converged *bool
	// Ending names why the loop ended, which the boolean above cannot: a loop can
	// also be stopped or accepted by an operator. It is empty until the loop settles.
	Ending Ending
	// WaitingSince is when this pass started waiting for an operator's decision, and
	// the zero time whenever it is not waiting. It is what makes a run that is
	// technically still running report that it needs a human.
	WaitingSince time.Time
	// WaitingSession is the steering session that wait is in, and empty whenever the
	// pass is not waiting. It travels with the wait so a surface can go straight from
	// the run that is asking to the question it is asking.
	WaitingSession string
	// Place is where the review pass runs.
	Place place.Facts
	// Instructions is which stored instruction version each of the loop's keys
	// resolved to, so the row explains what the agent was told without holding a
	// copy of the text.
	Instructions instruction.Resolution
	Error        string
}

// PilotState is the typed input to PersistPilotWorkflowState. A chained pilot
// continues as new per pass, so each pass is a row of its own.
type PilotState struct {
	WorkflowID string
	RunID      string
	// ParentWorkflowID is the develop run supervising this pilot, or empty for a
	// standalone `code pilot`.
	ParentWorkflowID string
	// PRURL is the pull request the pass operated on, once known.
	PRURL string
	// StartedAt and EndedAt come from the workflow's deterministic clock.
	StartedAt time.Time
	EndedAt   time.Time
	Status    execstore.Status
	// Tokens is this pass's own agent usage, never the inclusive running total.
	Tokens int
	// Addressed reports whether the pass actually addressed comments (as opposed
	// to finding none left). It is nil while the pass has not reached that
	// decision yet.
	Addressed *bool
	// Ending names why the loop ended, when it ended for a named reason. Only an
	// operator stopping a pass gives the pilot loop one today.
	Ending Ending
	// WaitingSince is when this pass started waiting for an operator's decision, and
	// the zero time whenever it is not waiting.
	WaitingSince time.Time
	// WaitingSession is the steering session that wait is in, and empty whenever the
	// pass is not waiting.
	WaitingSession string
	// Place is where the pilot pass runs.
	Place place.Facts
	// Instructions is which stored instruction version the pass addressed comments
	// under.
	Instructions instruction.Resolution
	Error        string
}

// PersistDevelopWorkflowState records a DevelopWorkflow execution's state. It is
// called when the workflow starts and again when it settles.
//
// The prompt passes through wfrecord.Sanitize at this boundary, like the failure
// text: a develop prompt is written by an operator (or generated by the fleet's
// planning agent, so it is not even a typed command-line argument), it can carry a
// credential, and it has no length bound of its own.
func (a *Activities) PersistDevelopWorkflowState(ctx context.Context, in DevelopState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindDevelop,
		Prompt:           wfrecord.Sanitize(in.Prompt),
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail: execstore.Detail{
			Branch:     in.Branch,
			PRURL:      in.PRURL,
			Error:      in.Error,
			Directory:  in.Place.Directory,
			Repository: in.Place.Repository,
		},
	})
}

// PersistReviewWorkflowState records a ReviewWorkflow pass's state.
func (a *Activities) PersistReviewWorkflowState(ctx context.Context, in ReviewState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	detail := execstore.Detail{
		Detached: in.Detached,
		Pass:     in.Pass, Resets: in.Resets, Converged: in.Converged, Ending: string(in.Ending), Error: in.Error,
		WaitingSince: waitingSince(in.WaitingSince), WaitingSession: in.WaitingSession,
		Directory: in.Place.Directory, Repository: in.Place.Repository,
		Instructions: instructionUses(in.Instructions),
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindReview,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail:           detail,
	})
}

// PersistPilotWorkflowState records a PilotWorkflow pass's state.
func (a *Activities) PersistPilotWorkflowState(ctx context.Context, in PilotState) error {
	if a.Store == nil {
		return execstore.ErrNotConfigured
	}
	detail := execstore.Detail{
		PRURL: in.PRURL, Addressed: in.Addressed, Ending: string(in.Ending), Error: in.Error,
		WaitingSince: waitingSince(in.WaitingSince), WaitingSession: in.WaitingSession,
		Directory: in.Place.Directory, Repository: in.Place.Repository,
		Instructions: instructionUses(in.Instructions),
	}
	return a.Store.SaveExecution(ctx, execstore.Execution{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             execstore.KindPilot,
		StartedAt:        in.StartedAt,
		EndedAt:          in.EndedAt,
		Status:           in.Status,
		Tokens:           in.Tokens,
		ParentWorkflowID: in.ParentWorkflowID,
		Detail:           detail,
	})
}

// startDevelopState builds and writes the "started" record for the running
// DevelopWorkflow, returning the state the terminal write updates.
//
// It records no place: a develop run's place is the worktree it develops in, and
// that worktree does not exist yet when the run must be recorded as started. The
// place is added by recordDevelopPlace as soon as the directory is real.
func startDevelopState(ctx workflow.Context, in DevelopInput) (DevelopState, error) {
	// An execution whose history predates durable recording must not have a record
	// write inserted into its replay (see wfrecord.Enabled); it simply goes
	// unrecorded.
	if !wfrecord.Enabled(ctx) {
		return DevelopState{}, nil
	}
	id := wfrecord.Of(ctx)
	st := DevelopState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		Prompt:           in.Prompt,
		Branch:           in.Branch,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistDevelopWorkflowState, st).Get(opts, nil); err != nil {
		return DevelopState{}, fmt.Errorf("record the develop run as started: %w", err)
	}
	return st, nil
}

// recordDevelopPlace establishes where the run develops — the worktree it created,
// or the checkout it switched — and upserts the record so a *running* develop run is
// already in its place on the overview, rather than only once it settles.
//
// Both halves are deliberately unfailing. The probe degrades to no place at all (see
// wfplace.Probe), and the write is best-effort because the same facts travel on the
// terminal write: losing this one costs a place until the run settles, while failing
// the run would cost the work itself. It returns the state the caller keeps.
func recordDevelopPlace(ctx workflow.Context, st DevelopState, workDir string) DevelopState {
	st.Place = wfplace.Probe(ctx, workDir)
	if !wfrecord.Enabled(ctx) || !st.Place.Established() {
		return st
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistDevelopWorkflowState, st).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn(
			"could not record where the develop run develops; its terminal record will carry it", "error", err)
	}
	return st
}

// finishDevelopState records the develop run's terminal state on a disconnected
// context, so a cancelled run still settles its record rather than staying
// "running" forever.
func finishDevelopState(ctx workflow.Context, st DevelopState, err error) error {
	if !wfrecord.Enabled(ctx) {
		return nil
	}
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistDevelopWorkflowState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the develop run's terminal state: %w", perr)
	}
	return nil
}

// startReviewState builds and writes the "started" record for the running
// ReviewWorkflow pass.
func startReviewState(ctx workflow.Context, in ReviewInput) (ReviewState, error) {
	// An execution whose history predates durable recording must not have a record
	// write inserted into its replay (see wfrecord.Enabled); it simply goes
	// unrecorded.
	if !wfrecord.Enabled(ctx) {
		return ReviewState{}, nil
	}
	id := wfrecord.Of(ctx)
	st := ReviewState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		Detached:         in.Detached,
		ParentWorkflowID: id.ParentWorkflowID,
		Pass:             in.Pass,
		Resets:           in.Resets,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
		Place:            wfplace.Probe(ctx, in.WorkDir),
		Instructions:     in.Instructions,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistReviewWorkflowState, st).Get(opts, nil); err != nil {
		return ReviewState{}, fmt.Errorf("record the review pass as started: %w", err)
	}
	return st, nil
}

// recordReviewWaiting upserts the review pass's row while it waits for an
// operator, and again once it no longer does, so a run that needs a human says so
// on the overview instead of looking like a run that is quietly working.
//
// It is best-effort, exactly as recordDevelopPlace is: losing the write costs a
// waiting run its "needs input" badge until it settles, while failing the pass for
// it would throw away the round the operator is being asked about.
func recordReviewWaiting(ctx workflow.Context, st ReviewState) {
	if !wfrecord.Enabled(ctx) {
		return
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistReviewWorkflowState, st).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn(
			"could not record that the review pass is waiting for an operator", "error", err)
	}
}

// recordPilotWaiting is recordReviewWaiting for the pilot loop, with the same
// best-effort policy and for the same reason.
func recordPilotWaiting(ctx workflow.Context, st PilotState) {
	if !wfrecord.Enabled(ctx) {
		return
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistPilotWorkflowState, st).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn(
			"could not record that the pilot pass is waiting for an operator", "error", err)
	}
}

// finishReviewState records the review pass's terminal state.
func finishReviewState(ctx workflow.Context, st ReviewState, err error) error {
	if !wfrecord.Enabled(ctx) {
		return nil
	}
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)
	// A settled pass is not waiting for anybody, however it settled.
	st.WaitingSince, st.WaitingSession = time.Time{}, ""

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistReviewWorkflowState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the review pass's terminal state: %w", perr)
	}
	return nil
}

// startPilotState builds and writes the "started" record for the running
// PilotWorkflow pass. The pass reports its place from its first write, and — from
// its second pass on — the instructions the loop resolved when it began.
func startPilotState(ctx workflow.Context, in PilotInput) (PilotState, error) {
	// An execution whose history predates durable recording must not have a record
	// write inserted into its replay (see wfrecord.Enabled); it simply goes
	// unrecorded.
	if !wfrecord.Enabled(ctx) {
		return PilotState{}, nil
	}
	id := wfrecord.Of(ctx)
	st := PilotState{
		WorkflowID:       id.WorkflowID,
		RunID:            id.RunID,
		ParentWorkflowID: id.ParentWorkflowID,
		StartedAt:        workflow.Now(ctx),
		Status:           execstore.StatusRunning,
		Place:            wfplace.Probe(ctx, in.WorkDir),
		Instructions:     in.Instructions,
	}
	opts := wfrecord.WithOptions(ctx)
	var a *Activities
	if err := workflow.ExecuteActivity(opts, a.PersistPilotWorkflowState, st).Get(opts, nil); err != nil {
		return PilotState{}, fmt.Errorf("record the pilot pass as started: %w", err)
	}
	return st, nil
}

// finishPilotState records the pilot pass's terminal state.
func finishPilotState(ctx workflow.Context, st PilotState, err error) error {
	if !wfrecord.Enabled(ctx) {
		return nil
	}
	st.EndedAt = workflow.Now(ctx)
	st.Status = wfrecord.StatusOf(err)
	st.Error = wfrecord.FailureText(err)
	// A settled pass is not waiting for anybody, however it settled.
	st.WaitingSince, st.WaitingSession = time.Time{}, ""

	dctx, cancel := wfrecord.TerminalOptions(ctx)
	defer cancel()
	var a *Activities
	if perr := workflow.ExecuteActivity(dctx, a.PersistPilotWorkflowState, st).Get(dctx, nil); perr != nil {
		return fmt.Errorf("record the pilot pass's terminal state: %w", perr)
	}
	return nil
}

// boolPtr returns a pointer to a copy of b, for the record fields that must tell
// "false" apart from "not decided yet".
func boolPtr(b bool) *bool { return &b }

// waitingSince renders a wait for the record: the moment it began, or nothing at
// all when the execution is not waiting for anybody.
func waitingSince(since time.Time) *time.Time {
	if since.IsZero() {
		return nil
	}
	return &since
}

// instructionUses renders a resolution as the provenance the durable record keeps:
// which key resolved to which version, from which scope, and the hash of the text it
// was. The text itself is deliberately not copied — it stays recoverable from the
// version record, which is what keeps a row small and keeps one instruction's text
// stored once however many runs used it.
func instructionUses(resolved instruction.Resolution) []execstore.InstructionUse {
	if len(resolved) == 0 {
		return nil
	}
	uses := make([]execstore.InstructionUse, 0, len(resolved))
	for _, value := range resolved {
		uses = append(uses, execstore.InstructionUse{
			Key:          string(value.Key),
			Scope:        string(value.Scope),
			Version:      value.Version,
			Hash:         value.Hash,
			ModelScope:   string(value.Model.Scope),
			ModelVersion: value.Model.Version,
			ModelHash:    value.Model.Hash,
		})
	}
	return uses
}
