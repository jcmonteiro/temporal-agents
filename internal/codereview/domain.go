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
	"encoding/json"
	"errors"
	"fmt"
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
	// Payload is the structured JSON review actions carried over from the
	// previous pass. It is empty on the first run: with no payload the workflow
	// only reviews; with a payload it first implements the actions—checking
	// HEAD before and after—then reviews again.
	Payload string
	// Pass counts how many implement-then-review passes have run so far. The
	// first (review-only) run is pass 0; each continue-as-new increments it. The
	// loop stops once it reaches MaxReviewPasses so it cannot run forever.
	Pass int
}

// ReviewPrompt is the instruction handed to the Pi agent to review the current
// branch. It is deliberately terse; the agent decides how to review.
const ReviewPrompt = "Perform a thorough code review of the current branch"

// ReviewPayload is the structured output of the review-structuring step. Each
// review item is an arbitrary object of name/value pairs, matching the shape
// requested from the agent: {"review": [{"itemName": "itemValue"}, ...]}.
type ReviewPayload struct {
	Review []map[string]any `json:"review"`
}

// BuildStructurePrompt renders the structuring instruction around a review's
// last output. This hardens the flow by forcing the free-form review text into
// the JSON shape the rest of the workflow expects. It asks for only blocking,
// actionable items so the loop converges instead of chasing every nitpick the
// review surfaced.
func BuildStructurePrompt(lastOutput string) string {
	return `Structure in JSON format {"review": [{"itemName": "itemValue"}, {"itemName": "itemValue"}]} the actions from the code review described below (DO NOT PERFORM A CODE REVIEW). Include ONLY blocking, actionable items that require a concrete code change; omit nitpicks, praise, and anything that cannot be actioned. If nothing is blocking, return {"review": []}: ` + lastOutput
}

// BuildImplementPrompt renders the instruction that has the Pi agent implement
// the actions carried in a structured review payload. It asks the agent to
// commit its work so the workflow's HEAD-advanced check can confirm the change
// actually landed.
func BuildImplementPrompt(payload string) string {
	return `Implement the actions from the structured code review below. For each item, read the referenced code for context and make the change. Confirm lint/typecheck/build (and synth, if infra) pass, then commit all your work.

` + payload
}

// ParseReviewPayload extracts and validates the structured review JSON produced
// by the structuring step. It tolerates surrounding prose or Markdown code
// fences by parsing the outermost JSON object, and fails when no object is
// present or it does not decode into the expected schema.
func ParseReviewPayload(s string) (ReviewPayload, error) {
	js := extractJSONObject(s)
	if js == "" {
		return ReviewPayload{}, errors.New("no JSON object found in review output")
	}
	var p ReviewPayload
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return ReviewPayload{}, fmt.Errorf("parse review JSON: %w", err)
	}
	return p, nil
}

// FilterActionable drops review items the implement pass could not act on. An
// item is actionable only when it carries at least one non-empty string field:
// empty objects and items whose values are all blank are noise that would
// otherwise force another implement pass (which then fails with NoCommits when
// there is nothing to change). Filtering here keeps "actionable" from meaning
// merely "non-empty array".
func FilterActionable(p ReviewPayload) ReviewPayload {
	out := ReviewPayload{Review: make([]map[string]any, 0, len(p.Review))}
	for _, item := range p.Review {
		if itemIsActionable(item) {
			out.Review = append(out.Review, item)
		}
	}
	return out
}

// itemIsActionable reports whether a review item carries any non-blank string
// content the agent could act on. Non-string values (numbers, bools, nested
// objects) also count as content, since they signal a populated item.
func itemIsActionable(item map[string]any) bool {
	for _, v := range item {
		switch val := v.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				return true
			}
		case nil:
			// blank
		default:
			return true
		}
	}
	return false
}

// extractJSONObject returns the substring spanning the first "{" through the
// last "}", or "" when no such span exists. This is enough to peel a JSON
// object out of an agent reply that may wrap it in explanation or code fences.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
