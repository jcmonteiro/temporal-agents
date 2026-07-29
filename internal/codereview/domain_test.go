package codereview

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBuildPrompt_DefaultIncludesBuiltInPromptDescriptionAndComments(t *testing.T) {
	threads := []ReviewThread{{Path: "main.go", Line: 12, Author: "octocat", Body: "rename this"}}

	got := BuildPrompt(PromptDefault, "ignored when mode is default", "Adds the widget feature", threads)

	// Default mode ignores caller text and uses the built-in prompt.
	if !strings.Contains(got, DefaultPrompt) {
		t.Fatalf("default prompt not present:\n%s", got)
	}
	if !strings.Contains(got, "Adds the widget feature") {
		t.Fatalf("PR description not present:\n%s", got)
	}
	if !strings.Contains(got, "rename this") {
		t.Fatalf("comment body not present:\n%s", got)
	}
	if !strings.Contains(got, "main.go:12") {
		t.Fatalf("comment location not present:\n%s", got)
	}
	if !strings.Contains(got, "@octocat") {
		t.Fatalf("comment author not present:\n%s", got)
	}
}

func TestBuildPrompt_OmitsDescriptionSectionWhenEmpty(t *testing.T) {
	got := BuildPrompt(PromptDefault, "", "  ", nil)
	if strings.Contains(got, "Pull request description") {
		t.Fatalf("empty description should not add a section:\n%s", got)
	}
}

func TestBuildPrompt_AppendKeepsDefaultAndAddsText(t *testing.T) {
	got := BuildPrompt(PromptAppend, "prefer table-driven tests", "", nil)

	if !strings.Contains(got, DefaultPrompt) {
		t.Fatalf("append should keep the default prompt:\n%s", got)
	}
	if !strings.Contains(got, "prefer table-driven tests") {
		t.Fatalf("append should include the caller text:\n%s", got)
	}
}

func TestBuildPrompt_ReplaceDropsDefault(t *testing.T) {
	got := BuildPrompt(PromptReplace, "only fix naming", "", nil)

	if strings.Contains(got, DefaultPrompt) {
		t.Fatalf("replace should not include the default prompt:\n%s", got)
	}
	if !strings.Contains(got, "only fix naming") {
		t.Fatalf("replace should use the caller text:\n%s", got)
	}
}

func TestBuildImplementPrompt_EmbedsReviewAndAsksToCommit(t *testing.T) {
	review := "Rename X for clarity and add tests for Y."
	got := BuildImplementPrompt(review)

	if !strings.Contains(got, review) {
		t.Fatalf("implement prompt should embed the review output:\n%s", got)
	}
	if !strings.Contains(got, "commit") {
		t.Fatalf("implement prompt should ask the agent to commit:\n%s", got)
	}
	// It must tell the agent not to commit when nothing needs changing, so the
	// workflow's no-commits success exit is reachable.
	if !strings.Contains(got, "do not commit anything") {
		t.Fatalf("implement prompt should permit making no commit when nothing changes:\n%s", got)
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
