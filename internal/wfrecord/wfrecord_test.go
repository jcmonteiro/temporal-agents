package wfrecord

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/execstore"
)

func TestStatusOf_SuccessAndFailure(t *testing.T) {
	require.Equal(t, execstore.StatusSucceeded, StatusOf(nil))
	require.Equal(t, execstore.StatusFailed, StatusOf(errors.New("pi crashed")))
}

func TestStatusOf_ContinueAsNewIsASuccessfulIteration(t *testing.T) {
	// Continuing as new is a control signal, not a failure: the iteration emitting
	// it did its own work and settled, and the next iteration is a row of its own.
	err := fmt.Errorf("wrapped: %w", &workflow.ContinueAsNewError{})

	require.Equal(t, execstore.StatusSucceeded, StatusOf(err))
	require.True(t, IsContinueAsNew(err))
	require.Empty(t, FailureText(err), "a chained iteration has no failure to record")
}

func TestFailureText_OnlyForRealFailures(t *testing.T) {
	require.Empty(t, FailureText(nil))
	require.Equal(t, "pi crashed", FailureText(errors.New("pi crashed")))
}

func TestFailureText_GoesThroughTheSanitizer(t *testing.T) {
	// git echoes the remote it failed on, and a token-authenticated remote embeds
	// the token in it. The record is long-lived, so the token must not reach it.
	err := errors.New("unable to access 'https://x-access-token:ghs_0123456789abcdefghij@github.com/o/r.git/'")

	text := FailureText(err)

	require.NotContains(t, text, "ghs_0123456789abcdefghij")
	require.Contains(t, text, "github.com/o/r.git", "the useful part of the message survives")
}

func TestSanitize_RemovesCredentialsAndKeepsTheRest(t *testing.T) {
	cases := map[string]struct{ in, mustNotContain, mustContain string }{
		"credential in a URL": {
			in:             "fatal: could not read from 'https://user:s3cret@example.com/repo.git'",
			mustNotContain: "s3cret",
			mustContain:    "example.com/repo.git",
		},
		"bare token in agent output": {
			in:             "Authorization: Bearer ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
			mustNotContain: "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
			mustContain:    "Authorization: Bearer",
		},
		"fine-grained personal access token": {
			in:             "token github_pat_11ABCDEFG0123456789_abcdefghijklmnop failed",
			mustNotContain: "github_pat_11ABCDEFG0123456789",
			mustContain:    "failed",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := Sanitize(c.in)

			require.NotContains(t, got, c.mustNotContain)
			require.Contains(t, got, c.mustContain)
			require.Contains(t, got, "REDACTED", "a removed secret is visibly removed")
		})
	}
}

func TestSanitize_LeavesOrdinaryTextAlone(t *testing.T) {
	plain := "develop failed: the agent exited with status 1\nsee https://github.com/o/r/pull/7"

	require.Equal(t, plain, Sanitize(plain))
	require.Empty(t, Sanitize(""))
}

func TestSanitize_CapsUnboundedText(t *testing.T) {
	// A node's detail is a whole agent output or stderr dump, and a fleet parent
	// holds one per node, so an uncapped field would grow a row without bound.
	got := Sanitize(strings.Repeat("x", 4*MaxDetailText))

	require.Less(t, len(got), MaxDetailText+len(truncationMarker)+40)
	require.Contains(t, got, truncationMarker, "a shortened value says that it was shortened")
}

func TestSanitize_CappedTextStaysValidUTF8(t *testing.T) {
	// The detail is stored in a jsonb column, so a cut through a multi-byte rune
	// would corrupt the write rather than shorten it. "æ" is two bytes, so cutting
	// at the byte budget lands mid-rune.
	got := Sanitize(strings.Repeat("æ", MaxDetailText))

	require.True(t, utf8.ValidString(got))
}
