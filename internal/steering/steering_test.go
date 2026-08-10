package steering

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A decision is the whole contract between the operator and the loop, so the rules
// about what may be sent are stated once, as a table, and read as sentences an
// operator would recognise.
func TestWhatMayBeDecided(t *testing.T) {
	cases := []struct {
		name     string
		decision Decision
		refused  bool
	}{
		{
			name:     "guiding carries the operator's text",
			decision: Decision{Choice: ChoiceGuide, Guidance: "keep the public API as it is"},
		},
		{
			name:     "guiding with nothing to say is refused",
			decision: Decision{Choice: ChoiceGuide},
			refused:  true,
		},
		{
			name:     "guiding with only whitespace is refused",
			decision: Decision{Choice: ChoiceGuide, Guidance: "   \n\t"},
			refused:  true,
		},
		{
			name:     "guidance past the bound is refused rather than shortened",
			decision: Decision{Choice: ChoiceGuide, Guidance: strings.Repeat("x", MaxGuidanceLength+1)},
			refused:  true,
		},
		{
			name:     "proceeding without guidance needs no text",
			decision: Decision{Choice: ChoiceSkip},
		},
		{
			name:     "stopping needs no text",
			decision: Decision{Choice: ChoiceStop},
		},
		{
			name:     "text sent with a skip is refused, because it would never be used",
			decision: Decision{Choice: ChoiceSkip, Guidance: "please also rename it"},
			refused:  true,
		},
		{
			name:     "a choice nobody offers is refused",
			decision: Decision{Choice: "build-it-yourself"},
			refused:  true,
		},
		{
			name:     "no choice at all is not a decision",
			decision: Decision{},
			refused:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decision.Validate()
			if tc.refused {
				require.ErrorIs(t, err, ErrInvalidDecision)
				return
			}
			require.NoError(t, err)
		})
	}
}

// The block is what the agent actually reads, so its shape is pinned: it is added,
// never merged into what was already there, and it sits immediately in front of the
// material it is about.
func TestHowGuidanceReachesTheAgent(t *testing.T) {
	const material = "--- Comment 1 (main.go:12) ---\nrename this"

	cases := []struct {
		name     string
		guidance string
		want     string
	}{
		{
			name:     "no guidance leaves the material exactly as it was",
			guidance: "",
			want:     material,
		},
		{
			name:     "whitespace is not guidance either",
			guidance: "  \n ",
			want:     material,
		},
		{
			name:     "guidance is fenced and put in front of the material",
			guidance: "ignore the naming comment",
			want: "--- Operator guidance ---\nignore the naming comment\n--- End of operator guidance ---\n" +
				material,
		},
		{
			name:     "the operator's own spacing is trimmed at the fences, not inside",
			guidance: "\n\nfirst point\n\nsecond point\n\n",
			want: "--- Operator guidance ---\nfirst point\n\nsecond point\n--- End of operator guidance ---\n" +
				material,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, WithGuidance(tc.guidance, material))
		})
	}
}

// The session that pauses a run is named after that run, and nothing else, because
// conversational memory is keyed on that name for the session's whole life.
func TestASessionIsNamedAfterTheRunItPauses(t *testing.T) {
	first := SessionID("run-id-1")
	require.Equal(t, first, SessionID("run-id-1"), "one run always names one session")
	require.NotEqual(t, first, SessionID("run-id-2"), "a later pass is a session of its own")
}
