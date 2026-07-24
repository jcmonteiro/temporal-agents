package codereview

import (
	"strings"
	"testing"
)

func TestBuildPrompt_DefaultIncludesBuiltInPromptAndComments(t *testing.T) {
	threads := []ReviewThread{{Path: "main.go", Line: 12, Author: "octocat", Body: "rename this"}}

	got := BuildPrompt(PromptDefault, "ignored when mode is default? no", threads)

	// Default mode ignores caller text and uses the built-in prompt.
	if !strings.Contains(got, DefaultPrompt) {
		t.Fatalf("default prompt not present:\n%s", got)
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

func TestBuildPrompt_AppendKeepsDefaultAndAddsText(t *testing.T) {
	got := BuildPrompt(PromptAppend, "prefer table-driven tests", nil)

	if !strings.Contains(got, DefaultPrompt) {
		t.Fatalf("append should keep the default prompt:\n%s", got)
	}
	if !strings.Contains(got, "prefer table-driven tests") {
		t.Fatalf("append should include the caller text:\n%s", got)
	}
}

func TestBuildPrompt_ReplaceDropsDefault(t *testing.T) {
	got := BuildPrompt(PromptReplace, "only fix naming", nil)

	if strings.Contains(got, DefaultPrompt) {
		t.Fatalf("replace should not include the default prompt:\n%s", got)
	}
	if !strings.Contains(got, "only fix naming") {
		t.Fatalf("replace should use the caller text:\n%s", got)
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
