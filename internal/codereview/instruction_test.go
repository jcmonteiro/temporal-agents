package codereview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execstoretest"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/place"
	"temporal-agents/internal/place/placetest"
	"temporal-agents/internal/scoped/scopedtest"
	"temporal-agents/internal/setting"
	"temporal-agents/internal/steering"
)

// The review and pilot loops are the first workflows whose instructions come out of
// storage. What matters about that is not that a read happens, but what an operator
// can rely on: what they configured is what the agent is told, an edit made while a
// loop runs does not change what a later pass did, an unreachable store stops the
// work instead of silently changing it, and every settled pass says which
// instruction it used.

// newLoopEnv builds a review environment around a given instruction store and
// execution store, so a test can configure what is stored and read what was
// recorded.
func newLoopEnv(t *testing.T, instructions *scopedtest.Store, records *execstoretest.Store) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Store: records})
	env.RegisterActivity(&notification.Activity{})
	env.RegisterActivity(&place.Activity{Prober: placetest.New()})
	env.RegisterActivity(&instruction.Activity{Store: instructions})
	// The settings resolve through a store of their own here, so a test that counts
	// reads of the instruction store counts instruction reads and nothing else.
	env.RegisterActivity(&setting.Activity{Resolver: setting.Resolver{Store: autonomousSettings()}})
	env.RegisterActivity(&steering.Activities{Store: records})
	env.RegisterWorkflow(steering.SessionWorkflow)
	env.RegisterWorkflow(ReviewWorkflow)
	env.RegisterWorkflow(PilotWorkflow)
	return env
}

// What an operator stored for a place is what that place's agent is told. The pass
// hands the resolved instruction to the agent step in its own input, which is what
// lets it keep using it after a later edit.
func TestAReviewPassRunsUnderTheInstructionStoredForWhereItRuns(t *testing.T) {
	instructions := scopedtest.New()
	instructions.Store(instruction.KeyReviewPerform, instruction.GlobalScope, "Review only the public API")
	env := newLoopEnv(t, instructions, execstoretest.New())
	var told ReviewInput
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { told = args.Get(1).(ReviewInput) }).
		Return(AgentResult{Output: "feedback"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, "Review only the public API", told.Instructions.Text(instruction.KeyReviewPerform))
}

// A loop resolves once, at its start. A pass that was handed instructions by the
// pass before it must not read the store again: re-resolving would let an
// instruction edited mid-loop change what a later pass did, while the passes already
// recorded name the version the loop began under.
func TestALaterPassUsesWhatTheLoopResolvedRatherThanWhatIsStoredNow(t *testing.T) {
	instructions := scopedtest.New()
	instructions.Store(instruction.KeyReviewImplement, instruction.GlobalScope, "the edit made mid-loop {{.Review}}")
	env := newLoopEnv(t, instructions, execstoretest.New())
	carried := instruction.Resolution{{
		Key:     instruction.KeyReviewImplement,
		Text:    "what the loop began under {{.Review}}",
		Scope:   instruction.GlobalScope,
		Version: 1,
	}}
	var told RunImplementRequest
	env.OnActivity(a.MarkHeadAndStash, mock.Anything, mock.Anything).Return(Checkpoint{HeadSHA: "head"}, nil)
	env.OnActivity(a.RunImplementAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { told = args.Get(1).(RunImplementRequest) }).
		Return(AgentResult{Output: "implemented"}, nil)
	env.OnActivity(a.EnsureHeadAdvanced, mock.Anything, mock.Anything).Return([]string{"sha"}, nil)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "more"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{
		WorkDir: "/repo", Payload: "feedback", Pass: 1, Instructions: carried,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Equal(t, "what the loop began under {{.Review}}",
		told.Instructions.Text(instruction.KeyReviewImplement))
	require.Zero(t, instructions.Reads, "a pass that was handed its instructions read the store again")
}

// A store that cannot answer stops the pass before any agent runs. Falling back to
// the build's defaults would change what the agent is told with nothing in the
// record to say it happened, which is exactly what stored instructions exist to
// prevent.
func TestAPassWhoseInstructionsCannotBeResolvedFailsBeforeTheAgentRuns(t *testing.T) {
	instructions := scopedtest.New()
	instructions.Err = errors.New("postgres is down")
	env := newLoopEnv(t, instructions, execstoretest.New())

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "postgres is down")
	env.AssertNotCalled(t, activityName(a.RunReviewAgent), mock.Anything, mock.Anything)
}

