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

func TestBuildStructurePrompt_EmbedsLastOutputAndForbidsReviewing(t *testing.T) {
	got := BuildStructurePrompt("rename X and add tests for Y")

	if !strings.Contains(got, `{"review":`) {
		t.Fatalf("structure prompt should describe the JSON shape:\n%s", got)
	}
	if !strings.Contains(got, "DO NOT PERFORM A CODE REVIEW") {
		t.Fatalf("structure prompt should forbid reviewing:\n%s", got)
	}
	if !strings.Contains(got, "rename X and add tests for Y") {
		t.Fatalf("structure prompt should embed the last output:\n%s", got)
	}
	// The example shape must be valid JSON (colon, not comma) so it does not bias
	// the agent toward malformed output.
	if strings.Contains(got, `{"itemName", "itemValue"}`) {
		t.Fatalf("structure prompt example must use a colon, not a comma:\n%s", got)
	}
	if !strings.Contains(got, `{"itemName": "itemValue"}`) {
		t.Fatalf("structure prompt should show a valid JSON example:\n%s", got)
	}
	// It must ask for only blocking, actionable items so the loop converges.
	if !strings.Contains(got, "blocking") {
		t.Fatalf("structure prompt should constrain to blocking items:\n%s", got)
	}
}

func TestBuildImplementPrompt_EmbedsPayloadAndAsksToCommit(t *testing.T) {
	got := BuildImplementPrompt(`{"review":[{"item":"do X"}]}`)

	if !strings.Contains(got, `{"review":[{"item":"do X"}]}`) {
		t.Fatalf("implement prompt should embed the payload:\n%s", got)
	}
	if !strings.Contains(got, "commit") {
		t.Fatalf("implement prompt should ask the agent to commit:\n%s", got)
	}
}

func TestParseReviewPayload(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantItems int
		wantErr   bool
	}{
		{
			name:      "plain object with items",
			in:        `{"review":[{"itemName":"rename foo"},{"itemName":"add test"}]}`,
			wantItems: 2,
		},
		{
			name:      "empty review array",
			in:        `{"review":[]}`,
			wantItems: 0,
		},
		{
			name:      "wrapped in prose and code fence",
			in:        "Here is the JSON:\n```json\n{\"review\":[{\"itemName\":\"fix\"}]}\n```\n",
			wantItems: 1,
		},
		{
			name:    "no json object",
			in:      "there is nothing actionable here",
			wantErr: true,
		},
		{
			name:    "malformed json",
			in:      `{"review": [ {`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseReviewPayload(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseReviewPayload(%q) = %v, want error", tc.in, p)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReviewPayload(%q) unexpected error: %v", tc.in, err)
			}
			if len(p.Review) != tc.wantItems {
				t.Fatalf("ParseReviewPayload(%q) items = %d, want %d", tc.in, len(p.Review), tc.wantItems)
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
