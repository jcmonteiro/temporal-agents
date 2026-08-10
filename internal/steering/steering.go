// Package steering owns the human-in-the-loop pause of a review round: the
// decision an operator may make, the guidance text a decision may carry, and the
// durable session that waits for it.
//
// A review loop runs on its own until it converges. Where an operator has asked for
// it, the loop stops after a review has produced material and before the agent acts
// on it, and this package is what it stops in: a session of its own, signalled to
// proceed, that outlives a worker restart and waits as long as a human takes.
//
// The split follows the rest of the tool. The vocabulary and the pure composition
// of the guidance block are here and depend on nothing; the session's orchestration
// is here too, because it *is* an orchestrated unit rather than a helper over one;
// and everything durable is reached through the ports the rest of the tool already
// owns (execstore for the session's own execution row). No SQL and no HTTP reaches
// this package.
package steering

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrInvalidDecision marks a decision refused as it stands: an unknown choice, a
// claim of guidance with no guidance, or guidance past the bound. A refusal always
// names what is wrong, because an operator cannot act on "invalid".
var ErrInvalidDecision = errors.New("invalid steering decision")

// Choice is what the operator decided to do with the round that is waiting. It is a
// closed set of exactly three: guide the pass, let it proceed unguided, or stop the
// loop.
type Choice string

const (
	// ChoiceGuide proceeds with the operator's guidance in hand.
	ChoiceGuide Choice = "guide"
	// ChoiceSkip proceeds without guidance. It is how an operator says "carry on",
	// so empty guidance never has to mean it.
	ChoiceSkip Choice = "skip"
	// ChoiceStop ends the loop deliberately, leaving the work needing a human.
	ChoiceStop Choice = "stop"
)

// Choices lists every decision, in the order an interface offers them.
func Choices() []Choice { return []Choice{ChoiceGuide, ChoiceSkip, ChoiceStop} }

// ValidChoice reports whether choice is one of the three, so a decision arriving
// from anywhere is refused by name rather than silently treated as one of them.
func ValidChoice(choice Choice) bool {
	for _, known := range Choices() {
		if known == choice {
			return true
		}
	}
	return false
}

// MaxGuidanceLength bounds one guidance text, in bytes. The block is added to a
// prompt the agent is charged for, so it needs a bound; it is generous next to
// anything a human types into a decision, so reaching it means the text is not
// guidance. Text over the bound is refused with the bound named, never truncated:
// truncation would silently drop the operator's most recent sentences, which are
// the ones they cared enough to add last.
const MaxGuidanceLength = 8 << 10 // 8 KiB

// Decision is what an operator sent: the choice, the guidance it carries, and who
// made it. It is the session's whole artifact — the conversation that may have
// produced the text never travels with it.
type Decision struct {
	// Choice is what to do with the waiting round.
	Choice Choice
	// Guidance is the text handed to the agent. It is present only with ChoiceGuide.
	Guidance string
	// Principal is who decided, recorded for audit. Any signed-in operator may
	// answer, so this says who did, never who was allowed to.
	Principal string
}

// Made reports whether this is a decision at all, i.e. whether the session has been
// answered. The zero value is "not yet".
func (d Decision) Made() bool { return d.Choice != "" }

// Validate reports whether the decision may be acted on.
//
// Guidance is mandatory when guiding: an empty guidance block is indistinguishable
// from a mistake, and "proceed without guidance" is the decision that means it. Only
// guiding may carry text, so a skip or a stop cannot smuggle a prompt past the
// operator's own choice.
func (d Decision) Validate() error {
	if !ValidChoice(d.Choice) {
		return fmt.Errorf("%w: %q is not one of %v", ErrInvalidDecision, d.Choice, Choices())
	}
	guidance := strings.TrimSpace(d.Guidance)
	if d.Choice != ChoiceGuide {
		if guidance != "" {
			return fmt.Errorf("%w: %s carries no guidance, use %s to send text",
				ErrInvalidDecision, d.Choice, ChoiceGuide)
		}
		return nil
	}
	if guidance == "" {
		return fmt.Errorf("%w: %s needs guidance; use %s to proceed without it",
			ErrInvalidDecision, ChoiceGuide, ChoiceSkip)
	}
	if !utf8.ValidString(d.Guidance) {
		return fmt.Errorf("%w: the guidance is not valid text", ErrInvalidDecision)
	}
	if len(d.Guidance) > MaxGuidanceLength {
		return fmt.Errorf("%w: the guidance is %d bytes and the limit is %d",
			ErrInvalidDecision, len(d.Guidance), MaxGuidanceLength)
	}
	return nil
}

// Proceeds reports whether the pass the session paused goes ahead.
func (d Decision) Proceeds() bool { return d.Choice == ChoiceGuide || d.Choice == ChoiceSkip }

// The fences around the guidance block. They are the same shape as the fences the
// pilot pass already puts around a review comment, so the agent reads one document
// in one convention rather than two.
const (
	guidanceOpen  = "--- Operator guidance ---"
	guidanceClose = "--- End of operator guidance ---"
)

// Block renders guidance as the fenced block the agent is handed, and renders
// nothing at all when there is no guidance.
//
// It is deliberately additive: it introduces a section of its own instead of
// editing one, so an instruction and the material it applies to reach the agent
// exactly as they did before steering existed.
func Block(guidance string) string {
	trimmed := strings.TrimSpace(guidance)
	if trimmed == "" {
		return ""
	}
	return guidanceOpen + "\n" + trimmed + "\n" + guidanceClose
}

// WithGuidance puts the guidance block immediately before the material it applies
// to, and returns the material untouched when there is no guidance.
//
// Prefixing the *material* is what places the block after the instruction and
// before the material wherever the instruction inserts it: an operator who moved
// the insert moved the guidance with it, and the guidance can never end up
// separated from what it is about.
func WithGuidance(guidance, material string) string {
	block := Block(guidance)
	if block == "" {
		return material
	}
	return block + "\n" + material
}
