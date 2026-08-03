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
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	// Chain, when true, continues the loop as new after a successful pass that
	// addressed comments so it keeps folding in new review feedback until a
	// pass finds nothing left to address. Every caller enables it: the pilot
	// loop always chains (the standalone `code pilot` command and the pilot
	// stage of `develop --with-remote` both set it), so a pass that addresses
	// comments always loops rather than stopping after one pass.
	Chain bool
	// TokensSoFar carries the accumulated total token usage from prior passes of
	// a chained run, so the terminal result reports the whole chain's usage.
	TokensSoFar int
	// Summary, when true, runs a final activity before the workflow returns
	// (on success or failure) that summarizes the last Pi execution and sends
	// that summary as the webhook notification's body (only the webhook).
	Summary bool
	// ChainSummary carries the webhook summary of the most recent addressed pass
	// across continue-as-new. With --chain --summary the terminal success is the
	// no-comments pass: it runs no agent (its own Pi session is empty) and lands
	// under a new RunID, and because piagent keys the Pi session on the run, that
	// pass cannot summarize the real work. So each addressing pass summarizes
	// itself while its session is still live and carries the text here, and the
	// terminal notification attaches this preserved summary instead. It is set
	// only when both Chain and Summary are enabled.
	ChainSummary string
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

// withDevelopStepTokens appends a develop-step-scoped token-usage line to a
// summary. It is used by the supervised `develop --with-remote` terminal summary,
// where the review and pilot children run in their own sessions and report their
// own totals separately; no single figure the parent holds covers all sessions,
// so "across all sessions" wording would over-claim.
func withDevelopStepTokens(summary string, total int) string {
	return summary + "\n\nDevelop step token usage: " + groupThousands(total) +
		" tokens. The review and pilot stages report their own token totals separately."
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
	// Summary, when true, runs a final activity before the workflow returns
	// (on success or failure) that summarizes the last Pi execution and sends
	// that summary as the webhook notification's body (only the webhook).
	Summary bool
}

// ReviewPrompt is the instruction handed to the Pi agent to review the current
// branch. It is deliberately terse; the agent decides how to review.
const ReviewPrompt = "Perform a thorough code review of the current branch"

// SummarizePrompt is handed to the Pi agent, resuming the workflow run's
// session, to condense the work it just performed. Because piagent keys the
// session on the workflow run, running it as a later activity in the same run
// continues the last execution's session, so the agent summarizes what it
// actually did. The result is delivered as the webhook notification body.
//
// This only holds when an agent activity actually ran earlier in the workflow
// run; on terminal paths where none did, the step is skipped entirely (see the
// agentRan guard on summarizeForWebhook) so the agent is never asked to
// summarize a fresh, empty session.
const SummarizePrompt = "Summarize the work performed in this session in a few sentences: what changed and why, at a high level. Do not make any further code changes."

// DevelopInput is the input to DevelopWorkflow.
type DevelopInput struct {
	// WorkDir is the repository directory the CLI was invoked from.
	WorkDir string
	// Branch is the new branch to create and develop on. When empty, the workflow
	// generates a random alias (see RandomBranchAlias).
	Branch string
	// WorktreesDir, when non-empty, makes the workflow develop in a fresh git
	// worktree created under this directory (at <WorktreesDir>/<branch>) instead of
	// switching the branch in WorkDir, leaving WorkDir untouched.
	WorktreesDir string
	// Prompt is the caller's instruction describing what to implement.
	Prompt string
	// Summary, when true, runs a final activity before the workflow returns
	// (on success or failure) that summarizes the last Pi execution and sends
	// that summary as the webhook notification's body (only the webhook). It is
	// also propagated to the review loop this workflow spawns.
	Summary bool
	// WithRemote, when true, extends the flow past the local review loop onto
	// GitHub: after development and review converge, this workflow supervises an
	// open-PR-and-Copilot-request stage and then the pilot loop, waiting for each
	// to complete before returning. When false the workflow keeps its original
	// behavior: it starts the review loop as an abandoned child and returns.
	WithRemote bool
}

// OpenPRInput is the input to OpenPRWorkflow.
type OpenPRInput struct {
	// WorkDir is the repository directory the CLI was invoked from.
	WorkDir string
}

// OpenPRResult is the structured outcome of OpenPRWorkflow. It carries both the
// human-readable summary and the PR URL so callers (e.g. the remote develop
// pipeline, and in turn the fleet orchestrator) can surface the link rather
// than having to scrape it out of prose.
type OpenPRResult struct {
	// Summary is the outcome-neutral, human-readable summary line.
	Summary string
	// URL is the pull request's web URL.
	URL string
}

// branchAdjectives and branchAnimals seed the auto-generated branch alias used
// when `code develop` is run without an explicit --branch. A name is composed as
// <adjective>-<animal>-<date> (e.g. "flaming-duck-2026-jul-29"). With 15 of each
// there are 225 adjective/animal pairs per day, so a same-day collision is
// unlikely; when one does happen CreateBranch simply fails and its retry picks a
// fresh alias.
var (
	branchAdjectives = []string{
		"dramatic", "squishy", "jittery", "befuddled", "overcaffeinated",
		"wonky", "peculiar", "sneezy", "bumbling", "ridiculous",
		"flabbergasted", "flaming", "waddling", "grumpy", "sparkly",
	}
	branchAnimals = []string{
		"badger", "pangolin", "ferret", "octopus", "capybara",
		"gecko", "raven", "narwhal", "mongoose", "yak",
		"salamander", "fox", "moose", "jellyfish", "duck",
	}
)

