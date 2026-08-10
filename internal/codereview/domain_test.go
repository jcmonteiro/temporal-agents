package codereview

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/steering"
)

// pilotFactory is the shipped instruction the pilot loop uses when nothing is
// configured, read from the catalogue so the tests state what they expect without
// copying the text.
func pilotFactory(t *testing.T) string {
	t.Helper()
	spec, ok := instruction.SpecFor(instruction.KeyPilotAddress)
	if !ok {
		t.Fatal("the catalogue does not govern the pilot instruction")
	}
	return spec.Factory
}

// A pass that resolved nothing behaves exactly as it did before instructions were
// stored: the shipped instruction, the pull request's description as context, and
// one section per unresolved comment.
func TestThePilotPromptCarriesTheInstructionTheDescriptionAndTheComments(t *testing.T) {
	threads := []ReviewThread{{Path: "main.go", Line: 12, Author: "octocat", Body: "rename this"}}

	got, err := BuildPilotPrompt(PilotPrompt{
		Mode: PromptDefault, Text: "ignored when mode is default",
		Description: "Adds the widget feature", Threads: threads,
	})

	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}
	for _, want := range []string{pilotFactory(t), "Adds the widget feature", "rename this", "main.go:12", "@octocat"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q not present:\n%s", want, got)
		}
	}
}

// The instruction an operator stored for this place is what the pass is told, while
// the comments — whose shape the code produces — are appended whatever the
// instruction says.
func TestAResolvedInstructionReplacesTheShippedOneButNotTheComments(t *testing.T) {
	resolved := instruction.Resolution{{
		Key:   instruction.KeyPilotAddress,
		Text:  "Address only the comments about tests.",
		Scope: instruction.DirectoryScope("/src/agents"),
	}}
	threads := []ReviewThread{{Path: "main.go", Body: "rename this"}}

	got, err := BuildPilotPrompt(PilotPrompt{Instructions: resolved, Threads: threads})

	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}
	if !strings.Contains(got, "Address only the comments about tests.") {
		t.Fatalf("the resolved instruction is not what the agent was told:\n%s", got)
	}
	if strings.Contains(got, pilotFactory(t)) {
		t.Fatalf("the shipped instruction survived the override:\n%s", got)
	}
	if !strings.Contains(got, "rename this") {
		t.Fatalf("the comments were dropped by the override:\n%s", got)
	}
}

func TestThePilotPromptOmitsTheDescriptionSectionWhenThereIsNone(t *testing.T) {
	got, err := BuildPilotPrompt(PilotPrompt{Description: "  "})
	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}
	if strings.Contains(got, "Pull request description") {
		t.Fatalf("empty description should not add a section:\n%s", got)
	}
}

// The per-run modes the CLI already offers keep working against whatever the pass
// resolved: append adds to it, replace stands in for it.
func TestThePerRunModesCombineWithTheResolvedInstruction(t *testing.T) {
	appended, err := BuildPilotPrompt(PilotPrompt{Mode: PromptAppend, Text: "prefer table-driven tests"})
	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}
	if !strings.Contains(appended, pilotFactory(t)) || !strings.Contains(appended, "prefer table-driven tests") {
		t.Fatalf("append should keep the instruction and add the caller text:\n%s", appended)
	}

	replaced, err := BuildPilotPrompt(PilotPrompt{Mode: PromptReplace, Text: "only fix naming"})
	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}
	if strings.Contains(replaced, pilotFactory(t)) {
		t.Fatalf("replace should not include the instruction:\n%s", replaced)
	}
	if !strings.Contains(replaced, "only fix naming") {
		t.Fatalf("replace should use the caller text:\n%s", replaced)
	}
}

// The review loop's convergence rests on this prompt: the agent must be asked to
// commit its work, and told to commit nothing when there is nothing to change.
func TestTheImplementPromptEmbedsTheReviewAndAsksToCommit(t *testing.T) {
	review := "Rename X for clarity and add tests for Y."

	got, err := instruction.Render(nil, instruction.KeyReviewImplement, instruction.Data{"Review": review})

	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{review, "commit", "do not commit anything"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q not present:\n%s", want, got)
		}
	}
}

func TestBuildDevelopPrompt_EmbedsPromptAndAsksToCommit(t *testing.T) {
	prompt := "add a rate limiter to the API client"
	got := BuildDevelopPrompt(prompt)

	if !strings.Contains(got, prompt) {
		t.Fatalf("develop prompt should embed the caller's prompt:\n%s", got)
	}
	if !strings.Contains(got, "commit") {
		t.Fatalf("develop prompt should ask the agent to commit:\n%s", got)
	}
}

func TestFormatBranchAlias_ComposesAdjectiveAnimalAndLowercasedDate(t *testing.T) {
	date := time.Date(2026, time.July, 29, 13, 45, 0, 0, time.UTC)

	if got, want := FormatBranchAlias("flaming", "duck", date), "flaming-duck-2026-jul-29"; got != want {
		t.Fatalf("FormatBranchAlias = %q, want %q", got, want)
	}
}

