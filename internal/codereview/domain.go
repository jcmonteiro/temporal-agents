// Package codereview implements two related review-loop features that share the
// same activities and domain logic:
//
//   - The Copilot pilot loop (PilotWorkflow): a Temporal workflow that lets the
//     Pi agent address the unresolved review comments on the open pull request
//     for the current branch, then replies to and resolves those comments and
//     requests a fresh Copilot review.
//   - The local review loop (ReviewWorkflow): a workflow that runs entirely on
//     the host machine, alternating a Pi-agent code review of the current
//     branch with a Pi-agent implement pass that acts on the review's raw
//     output, converging when a pass has nothing left to commit (bounded by a
//     maximum number of passes).
//
// It is organized around hexagonal architecture: this package holds the
// application core (the workflow orchestration, domain types, and pure logic)
// and depends only on ports (interfaces in ports.go). Concrete adapters for
// git, GitHub, and the Pi agent are injected from the edges (see the gitcli,
// ghcli, and piagent packages).
package codereview

import (
	"strconv"
	"strings"
)

// PromptMode selects how the caller-provided prompt text combines with the
// hard-coded default prompt.
type PromptMode string

const (
	// PromptDefault uses the built-in default prompt unchanged.
	PromptDefault PromptMode = ""
	// PromptAppend appends the caller's text to the default prompt.
	PromptAppend PromptMode = "append"
	// PromptReplace uses the caller's text instead of the default prompt.
	PromptReplace PromptMode = "replace"
)

// PilotInput is the workflow input.
type PilotInput struct {
	// WorkDir is the repository directory the CLI was invoked from.
	WorkDir string
	// PromptMode controls how PromptText is combined with the default prompt.
	PromptMode PromptMode
	// PromptText is the caller-supplied prompt text for append/replace modes.
	PromptText string
	// Chain, when true, spawns a delayed child run after a successful pass so
	// the loop keeps addressing new review feedback indefinitely.
	Chain bool
	// TokensSoFar carries the accumulated total token usage from prior passes of
	// a chained run, so the terminal result reports the whole chain's usage.
	TokensSoFar int
}

// PullRequest identifies the open PR the loop operates on.
type PullRequest struct {
	Number  int
	URL     string
	Owner   string
	Repo    string
	HeadRef string
	// Body is the PR description, given to the agent as context.
	Body string
}

// ReviewThread is one unresolved review conversation on the PR. ID is the
// GraphQL node id of the thread, used both to post a reply and to resolve it.
type ReviewThread struct {
	ID     string
	Path   string
	Line   int
	Author string
	// Body is the combined text of the thread's comments, used as context for
	// the agent.
	Body string
}

// Checkpoint records the repository state captured before the agent runs, so
// the workflow can tell whether the agent produced new commits and can restore
// any changes it stashed out of the way.
type Checkpoint struct {
	// HeadSHA is the commit HEAD pointed at before the agent ran.
	HeadSHA string
	// Stashed is true when local changes were stashed and must be restored if
	// the agent produces no commits.
	Stashed bool
}

// DefaultPrompt is the built-in instruction handed to the Pi agent. It is
// intentionally not surfaced in the CLI help.
const DefaultPrompt = `- For each comment below, read the referenced code for context, then fix it. Read the code and relevant in-repo documentation to decide on the solution.
- Confirm lint/typecheck/build (and synth, if infra) pass first.
- Commit all the fixes.
- Summarize your work once you are done explaining WHAT changed (not HOW)`

