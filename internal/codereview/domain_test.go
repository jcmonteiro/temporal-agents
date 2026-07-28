package codereview

import (
	"strings"
	"testing"
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
