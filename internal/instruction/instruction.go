// Package instruction owns the instructions the agent is given: the catalogue of
// the keys this tool governs, the default each one ships with, the rule that
// decides which stored value a unit of work uses, and the port that storage is
// reached through.
//
// An instruction is behaviour, not text an operator happens to type: it decides how
// the agent reviews, and how it acts on a review. So it is modelled here rather than
// spelled out at each call site — one place says which instructions exist, what may
// be inserted into each, which part of each is the system's own, and how long one may
// be.
//
// It is a catalogue, not a mechanism: which scope wins, how a version is stored and
// how the shipped default is published are the scoped package's, shared with every
// other kind of configured value. This package says what instructions exist and what
// each one may contain.
//
// It holds no SQL and no Temporal: the driven adapter lives in scoped/scopedpg, and
// the workflow-side helper that schedules the resolve activity lives in
// wfinstruction — the same split the place and execstore packages keep.
package instruction

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"unicode/utf8"

	"temporal-agents/internal/scoped"
)

// ErrUnknownKey reports an instruction key this build does not govern. The
// catalogue is the closed set of keys, so a key outside it is a defect (a stale
// stored row, a typo in a caller), never a value to fall back on.
var ErrUnknownKey = errors.New("unknown instruction key")

// ErrInvalidText marks text refused as an instruction: empty, too long, not a
// template, or missing material the key must carry. A refusal always names what is
// wrong, because an operator cannot act on "invalid".
var ErrInvalidText = errors.New("invalid instruction")

// Key names one governed instruction. It is an alias of the scoped key every kind
// of configured value shares, because instructions and settings live in one key
// space: a key is chosen once and can never mean two things.
type Key = scoped.Key

const (
	// KeyReviewPerform is what the agent is told when it reviews a branch.
	KeyReviewPerform Key = "review.perform"
	// KeyReviewImplement is what the agent is told when it acts on a review's raw
	// output.
	KeyReviewImplement Key = "review.implement"
	// KeyPilotAddress is what the agent is told when it addresses the unresolved
	// review comments on a pull request.
	KeyPilotAddress Key = "pilot.address"
	// KeySteeringQuestion is what the read-only questioning agent is told when it
	// helps an operator turn context into guidance for one review round.
	KeySteeringQuestion Key = "steering.question"
)

// MaxTextLength bounds one instruction, in bytes. The bound exists so a paste
// accident cannot become a prompt the agent is charged for on every pass; it is
// generous next to any instruction a human writes, so reaching it means the text is
// not an instruction. Text over the bound is refused with the bound named, never
// truncated: a silently shortened instruction changes agent behaviour without
// record.
const MaxTextLength = 16 << 10 // 16 KiB

// Insert is a value the system supplies to an instruction when it renders it,
// written as a Go template action ("{{.Review}}").
//
// Declaring inserts per key is what makes an override safe to save: a required
// insert that an override drops would run the agent with nothing to act on, which
// looks like a plausible run and is expensive to diagnose. Saving is therefore
// checked against this declaration (see Spec.Validate).
type Insert struct {
	// Name is the field name used in the template action.
	Name string
	// Purpose says what the system puts there, for the operator editing the text.
	Purpose string
	// Required reports whether an override must render this insert. An insert that
	// only the system's own block uses is not required of the operator.
	Required bool
}

// Action renders the insert as it is written in an instruction.
func (i Insert) Action() string { return "{{." + i.Name + "}}" }

// Spec is everything the tool knows about one governed instruction: what it is for,
// what it ships as, what may be inserted into it, and which part of it the system
// keeps for itself.
type Spec struct {
	// Key is the instruction this describes.
	Key Key
	// Purpose is the one-line description an operator reads before editing it.
	Purpose string
	// Factory is the shipped default, and stays the source of truth in code so an
	// upgrade improves every place that has not overridden it (it is published into
	// storage at startup; see PublishDefaults).
	Factory string
	// Inserts are the values the system supplies when the instruction renders.
	Inserts []Insert
	// System is the block the system always appends after the operator's text. It is
	// not overridable: it carries material whose shape the code produces (and, later,
	// parses), and no operator should be asked to maintain a machine contract by
	// hand. It is empty for a key that has none.
	System string
	// Advanced marks instructions whose protected material makes an unsafe edit more
	// costly. A configuration surface must put an explicit warning in front of them.
	Advanced bool
}

// Data is the material an instruction renders from, keyed by insert name.
type Data map[string]string

// The shipped defaults. They are constants in the build, so "return to the shipped
// default" means the one this build carries, and an upgrade that improves an
// instruction reaches every place that never overrode it.
const (
	// factoryReviewPerform is deliberately terse: the agent decides how to review.
	factoryReviewPerform = "Perform a thorough code review of the current branch"

	// factoryReviewImplement asks the agent to commit its work, so the loop's
	// HEAD-advanced check can confirm the change landed, and to commit nothing when
	// there is nothing to change, so the loop can recognize convergence. The review
	// it must act on is inserted where the instruction says, and an override that
	// drops the insert is refused.
	factoryReviewImplement = `Implement the actionable changes called for by the code review below. Read the referenced code for context and make the changes. Confirm lint/typecheck/build (and synth, if infra) pass, then commit all your work. If nothing in the review requires a code change, do not commit anything.

{{.Review}}`

	// factoryPilotAddress governs how the agent addresses review comments. The
	// comments themselves are not part of it: they are rendered by the code, in a
	// shape the code produces, and appended as the system's own block.
	factoryPilotAddress = `- For each comment below, read the referenced code for context, then fix it. Read the code and relevant in-repo documentation to decide on the solution.
- Confirm lint/typecheck/build (and synth, if infra) pass first.
- Commit all the fixes.
- Summarize your work once you are done explaining WHAT changed (not HOW)`

	// systemPilotAddress appends the pull request's description and its unresolved
	// comment threads, rendered by the code that read them.
	systemPilotAddress = "\n{{.Comments}}"

	factorySteeringQuestion = `Help the operator turn their context into concise guidance for an implementing agent.
Ask exactly one focused question in each response. Do not propose or make repository changes.
When asked to finish, return only a self-contained guidance draft, not a question or an explanation.`

	// The material is system-owned so an override cannot make the questioning agent
	// discuss a different round or omit what the operator is deciding about.
	systemSteeringQuestion = "\n\nReview material:\n{{.Material}}"
)

