package main

import (
	"testing"

	"temporal-agents/internal/codereview"
	"temporal-agents/internal/setting"
)

// These tests cover the observable behavior of the code subcommands' flag
// parsing: the --summary toggle on pilot, review, and develop. Error paths call
// fatalf (os.Exit) and are intentionally not exercised here.

func TestParsePilotFlags_Summary(t *testing.T) {
	mode, text, _, summary, _ := parsePilotFlags([]string{"--summary"})
	if mode != codereview.PromptDefault || text != "" {
		t.Fatalf("unexpected mode/text: %q %q", mode, text)
	}
	if !summary {
		t.Fatal("--summary should set summary = true")
	}

	// Summary defaults to false.
	if _, _, _, summary, _ = parsePilotFlags(nil); summary {
		t.Fatal("summary should default to false")
	}
}

func TestParsePilotFlags_Chain(t *testing.T) {
	if _, _, chain, _, _ := parsePilotFlags([]string{"--no-chain"}); chain {
		t.Fatal("--no-chain should set chain = false")
	}

	// Chaining is the default: a standalone pilot loops unless --no-chain is given.
	if _, _, chain, _, _ := parsePilotFlags(nil); !chain {
		t.Fatal("chain should default to true")
	}
}

func TestParsePilotFlags_SteeringDefaultsOnAndCanBeDisabled(t *testing.T) {
	_, _, _, _, steering := parsePilotFlags(nil)
	if !steering {
		t.Fatal("steering should default to true")
	}
	_, _, _, _, steering = parsePilotFlags([]string{"--no-steering"})
	if steering {
		t.Fatal("--no-steering should set steering = false")
	}
}

func TestParseReviewFlags_Summary(t *testing.T) {
	if summary, _ := parseReviewFlags([]string{"--summary"}); !summary {
		t.Fatal("--summary should set summary = true")
	}
	if summary, _ := parseReviewFlags(nil); summary {
		t.Fatal("summary should default to false")
	}
}

func TestParseReviewFlags_SteeringDefaultsOnAndCanBeDisabled(t *testing.T) {
	_, steering := parseReviewFlags(nil)
	if !steering {
		t.Fatal("steering should default to true")
	}
	_, steering = parseReviewFlags([]string{"--no-steering"})
	if steering {
		t.Fatal("--no-steering should set steering = false")
	}
}

func TestParseDevelopFlags_Summary(t *testing.T) {
	prompt, branch, worktree, summary, withRemote, _ := parseDevelopFlags([]string{"do the thing", "--branch", "feat/x", "--summary"})
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

	_, _, worktree, summary, withRemote, _ = parseDevelopFlags([]string{"do the thing", "--branch=feat/x"})
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
	prompt, branch, _, summary, withRemote, _ := parseDevelopFlags([]string{"do the thing", "--branch", "feat/x", "--with-remote"})
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
	prompt, _, worktree, _, _, _ := parseDevelopFlags([]string{"do the thing", "--worktree"})
	if prompt != "do the thing" {
		t.Fatalf("prompt=%q, unexpected", prompt)
	}
	if !worktree {
		t.Fatal("--worktree should set worktree = true")
	}

	if _, _, worktree, _, _, _ = parseDevelopFlags([]string{"do the thing"}); worktree {
		t.Fatal("worktree should default to false")
	}
}

func TestParseDevelopFlags_BranchOptional_DefaultsToEmpty(t *testing.T) {
	// --branch is optional; when omitted the parser returns an empty branch and
	// the workflow generates an alias.
	prompt, branch, _, _, _, _ := parseDevelopFlags([]string{"do the thing"})
	if prompt != "do the thing" {
		t.Fatalf("prompt=%q, unexpected", prompt)
	}
	if branch != "" {
		t.Fatalf("branch should default to empty, got %q", branch)
	}
}

func TestParseDevelopFlags_SteeringDefaultsOnAndCanBeDisabled(t *testing.T) {
	_, _, _, _, _, steering := parseDevelopFlags([]string{"do the thing"})
	if !steering {
		t.Fatal("steering should default to true")
	}

	_, _, _, _, _, steering = parseDevelopFlags([]string{"do the thing", "--no-steering"})
	if steering {
		t.Fatal("--no-steering should set steering = false")
	}
}

func TestCLISteeringSettingsOnlyOverrideTheDefaultWhenDisabled(t *testing.T) {
	if settings := cliSteeringSettings(true); len(settings) != 0 {
		t.Fatalf("default steering should resolve through scoped settings, got %+v", settings)
	}
	settings := cliSteeringSettings(false)
	if settings.Enabled(setting.KeySteeringEnabled) {
		t.Fatalf("disabled steering should carry an explicit off value, got %+v", settings)
	}
	if len(settings) != 1 {
		t.Fatalf("disabled steering should carry one setting, got %+v", settings)
	}
}
