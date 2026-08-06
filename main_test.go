package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncate_KeepsShortTextAsItIs(t *testing.T) {
	require.Equal(t, "tidy the parser", truncate("  tidy the parser  ", 60))
}

func TestTruncate_NeverCutsARuneInHalf(t *testing.T) {
	// The text this shortens is agent-written or agent-recorded, so arrows, ellipses
	// and accented characters are routine. Cutting one in half would print replacement
	// characters and undo the rune-safe capping wfrecord does one layer down.
	multiByte := "review did not converge: " + strings.Repeat("→", 20)

	got := truncate(multiByte, 60)

	require.True(t, utf8.ValidString(got), "a shortened value must stay valid UTF-8")
	require.True(t, strings.HasSuffix(got, "…"), "the cut is marked")
	require.LessOrEqual(t, len(got), 60+len("…"))
}
