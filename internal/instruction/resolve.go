package instruction

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotConfigured is returned when resolution is asked of a process that was wired
// without a store. Resolution then fails, loudly, instead of quietly substituting a
// default: a silent substitution changes what the agent was told with no record that
// it happened, which is the one failure this feature exists to prevent.
var ErrNotConfigured = errors.New("instruction store is not configured (is DATABASE_URL set?)")

// Record is one stored instruction version as the port reports it: the text a
// (key, scope) currently points at, and which version that is.
type Record struct {
	// Key is the governed instruction.
	Key Key
	// Scope is where the value was set.
	Scope Scope
	// Version is which version of that (key, scope) this is. Versions are
	// append-only and start at 1, so a version number, once recorded, always names
	// the same text.
	Version int
	// Text is the instruction itself.
	Text string
	// Hash is the content hash of Text (see Hash).
	Hash string
}

// Value is one resolved instruction: the text a unit of work will use, and the
// provenance of where it came from.
//
// It travels with the unit of work — in the workflow's input, across
// continue-as-new — so every pass of a loop uses what the first pass resolved. An
// instruction edited while a loop runs therefore cannot change what an already
// recorded pass did.
type Value struct {
	Key     Key
	Text    string
	Scope   Scope
	Version int
	Hash    string
}

// Resolution is what one unit of work resolved: one Value per key it asked for, in
// the order the keys were asked for. It is a slice rather than a map so its encoding
// is stable, which matters for a value carried in a workflow input.
type Resolution []Value

// Value resolves one key of the resolution.
func (r Resolution) Value(key Key) (Value, bool) {
	for _, value := range r {
		if value.Key == key {
			return value, true
		}
	}
	return Value{}, false
}

// Text is the instruction to use for key: what was resolved, or the shipped default
// when this resolution carries none.
//
// The fallback is for executions that predate stored instructions (their input
// carries no resolution at all) and for a caller that asks for a key it never
// resolved. It is not a fallback for a failed resolution: that fails the unit of
// work before any agent runs.
func (r Resolution) Text(key Key) string {
	if value, ok := r.Value(key); ok {
		return value.Text
	}
	if spec, ok := SpecFor(key); ok {
		return spec.Factory
	}
	return ""
}

// Render builds the prompt for key: the resolved instruction (or the shipped
// default, see Text) rendered from data, with the system's own block appended.
func Render(resolution Resolution, key Key, data Data) (string, error) {
	spec, ok := SpecFor(key)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	return spec.Render(resolution.Text(key), data)
}

// Reader is the driven port resolution reads through. An adapter answers only what
// it stores: which version each (key, scope) currently points at. Deciding which of
// them wins is the core's rule, stated once in resolve, never in SQL.
type Reader interface {
	// Current returns the pointed-at version of every (key, scope) pair that has one.
	// A pair with no pointer is simply absent; that is a gap in the chain, not an
	// error.
	Current(ctx context.Context, keys []Key, scopes []Scope) ([]Record, error)
}

// Publisher is the driven port the shipped defaults are published through at
// startup.
type Publisher interface {
	// PublishFactory records text as the factory value of key, appending a version
	// only when the shipped text has actually changed. It must be idempotent and safe
	// to run concurrently: two processes starting together both call it.
	PublishFactory(ctx context.Context, key Key, text string) (Record, error)
}

// Store is both halves, for the composition root that owns one adapter.
type Store interface {
	Reader
	Publisher
}

// PublishDefaults publishes every shipped default into storage, so an upgrade that
// improves an instruction reaches every place that has not overridden it, and
// "return to the shipped default" means the default this build carries.
//
// A default that does not satisfy its own key's rules is a build defect, so it is
// refused here rather than published: the check costs nothing at startup and turns a
// broken default into a failure to start rather than into agent behaviour.
func PublishDefaults(ctx context.Context, publisher Publisher) error {
	if publisher == nil {
		return ErrNotConfigured
	}
	for _, spec := range Specs() {
		if err := spec.Validate(spec.Factory); err != nil {
			return fmt.Errorf("the shipped default for %s is not usable: %w", spec.Key, err)
		}
		if _, err := publisher.PublishFactory(ctx, spec.Key, spec.Factory); err != nil {
			return fmt.Errorf("publish the shipped default for %s: %w", spec.Key, err)
		}
	}
	return nil
}

// Request is what a unit of work asks to have resolved: which instructions, and the
// scope chain of the place it runs in (see Chain).
type Request struct {
	Keys   []Key
	Scopes []Scope
}

// Activity drives the store as a Temporal activity, exactly as the location probe
// does: one implementation registered once per worker, referenced by every workflow
// that needs an instruction. The workflow-side helper that schedules it lives in
// wfinstruction, so this package stays SDK-free.
type Activity struct {
	// Store is the driven adapter. A nil store makes resolution fail rather than
	// answer from the build's defaults, because an unrecorded substitution is
	// precisely what must not happen.
	Store Reader
}

// Resolve answers which instruction each requested key resolves to.
func (a *Activity) Resolve(ctx context.Context, req Request) (Resolution, error) {
	if a.Store == nil {
		return nil, ErrNotConfigured
	}
	specs := make([]Spec, 0, len(req.Keys))
	for _, key := range req.Keys {
		spec, ok := SpecFor(key)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		specs = append(specs, spec)
	}
	scopes := req.Scopes
	if len(scopes) == 0 {
		// Work whose place could not be established still resolves, through the
		// installation's value.
		scopes = Chain("", "")
	}
	records, err := a.Store.Current(ctx, req.Keys, scopes)
	if err != nil {
		return nil, fmt.Errorf("read the stored instructions: %w", err)
	}
	resolution := make(Resolution, 0, len(specs))
	for _, spec := range specs {
		resolution = append(resolution, resolve(spec, scopes, records))
	}
	return resolution, nil
}

// resolve is the resolution rule itself: the first scope of the chain that has a
// stored value for the key wins, and the value the build ships is the last resort.
//
// It is a free function over values so the rule is unit testable without a store,
// and so no adapter can express a different one by ordering its rows.
func resolve(spec Spec, scopes []Scope, records []Record) Value {
	for _, scope := range scopes {
		for _, record := range records {
			if record.Key != spec.Key || record.Scope != scope {
				continue
			}
			return Value{
				Key:     spec.Key,
				Text:    record.Text,
				Scope:   record.Scope,
				Version: record.Version,
				Hash:    record.Hash,
			}
		}
	}
	// Nothing is stored for this key anywhere in the chain, not even the published
	// factory row. That is what a database whose defaults have not been published yet
	// looks like, so the build's own default answers — recorded as version 0, which
	// is the version no stored row can have.
	return Value{
		Key:     spec.Key,
		Text:    spec.Factory,
		Scope:   FactoryScope,
		Version: 0,
		Hash:    Hash(spec.Factory),
	}
}
