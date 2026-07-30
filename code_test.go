package main

import (
	"testing"

	"temporal-agents/internal/codereview"
)

// These tests cover the observable behavior of the code subcommands' flag
// parsing: the --summary toggle on pilot, review, and develop. Error paths call
// fatalf (os.Exit) and are intentionally not exercised here.

func TestParsePilotFlags_Summary(t *testing.T) {
	mode, text, _, summary := parsePilotFlags([]string{"--summary"})
	if mode != codereview.PromptDefault || text != "" {
		t.Fatalf("unexpected mode/text: %q %q", mode, text)
	}
	if !summary {
		t.Fatal("--summary should set summary = true")
	}

	// Summary defaults to false.
	if _, _, _, summary = parsePilotFlags(nil); summary {
		t.Fatal("summary should default to false")
	}
}

func TestParsePilotFlags_Chain(t *testing.T) {
	if _, _, chain, _ := parsePilotFlags([]string{"--no-chain"}); chain {
		t.Fatal("--no-chain should set chain = false")
	}

	// Chaining is the default: a standalone pilot loops unless --no-chain is given.
	if _, _, chain, _ := parsePilotFlags(nil); !chain {
		t.Fatal("chain should default to true")
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
	prompt, branch, worktree, summary, withRemote := parseDevelopFlags([]string{"do the thing", "--branch", "feat/x", "--summary"})
	if prompt != "do the thing" || branch != "feat/x" {
		t.Fatalf("prompt=%q branch=%q, unexpected", prompt, branch)
	}
	if !summary {
		t.Fatal("--summary should set summary = true")
	}
	if worktree {
		t.Fatal("worktree should default to false")
	}
	if withRemote {
		t.Fatal("with-remote should default to false")
	}

	_, _, worktree, summary, withRemote = parseDevelopFlags([]string{"do the thing", "--branch=feat/x"})
	if summary {
		t.Fatal("summary should default to false")
	}
	if worktree {
		t.Fatal("worktree should default to false")
	}
	if withRemote {
		t.Fatal("with-remote should default to false")
	}
}

func TestParseDevelopFlags_WithRemote(t *testing.T) {
	prompt, branch, _, summary, withRemote := parseDevelopFlags([]string{"do the thing", "--branch", "feat/x", "--with-remote"})
	if prompt != "do the thing" || branch != "feat/x" {
		t.Fatalf("prompt=%q branch=%q, unexpected", prompt, branch)
	}
	if !withRemote {
		t.Fatal("--with-remote should set withRemote = true")
	}
	if summary {
		t.Fatal("summary should default to false")
	}
}

func TestParseDevelopFlags_Worktree(t *testing.T) {
	prompt, _, worktree, _, _ := parseDevelopFlags([]string{"do the thing", "--worktree"})
	if prompt != "do the thing" {
		t.Fatalf("prompt=%q, unexpected", prompt)
	}
	if !worktree {
		t.Fatal("--worktree should set worktree = true")
	}

	if _, _, worktree, _, _ = parseDevelopFlags([]string{"do the thing"}); worktree {
		t.Fatal("worktree should default to false")
	}
}

func TestParseDevelopFlags_BranchOptional_DefaultsToEmpty(t *testing.T) {
	// --branch is optional; when omitted the parser returns an empty branch and
	// the workflow generates an alias.
	prompt, branch, _, _, _ := parseDevelopFlags([]string{"do the thing"})
	if prompt != "do the thing" {
		t.Fatalf("prompt=%q, unexpected", prompt)
	}
	if branch != "" {
		t.Fatalf("branch should default to empty, got %q", branch)
	}
}