func TestRandomBranchAlias_MatchesAdjectiveAnimalDateShape(t *testing.T) {
	date := time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)

	got := RandomBranchAlias(date)

	// The alias always ends with the day's date, regardless of which random pair
	// was chosen.
	if !strings.HasSuffix(got, "-2026-jul-29") {
		t.Fatalf("RandomBranchAlias = %q, want it to end with the date", got)
	}
	// <adjective>-<animal>-<year>-<month>-<day>: lowercase words then the date.
	if !regexp.MustCompile(`^[a-z]+-[a-z]+-2026-jul-29$`).MatchString(got) {
		t.Fatalf("RandomBranchAlias = %q, want <adjective>-<animal>-<date> shape", got)
	}
}

func TestValidateBranchName(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{"empty is allowed (generate an alias)", "", false},
		{"plain name", "feat/rate-limit", false},
		{"slashed name", "feat/x", false},
		{"traversal escapes the worktrees base dir", "../../foo", true},
		{"embedded traversal", "feat/../../foo", true},
		{"leading dash looks like a flag", "-force", true},
		{"absolute path", "/etc/evil", true},
		{"space is not a valid ref char", "feature name", true},
		{"tilde is git-special", "topic~1", true},
		{"open at-brace is git-special", "foo@{bar", true},
		{"component ending in .lock", "name.lock", true},
		{"trailing dot", "feat/x.", true},
		{"component beginning with a dot", "feat/.hidden", true},
		{"caret is git-special", "feat^1", true},
		{"colon is git-special", "feat:x", true},
		{"trailing slash", "feat/", true},
		{"consecutive slashes", "feat//x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBranchName(tt.branch)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateBranchName(%q) = nil, want error", tt.branch)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateBranchName(%q) = %v, want nil", tt.branch, err)
			}
		})
	}
}

func TestPlanWorktree_MirrorsInPlaceRetryIdempotency(t *testing.T) {
	tests := []struct {
		name           string
		adoptable      bool
		attempt        int
		worktreeExists bool
		want           worktreeStep
	}{
		{"adoptable branch, no worktree yet, first attempt", true, 1, false, createWorktreeStep},
		{"adoptable branch, worktree exists, first attempt -> reject", true, 1, true, rejectWorktreeStep},
		{"adoptable branch, worktree exists, retry -> adopt", true, 2, true, adoptWorktreeStep},
		{"adoptable branch, no worktree, retry -> create", true, 3, false, createWorktreeStep},
		{"fresh generated alias never adopts even if a path exists", false, 4, true, createWorktreeStep},
		{"fresh generated alias, no worktree", false, 1, false, createWorktreeStep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planWorktree(tt.adoptable, tt.attempt, tt.worktreeExists); got != tt.want {
				t.Fatalf("planWorktree(%v, %d, %v) = %v, want %v",
					tt.adoptable, tt.attempt, tt.worktreeExists, got, tt.want)
			}
		})
	}
}

func TestFormatReplyBody(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"none", nil, ""},
		{"one", []string{"abc123"}, "abc123"},
		{"many", []string{"a1", "b2", "c3"}, "a1 + b2 + c3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatReplyBody(tc.in); got != tc.want {
				t.Fatalf("FormatReplyBody(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The operator's guidance is added to the prompt, never merged into it: the
// instruction is what it was, the comments are what they were, and the guidance
// sits between them — immediately in front of the material it is about.
func TestGuidanceIsAddedToThePilotPromptWithoutChangingWhatWasThereBefore(t *testing.T) {
	threads := []ReviewThread{{Path: "main.go", Body: "rename this"}}
	unguided, err := BuildPilotPrompt(PilotPrompt{Threads: threads})
	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}

	guided, err := BuildPilotPrompt(PilotPrompt{Threads: threads, Guidance: "ignore the naming comment"})
	if err != nil {
		t.Fatalf("BuildPilotPrompt: %v", err)
	}

	if !strings.Contains(guided, pilotFactory(t)) {
		t.Fatalf("the instruction changed when guidance was added:\n%s", guided)
	}
	guidance := strings.Index(guided, "ignore the naming comment")
	comments := strings.Index(guided, "rename this")
	instructionEnd := strings.Index(guided, pilotFactory(t)) + len(pilotFactory(t))
	switch {
	case guidance < 0:
		t.Fatalf("the guidance never reached the agent:\n%s", guided)
	case guidance < instructionEnd:
		t.Fatalf("the guidance landed inside the instruction:\n%s", guided)
	case guidance > comments:
		t.Fatalf("the guidance landed after the comments it applies to:\n%s", guided)
	}
	// Everything the pass was already given is still there, unchanged, with only the
	// guidance block added.
	if strings.ReplaceAll(guided, steering.Block("ignore the naming comment")+"\n", "") != unguided {
		t.Fatalf("adding guidance changed a section that already existed:\n%s", guided)
	}
}