// "Which instruction produced this?" is answered from the durable record: the key,
// where the value came from, which version it was, and the hash of the text. The
// text itself is not copied into the row — it stays in the version record those
// three fields name.
func TestASettledReviewPassRecordsWhichInstructionVersionItUsed(t *testing.T) {
	instructions := scopedtest.New()
	stored := instructions.Store(instruction.KeyReviewPerform, instruction.GlobalScope, "Review only the public API")
	records := execstoretest.New()
	env := newLoopEnv(t, instructions, records)
	env.OnActivity(a.RunReviewAgent, mock.Anything, mock.Anything).Return(AgentResult{Output: "feedback"}, nil)

	env.ExecuteWorkflow(ReviewWorkflow, ReviewInput{WorkDir: "/repo", Pass: MaxReviewPasses - 1})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	used := records.Last(t).Detail.Instructions
	require.Contains(t, used, execstore.InstructionUse{
		Key:     string(instruction.KeyReviewPerform),
		Scope:   string(instruction.GlobalScope),
		Version: stored.Version,
		Hash:    stored.Hash,
	})
	for _, use := range used {
		require.NotContains(t, use.Hash, "Review only", "the instruction text was copied into the row")
	}
}

// The pilot loop is governed the same way, and its record answers the same question.
func TestASettledPilotPassRecordsWhichInstructionVersionItUsed(t *testing.T) {
	instructions := scopedtest.New()
	stored := instructions.Store(instruction.KeyPilotAddress, instruction.GlobalScope, "Address the test comments first")
	records := execstoretest.New()
	env := newLoopEnv(t, instructions, records)
	env.OnActivity(a.DeterminePR, mock.Anything, mock.Anything).
		Return(PullRequest{Number: 7, URL: "https://example.test/pr/7"}, nil)
	env.OnActivity(a.CheckOngoingReview, mock.Anything, mock.Anything).Return(false, nil)
	// No unresolved comments: the pass addresses nothing and settles.
	env.OnActivity(a.LoadUnresolvedComments, mock.Anything, mock.Anything).Return(LoadCommentsResult{}, nil)

	env.ExecuteWorkflow(PilotWorkflow, PilotInput{WorkDir: "/repo"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Contains(t, records.Last(t).Detail.Instructions, execstore.InstructionUse{
		Key:     string(instruction.KeyPilotAddress),
		Scope:   string(instruction.GlobalScope),
		Version: stored.Version,
		Hash:    stored.Hash,
	})
}

// The activities are where an instruction becomes a prompt, so this is where the
// promise "nothing configured behaves exactly as before" is kept: the agent is
// handed the shipped text, with the review inserted where the instruction says.
func TestTheAgentRunsUnderTheModelResolvedWithItsInstruction(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	agent := &fakeAgent{output: "feedback"}
	act := &Activities{Agent: agent}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.RunReviewAgent, ReviewInput{
		WorkDir: "/repo",
		Instructions: instruction.Resolution{{
			Key:   instruction.KeyReviewPerform,
			Text:  "Review this branch",
			Model: instruction.ModelValue{Text: "anthropic/claude-sonnet-4-5"},
		}},
	})

	require.NoError(t, err)
	require.Equal(t, "anthropic/claude-sonnet-4-5", agent.lastModel)
}

func TestTheAgentIsHandedTheResolvedInstructionWithItsMaterialInserted(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	agent := &fakeAgent{output: "implemented"}
	act := &Activities{Agent: agent}
	env.RegisterActivity(act)

	_, err := env.ExecuteActivity(act.RunImplementAgent, RunImplementRequest{
		WorkDir: "/repo",
		Payload: "rename the widget",
		Instructions: instruction.Resolution{{
			Key:  instruction.KeyReviewImplement,
			Text: "Act on this review, then commit:\n\n{{.Review}}",
		}},
	})

	require.NoError(t, err)
	require.Equal(t, "Act on this review, then commit:\n\nrename the widget", agent.lastPrompt)
}