// FormatBranchAlias renders a branch alias from its parts as
// <adjective>-<animal>-<date>, with the date lower-cased as "2006-jan-02"
// (e.g. FormatBranchAlias("flaming", "duck", ...jul 29 2026) ->
// "flaming-duck-2026-jul-29").
func FormatBranchAlias(adjective, animal string, date time.Time) string {
	return fmt.Sprintf("%s-%s-%s", adjective, animal, strings.ToLower(date.Format("2006-Jan-02")))
}

// RandomBranchAlias picks a random adjective/animal pair and combines it with
// now's date into a branch alias. It is intentionally impure (uses the default
// math/rand source): each call yields an independently chosen alias, so a
// CreateBranch retry after a name collision (which regenerates via this) gets a
// fresh name. The alias CreateBranch settles on is then persisted across retries
// (see generatedAlias) rather than regenerated on every attempt. The pure
// formatting lives in FormatBranchAlias.
func RandomBranchAlias(now time.Time) string {
	adjective := branchAdjectives[rand.Intn(len(branchAdjectives))]
	animal := branchAnimals[rand.Intn(len(branchAnimals))]
	return FormatBranchAlias(adjective, animal, now)
}

// ValidateBranchName rejects explicit branch names that are unsafe to use
// verbatim as a filesystem path or a git argument, and names git itself would
// refuse. In worktree mode CreateBranch joins <WorktreesDir>/<branch>, so a
// traversing or absolute name could escape the worktrees base directory; and a
// name beginning with "-" can be mistaken for a flag by git's argument parsing.
// Beyond those, the value must satisfy git's own branch-name contract (the same
// rules `git check-ref-format --branch` enforces): otherwise a malformed name
// like "feature name", "topic~1", "foo@{bar", or "name.lock" would pass here only
// for git to reject it, and the activity would retry that permanent error until
// its attempts are exhausted. Validating it up front fails such names
// immediately (as InvalidBranch). An empty name is allowed and means "generate
// an alias" (see RandomBranchAlias); it never reaches git or the filesystem
// verbatim.
func ValidateBranchName(branch string) error {
	if branch == "" {
		return nil
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("branch name %q may not start with '-'", branch)
	}
	if filepath.IsAbs(branch) {
		return fmt.Errorf("branch name %q may not be an absolute path", branch)
	}
	return validateGitRefName(branch)
}

// validateGitRefName enforces the branch-name rules of `git check-ref-format
// --branch` (see `git help check-ref-format`) in pure Go, so a name git would
// reject is caught before it reaches the filesystem or git. Keeping this a pure
// domain check (rather than shelling out through the Git port) lets the CLI and
// the activity both validate without invoking git.
func validateGitRefName(branch string) error {
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("branch name %q may not begin or end with '/'", branch)
	}
	if strings.Contains(branch, "//") {
		return fmt.Errorf("branch name %q may not contain consecutive slashes", branch)
	}
	if strings.HasSuffix(branch, ".") {
		return fmt.Errorf("branch name %q may not end with '.'", branch)
	}
	if strings.Contains(branch, "..") {
		return fmt.Errorf("branch name %q may not contain '..'", branch)
	}
	if strings.Contains(branch, "@{") {
		return fmt.Errorf("branch name %q may not contain '@{'", branch)
	}
	if branch == "@" {
		return fmt.Errorf("branch name %q may not be the single character '@'", branch)
	}
	// Disallowed characters anywhere: ASCII control chars and DEL, space, and the
	// git-special characters ~ ^ : ? * [ \.
	for _, r := range branch {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("branch name %q may not contain control characters", branch)
		}
		switch r {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return fmt.Errorf("branch name %q may not contain %q", branch, r)
		}
	}
	// No slash-separated component may be empty, begin with '.', or end with
	// ".lock".
	for _, comp := range strings.Split(branch, "/") {
		if comp == "" {
			return fmt.Errorf("branch name %q may not contain an empty path component", branch)
		}
		if strings.HasPrefix(comp, ".") {
			return fmt.Errorf("branch name %q path component %q may not begin with '.'", branch, comp)
		}
		if strings.HasSuffix(comp, ".lock") {
			return fmt.Errorf("branch name %q path component %q may not end with '.lock'", branch, comp)
		}
	}
	return nil
}

// worktreeStep is the action createWorktree takes for a requested branch
// worktree, decided purely from the retry attempt and whether a worktree for
// the branch already exists on disk.
type worktreeStep int

const (
	// createWorktreeStep means no worktree exists yet for the branch; create it.
	createWorktreeStep worktreeStep = iota
	// adoptWorktreeStep means a prior attempt already created the worktree
	// (attempt > 1); reuse it rather than failing on git's "already exists" error.
	adoptWorktreeStep
	// rejectWorktreeStep means a stable-named branch's worktree already exists on
	// the first attempt, i.e. the caller asked to develop on a branch that is
	// already checked out somewhere; reject it (mirrors the in-place BranchExists
	// guard).
	rejectWorktreeStep
)

// planWorktree decides how createWorktree should handle a requested branch
// worktree. It mirrors CreateBranch's in-place idempotency for the worktree
// path. adoptable is true for a name that is stable across retries — an
// explicit branch, or a generated alias recovered from a prior attempt — so its
// worktree may be the residue of an earlier attempt: it is rejected on the first
// attempt (indistinguishable from asking to develop on a pre-existing branch)
// but adopted on a Temporal retry (attempt > 1). A freshly generated alias
// (adoptable false) has a brand-new path, so it never adopts and always creates.
func planWorktree(adoptable bool, attempt int, worktreeExists bool) worktreeStep {
	if !adoptable || !worktreeExists {
		return createWorktreeStep
	}
	if attempt > 1 {
		return adoptWorktreeStep
	}
	return rejectWorktreeStep
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