// BuildPrompt combines the base prompt (default, appended, or replaced) with
// the PR description (as context) and a formatted rendering of the unresolved
// review threads.
func BuildPrompt(mode PromptMode, text, prDescription string, threads []ReviewThread) string {
	var base string
	switch mode {
	case PromptReplace:
		base = strings.TrimSpace(text)
	case PromptAppend:
		base = DefaultPrompt
		if t := strings.TrimSpace(text); t != "" {
			base += "\n\n" + t
		}
	default:
		base = DefaultPrompt
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n")
	if d := strings.TrimSpace(prDescription); d != "" {
		b.WriteString("\n--- Pull request description ---\n")
		b.WriteString(d)
		b.WriteString("\n")
	}
	for i, t := range threads {
		b.WriteString("\n")
		b.WriteString(formatThread(i+1, t))
	}
	return b.String()
}

func formatThread(n int, t ReviewThread) string {
	var b strings.Builder
	b.WriteString("--- Comment ")
	b.WriteString(strconv.Itoa(n))
	if t.Path != "" {
		b.WriteString(" (")
		b.WriteString(t.Path)
		if t.Line > 0 {
			b.WriteString(":")
			b.WriteString(strconv.Itoa(t.Line))
		}
		b.WriteString(")")
	}
	if t.Author != "" {
		b.WriteString(" by @")
		b.WriteString(t.Author)
	}
	b.WriteString(" ---\n")
	b.WriteString(strings.TrimSpace(t.Body))
	b.WriteString("\n")
	return b.String()
}

// FormatTokenTotal renders the total-token-usage line appended to a workflow's
// result, e.g. "Total token usage across all sessions: 1,234,567 tokens." It is
// defined here (rather than reusing the piagent adapter's identical helper) so
// the application core does not depend on a driven adapter.
func FormatTokenTotal(total int) string {
	return "Total token usage across all sessions: " + groupThousands(total) + " tokens."
}

// withTokenTotal appends the total-token-usage line to a workflow summary.
func withTokenTotal(summary string, total int) string {
	return summary + "\n\n" + FormatTokenTotal(total)
}

// groupThousands formats n with comma thousands separators (1234567 ->
// "1,234,567").
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return neg + b.String()
}

// FormatReplyBody renders the commit hashes as required by the spec, e.g.
// "<sha1> + <sha2> + <sha3>". It returns an empty string when there are no
// commits.
func FormatReplyBody(commitSHAs []string) string {
	return strings.Join(commitSHAs, " + ")
}

// MaxReviewPasses caps how many implement-then-review passes the review loop
// runs before stopping, even if the review agent keeps surfacing items. Without
// a cap the loop can run indefinitely, each pass committing code and consuming a
// full agent run; the review agent is prompted to be thorough and will almost
// always find something.
const MaxReviewPasses = 5

// ReviewInput is the input to ReviewWorkflow.
type ReviewInput struct {
	// WorkDir is the repository directory the CLI was invoked from.
	WorkDir string
	// Payload is the previous pass's raw review output, carried over verbatim.
	// It is empty on the first run: with no payload the workflow only reviews;
	// with a payload it first implements that feedback—checking HEAD before and
	// after—then reviews again.
	Payload string
	// Pass counts how many implement-then-review passes have run so far. The
	// first (review-only) run is pass 0; each continue-as-new increments it. The
	// loop stops once it reaches MaxReviewPasses so it cannot run forever.
	Pass int
	// TokensSoFar carries accumulated total token usage from prior passes and,
	// when this loop was spawned by DevelopWorkflow, from that parent workflow's
	// develop session, so the terminal result reports the whole tree's usage.
	TokensSoFar int
}

// ReviewPrompt is the instruction handed to the Pi agent to review the current
// branch. It is deliberately terse; the agent decides how to review.
const ReviewPrompt = "Perform a thorough code review of the current branch"

// DevelopInput is the input to DevelopWorkflow.
type DevelopInput struct {
	// WorkDir is the repository directory the CLI was invoked from.
	WorkDir string
	// Branch is the new branch to create and develop on.
	Branch string
	// Prompt is the caller's instruction describing what to implement.
	Prompt string
}

// BuildDevelopPrompt renders the instruction that has the Pi agent implement
// the caller's task on the freshly created branch. It asks the agent to commit
// all its work so the workflow's HEAD-advanced check can confirm the change
// landed.
func BuildDevelopPrompt(prompt string) string {
	return `Implement the task described below. Read the referenced code and relevant in-repo documentation for context, then make the changes. Confirm lint/typecheck/build (and synth, if infra) pass, then commit all your work.

` + strings.TrimSpace(prompt)
}

// BuildImplementPrompt renders the instruction that has the Pi agent act on a
// code review's raw output. It asks the agent to commit its work so the
// workflow's HEAD-advanced check can confirm the change landed, and to make no
// commit when nothing needs changing so the loop can recognize convergence.
func BuildImplementPrompt(review string) string {
	return `Implement the actionable changes called for by the code review below. Read the referenced code for context and make the changes. Confirm lint/typecheck/build (and synth, if infra) pass, then commit all your work. If nothing in the review requires a code change, do not commit anything.

` + review
}
