// Package setting owns the tool's non-text configuration: the catalogue of settings
// that switch behaviour on or off, what each one ships as, and how a stored value is
// read back as the type it means.
//
// It is a catalogue, not a mechanism. Which scope wins, how a version is stored and
// how a shipped default is published are the scoped package's — the same rules the
// instructions the agent is given resolve through. That sharing is the point: an
// operator learns one inheritance rule, and a setting cannot start disagreeing with
// an instruction about which place covers which.
//
// A setting's stored form is text, because storage is one discipline for every kind
// of value. What that text means is decided here, once, so no consumer parses a
// stored value itself.
package setting

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"temporal-agents/internal/scoped"
)

// ErrUnknownKey reports a setting key this build does not govern. The catalogue is
// the closed set of keys, so a key outside it is a defect (a stale stored row, a
// typo in a caller), never a value to fall back on.
var ErrUnknownKey = errors.New("unknown setting key")

// ErrInvalidValue marks text refused as a setting's value. A refusal always names
// what was wrong and what is accepted, because an operator cannot act on "invalid".
var ErrInvalidValue = errors.New("invalid setting value")

// Key names one governed setting. It is an alias of the scoped key every kind of
// configured value shares, because settings and instructions live in one key space:
// a key is chosen once and can never mean two things.
type Key = scoped.Key

const (
	// KeySteeringEnabled decides whether a review round stops for the operator to
	// steer it instead of building straight on.
	KeySteeringEnabled Key = "steering.enabled"
)

// Spec is everything the tool knows about one setting: what it is for and what it
// ships as. Every setting is a switch today; a setting with a value that is not a
// switch gets a type of its own when one exists, rather than a string nobody parses
// the same way twice.
type Spec struct {
	// Key is the setting this describes.
	Key Key
	// Purpose is the one-line description an operator reads before changing it.
	Purpose string
	// Factory is what the setting is when nothing anywhere has set it.
	Factory bool
}

// specs is the catalogue: the closed set of settings this build governs.
var specs = []Spec{
	{
		Key: KeySteeringEnabled,
		Purpose: "Stop a review round for the operator to steer it, instead of building " +
			"on the review's findings straight away.",
		// Off by factory default: a run that waits for a human it was never promised
		// would look like a run that hung.
		Factory: false,
	},
}

// Specs lists every governed setting, in a stable order, for publication and for the
// surfaces that let an operator see what exists.
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

// Format renders a setting's value as the text storage keeps. It is the counterpart
// of Parse, and the only place a value becomes text, so a value written by one
// surface reads the same to every other.
func Format(enabled bool) string { return strconv.FormatBool(enabled) }

// Parse reads a stored value back as the type the key means, refusing anything else
// by name.
//
// It is deliberately strict: a value that is nearly a boolean ("yes", "1 ") is a
// value somebody misunderstood, and accepting it would make the setting's meaning
// depend on who wrote it. Saving goes through the same check, so a value that cannot
// be read back can never be stored in the first place.
func (s Spec) Parse(text string) (bool, error) {
	switch strings.TrimSpace(text) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s is either \"true\" or \"false\", not %q", ErrInvalidValue, s.Key, text)
	}
}

// Validate reports whether text may be saved as this setting's value.
func (s Spec) Validate(text string) error {
	_, err := s.Parse(text)
	return err
}
