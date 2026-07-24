// Package codereview implements the "code review-loop" feature: a Temporal
// workflow that lets the Pi agent address the unresolved review comments on the
// open pull request for the current branch, then replies to and resolves those
// comments and requests a fresh Copilot review.
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
}

// PullRequest identifies the open PR the loop operates on.
type PullRequest struct {
	Number  int
	URL     string
	Owner   string
	Repo    string
	HeadRef string
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
const DefaultPrompt = `You are addressing reviewer feedback on an open pull request.

For each unresolved review comment below:
  - Understand what the reviewer is asking for.
  - Make the necessary code changes in the current repository.
  - Keep changes focused and minimal; do not address anything not raised.

When you are done, commit your work with clear, conventional commit messages.
Make at least one commit if you changed anything. Do not push.

The unresolved review comments follow.`

// BuildPrompt combines the base prompt (default, appended, or replaced) with a
// formatted rendering of the unresolved review threads.
func BuildPrompt(mode PromptMode, text string, threads []ReviewThread) string {
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

// FormatReplyBody renders the commit hashes as required by the spec, e.g.
// "<sha1> + <sha2> + <sha3>". It returns an empty string when there are no
// commits.
func FormatReplyBody(commitSHAs []string) string {
	return strings.Join(commitSHAs, " + ")
}
