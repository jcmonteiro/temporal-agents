package fleet

import "context"

// Agent is the port for running the Pi agent to produce a fleet plan. The
// concrete adapter lives in the piagent package; it is the same shape as
// codereview.Agent so the same piagent.Agent value satisfies both. Keeping a
// local port declaration keeps this application core decoupled from the driven
// adapter.
type Agent interface {
	// Run executes the agent for prompt in workDir and returns its final message
	// and the total token usage of the session.
	Run(ctx context.Context, prompt, workDir string) (output string, tokens int, err error)
}
