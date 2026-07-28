package main

import (
	"testing"

	"temporal-agents/internal/codereview"
)

// These tests cover the observable behavior of the code subcommands' flag
// parsing: the --summary toggle on pilot, review, and develop. Error paths call
// fatalf (os.Exit) and are intentionally not exercised here.

func TestParsePilotFlags_Summary(t *testing.T) {
	mode, text, chain, summary := parsePilotFlags([]string{"--summary"})
	if mode != codereview.PromptDefault || text != "" || chain {
		t.Fatalf("unexpected mode/text/chain: %q %q %v", mode, text, chain)
	}
	if !summary {
		t.Fatal("--summary should set summary = true")
	}

	// Absent by default, and independent of the other flags.
	_, _, chain, summary = parsePilotFlags([]string{"--chain"})
	if !chain || summary {
		t.Fatalf("chain=%v summary=%v, want chain=true summary=false", chain, summary)
	}
}

func TestParseReviewFlags_Summary(t *testing.T) {
	if summary := parseReviewFlags([]string{"--summary"}); !summary {
		t.Fatal("--summary should set summary = true")
	}
	if summary := parseReviewFlags(nil); summary {
		t.Fatal("summary should default to false")
	}
}

func TestParseDevelopFlags_Summary(t *testing.T) {
	prompt, branch, summary := parseDevelopFlags([]string{"do the thing", "--branch", "feat/x", "--summary"})
	if prompt != "do the thing" || branch != "feat/x" {
		t.Fatalf("prompt=%q branch=%q, unexpected", prompt, branch)
	}
	if !summary {
		t.Fatal("--summary should set summary = true")
	}

	_, _, summary = parseDevelopFlags([]string{"do the thing", "--branch=feat/x"})
	if summary {
		t.Fatal("summary should default to false")
	}
}
