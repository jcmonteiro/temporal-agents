package piagent

import (
	"context"
	"strings"
	"testing"
)

// fixedWindow returns a window resolver that always reports w tokens, so the
// percentage rendering can be exercised without the pi catalog.
func fixedWindow(w int) func(string, string) int {
	return func(string, string) int { return w }
}

func TestProgress_Heartbeat_ReportsTokensAndPercentOfContextWindow(t *testing.T) {
	p := progress{window: fixedWindow(200_000)}

	p.apply(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hi"}],"provider":"lego-anthropic","model":"Claude Opus 4.8","usage":{"totalTokens":18477}}}`)

	got := p.render()
	if !strings.Contains(got, "context: 18,477 tokens (9.2% of 200,000)") {
		t.Fatalf("heartbeat missing token consumption line, got:\n%s", got)
	}
}

func TestProgress_Heartbeat_TracksLatestNonZeroUsage(t *testing.T) {
	p := progress{window: fixedWindow(200_000)}

	// The first assistant message reports an all-zero usage; it must not reset
	// the running consumption to zero.
	p.apply(`{"type":"message_start","message":{"role":"assistant","content":[],"provider":"lego-anthropic","model":"Claude Opus 4.8","usage":{"totalTokens":0}}}`)
	p.apply(`{"type":"message_update","message":{"role":"assistant","content":[],"provider":"lego-anthropic","model":"Claude Opus 4.8","usage":{"totalTokens":18475}}}`)
	p.apply(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hi"}],"provider":"lego-anthropic","model":"Claude Opus 4.8","usage":{"totalTokens":18477}}}`)

	got := p.render()
	if !strings.Contains(got, "18,477 tokens") {
		t.Fatalf("expected latest usage 18,477, got:\n%s", got)
	}
}

func TestProgress_Heartbeat_OmitsPercentWhenContextWindowUnknown(t *testing.T) {
	p := progress{window: fixedWindow(0)} // window unknown

	p.apply(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hi"}],"provider":"lego-anthropic","model":"Claude Opus 4.8","usage":{"totalTokens":18477}}}`)

	got := p.render()
	if !strings.Contains(got, "context: 18,477 tokens") {
		t.Fatalf("expected absolute tokens, got:\n%s", got)
	}
	if strings.Contains(got, "%") {
		t.Fatalf("expected no percentage when window unknown, got:\n%s", got)
	}
}

func TestProgress_Heartbeat_NoContextLineBeforeAnyUsage(t *testing.T) {
	p := progress{window: fixedWindow(200_000)}

	p.apply(`{"type":"agent_start"}`)
	p.apply(`{"type":"turn_start"}`)

	if got := p.render(); strings.Contains(got, "context:") {
		t.Fatalf("expected no context line before usage is reported, got:\n%s", got)
	}
}

func TestParseContextWindows_KeysByProviderAndModelSkippingHeader(t *testing.T) {
	table := "provider        model             context  max-out  thinking  images\n" +
		"lego-anthropic  Claude Haiku 4.5  200K     64K      yes       yes   \n" +
		"lego-anthropic  Claude Opus 4.8   200K     128K     yes       yes   \n" +
		"amazon-bedrock  anthropic.claude-opus-4-8                         1M       128K     yes       yes   \n"

	m := parseContextWindows(table)

	if got := m[catalogKey("lego-anthropic", "Claude Opus 4.8")]; got != 200_000 {
		t.Fatalf("Claude Opus 4.8 window = %d, want 200000", got)
	}
	if got := m[catalogKey("amazon-bedrock", "anthropic.claude-opus-4-8")]; got != 1_000_000 {
		t.Fatalf("bedrock opus window = %d, want 1000000", got)
	}
	if _, ok := m[catalogKey("provider", "model")]; ok {
		t.Fatalf("header row must not be treated as a model")
	}
}

func TestProgress_DetectsThresholdCompactionForResume(t *testing.T) {
	var p progress
	p.apply(`{"type":"compaction_start","reason":"threshold"}`)
	p.apply(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":false}`)

	if !p.thresholdCompacted {
		t.Fatal("expected a successful threshold compaction to flag a resume")
	}
	if got := p.render(); !strings.Contains(got, "compaction complete") {
		t.Fatalf("expected compaction to appear in transcript, got:\n%s", got)
	}
}

func TestProgress_DoesNotResumeOnOverflowOrRetryOrFailure(t *testing.T) {
	cases := map[string]string{
		"overflow (auto-retried by pi)": `{"type":"compaction_end","reason":"overflow","willRetry":true}`,
		"threshold but retrying":        `{"type":"compaction_end","reason":"threshold","willRetry":true}`,
		"threshold aborted":             `{"type":"compaction_end","reason":"threshold","aborted":true,"willRetry":false}`,
		"threshold failed":              `{"type":"compaction_end","reason":"threshold","willRetry":false,"errorMessage":"boom"}`,
		"manual compaction":             `{"type":"compaction_end","reason":"manual","willRetry":false}`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			var p progress
			p.apply(line)
			if p.thresholdCompacted {
				t.Fatalf("%s must not trigger a resume", name)
			}
		})
	}
}

func TestProgress_ClearsThresholdCompactionWhenPiContinuesWithQueuedWork(t *testing.T) {
	var p progress
	// Threshold compaction flags a resume...
	p.apply(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":false}`)
	if !p.thresholdCompacted {
		t.Fatal("expected threshold compaction to flag a resume")
	}

	// ...but Pi then continues in the same invocation (e.g. an extension queued
	// a message), so the compaction is no longer the trailing event and the run
	// must not be resumed again by Run.
	p.apply(`{"type":"turn_start"}`)
	p.apply(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`)

	if p.thresholdCompacted {
		t.Fatal("expected subsequent agent/message activity to clear the trailing-compaction flag")
	}
}

func TestPiArgs_RunsNonInteractiveJSONForSession(t *testing.T) {
	args := piArgs("session-123")
	if want := []string{"-p", "--mode", "json", "--session-id", "session-123"}; strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("piArgs = %v, want %v", args, want)
	}
}

func TestRunLoop_ResumesUntilRunEndsWithoutThresholdCompaction(t *testing.T) {
	var inputs []string
	// First run ends on a threshold compaction (unfinished); second run finishes.
	results := []struct {
		result    string
		compacted bool
	}{
		{result: "partial", compacted: true},
		{result: "done", compacted: false},
	}
	i := 0
	run := func(_ context.Context, _ []string, _, input string) (string, bool, error) {
		inputs = append(inputs, input)
		r := results[i]
		i++
		return r.result, r.compacted, nil
	}

	got, err := runLoop(context.Background(), run, nil, "", "do the task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "done" {
		t.Fatalf("expected the final non-empty result, got %q", got)
	}
	if len(inputs) != 2 || inputs[0] != "do the task" || inputs[1] != continueMessage {
		t.Fatalf("expected original prompt then a continue message, got %v", inputs)
	}
}

func TestRunLoop_FailsWhenResumeCapExhaustedWhileStillCompacting(t *testing.T) {
	// Every run ends on a threshold compaction, so the task never finishes.
	var lastInput string
	run := func(_ context.Context, _ []string, _, input string) (string, bool, error) {
		lastInput = input
		return "partial", true, nil
	}

	got, err := runLoop(context.Background(), run, nil, "", "do the task")
	if err == nil {
		t.Fatalf("expected an error when the resume cap is exhausted mid-task, got result %q", got)
	}
	if got != "" {
		t.Fatalf("expected no partial result on cap exhaustion, got %q", got)
	}
	if lastInput != continueMessage {
		t.Fatalf("expected resumes to use the continue message, last input was %q", lastInput)
	}
}

func TestParseTokenSize(t *testing.T) {
	cases := map[string]int{
		"200K": 200_000,
		"1M":   1_000_000,
		"128K": 128_000,
		"4.1K": 4_100,
		"":     0,
		"yes":  0,
	}
	for in, want := range cases {
		if got := parseTokenSize(in); got != want {
			t.Errorf("parseTokenSize(%q) = %d, want %d", in, got, want)
		}
	}
}
