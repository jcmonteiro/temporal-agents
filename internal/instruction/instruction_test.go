package instruction

import (
	"errors"
	"strings"
	"testing"
)

// Every shipped default is text this build will hand to an agent, so it has to
// satisfy the rules its own key states. A default that could not be saved as an
// override is a default nobody could restore, and it would be published into storage
// at every startup.
func TestEveryShippedDefaultSatisfiesTheRulesItsKeyStates(t *testing.T) {
	for _, spec := range Specs() {
		if err := spec.Validate(spec.Factory); err != nil {
			t.Errorf("the shipped default for %s is not usable: %v", spec.Key, err)
		}
	}
}

// The point of declaring inserts is that an override cannot quietly drop the
// material the agent must act on. This is the refusal that promise rests on, and the
// refusal has to name what is missing: "invalid" is not something an operator can
// act on.
func TestAnOverrideThatDropsRequiredMaterialIsRefusedByName(t *testing.T) {
	spec, ok := SpecFor(KeyReviewImplement)
	if !ok {
		t.Fatalf("the catalogue does not govern %s", KeyReviewImplement)
	}

	err := spec.Validate("Fix everything the review found, then commit.")

	if !errors.Is(err, ErrInvalidText) {
		t.Fatalf("Validate = %v, want an invalid-instruction refusal", err)
	}
	if !strings.Contains(err.Error(), "{{.Review}}") {
		t.Fatalf("the refusal %q does not name the missing insert", err)
	}
}

// An operator moving the insert around, or rendering it twice, is doing exactly what
// overriding is for: the rule is that the material is rendered, not where it sits.
func TestAnOverrideThatMovesTheRequiredMaterialIsAccepted(t *testing.T) {
	spec, _ := SpecFor(KeyReviewImplement)

	if err := spec.Validate("Act on this review:\n\n{{.Review}}\n\nCommit your work."); err != nil {
		t.Fatalf("moving the insert was refused: %v", err)
	}
}

// The other half of the safety story: text a parser depends on is the system's own,
// so an override cannot decide whether the agent sees the comments at all. The
// system's block renders even for an override that never mentions it.
func TestTheQuestioningInstructionAlwaysReceivesTheReviewMaterial(t *testing.T) {
	spec, ok := SpecFor(KeySteeringQuestion)
	if !ok {
		t.Fatal("the questioning instruction is not governed")
	}

	rendered, err := spec.Render(spec.Factory, Data{"Material": "the retry hides the error"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "the retry hides the error") {
		t.Fatalf("questioning prompt does not contain its review material: %q", rendered)
	}
}

func TestTheSystemsOwnBlockIsAppendedToAnyOverride(t *testing.T) {
	spec, _ := SpecFor(KeyPilotAddress)

	rendered, err := spec.Render("Address the comments in the order they were made.",
		Data{"Comments": "--- Comment 1 ---\nfix the typo"})

	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(rendered, "Address the comments in the order they were made.") {
		t.Fatalf("the override is not the start of the prompt: %q", rendered)
	}
	if !strings.Contains(rendered, "--- Comment 1 ---\nfix the typo") {
		t.Fatalf("the system's own block was not appended: %q", rendered)
	}
}

// Refusals an operator has to be able to act on: a text that is not a template at
// all, one that inserts something the key does not offer, an empty one, and one so
// long it cannot be an instruction. Each names the bound or the mistake.
func TestTextThatCouldNotBeUsedAsAnInstructionIsRefused(t *testing.T) {
	spec, _ := SpecFor(KeyReviewImplement)
	cases := []struct {
		name    string
		text    string
		mustSay string
	}{
		{"nothing at all", "   \n ", "must not be empty"},
		{"not a template", "Act on {{.Review}} and {{oops}}", "not a valid template"},
		{"an insert the key does not offer", "Act on {{.Review}} for {{.Branch}}", "Branch"},
		{"longer than the bound", "{{.Review}}" + strings.Repeat("x", MaxTextLength), "16384"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := spec.Validate(tc.text)
			if !errors.Is(err, ErrInvalidText) {
				t.Fatalf("Validate = %v, want an invalid-instruction refusal", err)
			}
			if !strings.Contains(err.Error(), tc.mustSay) {
				t.Fatalf("the refusal %q does not mention %q", err, tc.mustSay)
			}
		})
	}
}

// The hash is what makes "which instruction produced this run?" answerable after any
// later edit, so it has to follow the text and nothing else.
func TestTheContentHashFollowsTheTextAndNothingElse(t *testing.T) {
	if Hash("review this branch") == Hash("review this branch ") {
		t.Fatal("two different instructions hash the same")
	}
	if Hash("review this branch") != Hash("review this branch") {
		t.Fatal("the same instruction hashes differently")
	}
}