// specs is the catalogue: the closed set of instructions this build governs.
var specs = []Spec{
	{
		Key:     KeyReviewPerform,
		Purpose: "How the agent reviews the current branch.",
		Factory: factoryReviewPerform,
	},
	{
		Key:     KeyReviewImplement,
		Purpose: "How the agent acts on a review's findings.",
		Factory: factoryReviewImplement,
		Inserts: []Insert{{
			Name:     "Review",
			Purpose:  "The previous pass's raw review output.",
			Required: true,
		}},
	},
	{
		Key:     KeyPilotAddress,
		Purpose: "How the agent addresses the unresolved review comments on a pull request.",
		Factory: factoryPilotAddress,
		Inserts: []Insert{{
			Name:    "Comments",
			Purpose: "The pull request's description and its unresolved comment threads.",
		}},
		System:   systemPilotAddress,
		Advanced: true,
	},
	{
		Key:     KeySteeringQuestion,
		Purpose: "How the read-only agent questions an operator into guidance for one review round.",
		Factory: factorySteeringQuestion,
		Inserts: []Insert{{
			Name:    "Material",
			Purpose: "The review material the operator is deciding about.",
		}},
		System:   systemSteeringQuestion,
		Advanced: true,
	},
}

// Specs lists every governed instruction, in a stable order, for publication and
// for the surfaces that let an operator see what exists.
func Specs() []Spec {
	listed := make([]Spec, len(specs))
	copy(listed, specs)
	return listed
}

// Keys lists the governed keys in the same stable order.
func Keys() []Key {
	keys := make([]Key, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	return keys
}

// SpecFor resolves one key's spec, reporting whether this build governs it.
func SpecFor(key Key) (Spec, bool) {
	for _, spec := range specs {
		if spec.Key == key {
			return spec, true
		}
	}
	return Spec{}, false
}

// Insert resolves one declared insert of the spec by name.
func (s Spec) Insert(name string) (Insert, bool) {
	for _, insert := range s.Inserts {
		if insert.Name == name {
			return insert, true
		}
	}
	return Insert{}, false
}

// Validate reports whether text may be saved as this instruction: it must be
// present, within the bound, a template that parses, use no insert the key does not
// declare, and render every insert the key requires.
//
// It is the same check wherever an override arrives from, so the answer cannot
// depend on which surface asked.
func (s Spec) Validate(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: the %s instruction must not be empty", ErrInvalidText, s.Key)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: the %s instruction is not valid text", ErrInvalidText, s.Key)
	}
	if len(text) > MaxTextLength {
		return fmt.Errorf("%w: the %s instruction is %d bytes and the limit is %d",
			ErrInvalidText, s.Key, len(text), MaxTextLength)
	}
	// Render the editable text without the system block. This answers three questions
	// at once: an undeclared insert fails, a required insert leaves its sentinel in
	// the output, and a system-owned insert in editable text can be refused instead
	// of being duplicated before the read-only block.
	rendered, err := s.renderPart(text, s.sentinels())
	if err != nil {
		return err
	}
	for _, insert := range s.Inserts {
		present := strings.Contains(rendered, sentinel(insert.Name))
		if insert.Required && !present {
			return fmt.Errorf("%w: the %s instruction must insert %s (%s)",
				ErrInvalidText, s.Key, insert.Action(), insert.Purpose)
		}
		if !insert.Required && strings.Contains(s.System, insert.Action()) && present {
			return fmt.Errorf("%w: %s is supplied by the read-only system block for %s",
				ErrInvalidText, insert.Action(), s.Key)
		}
	}
	return nil
}

// Render turns text into the prompt the agent is handed: the instruction rendered
// from data, followed by the system's own block rendered from the same data.
func (s Spec) Render(text string, data Data) (string, error) {
	return s.render(text, data)
}

// render is the one rendering path, so validation and production cannot disagree
// about what an instruction becomes.
func (s Spec) render(text string, data Data) (string, error) {
	return s.renderPart(text+s.System, data)
}

func (s Spec) renderPart(text string, data Data) (string, error) {
	if data == nil {
		data = Data{}
	}
	tmpl, err := template.New(string(s.Key)).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("%w: the %s instruction is not a valid template: %w", ErrInvalidText, s.Key, err)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("%w: the %s instruction could not be rendered: %w", ErrInvalidText, s.Key, err)
	}
	return out.String(), nil
}

// sentinels are the values Validate renders with: one recognisable string per
// declared insert, so what the text did with each is readable off the output.
func (s Spec) sentinels() Data {
	data := make(Data, len(s.Inserts))
	for _, insert := range s.Inserts {
		data[insert.Name] = sentinel(insert.Name)
	}
	return data
}

// sentinel is the stand-in one insert renders as while validating. It carries
// characters no instruction would contain, so a text that happens to quote an
// insert's name cannot be mistaken for one that renders it.
func sentinel(name string) string { return "\x00insert:" + name + "\x00" }
