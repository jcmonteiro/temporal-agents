package setting

import (
	"context"
	"fmt"

	"temporal-agents/internal/scoped"
)

// The vocabulary a setting shares with every other configured value is the scoped
// package's, re-exported here so a caller that only deals in settings names one
// package. Aliases, not copies: they are the same types, so a value crosses between
// the two without conversion and no second definition can drift from the first.
type (
	// Scope is where a stored setting was set.
	Scope = scoped.Scope
	// Reader is the driven port resolution reads through.
	Reader = scoped.Reader
	// Publisher is the driven port the shipped defaults are published through.
	Publisher = scoped.Publisher
)

const (
	// GlobalScope is the installation-wide value.
	GlobalScope = scoped.GlobalScope
	// FactoryScope is the shipped default as published into storage.
	FactoryScope = scoped.FactoryScope
)

var (
	// ErrNotConfigured reports resolution asked of a process wired without a store.
	ErrNotConfigured = scoped.ErrNotConfigured
	// DirectoryScope is the scope of one directory place.
	DirectoryScope = scoped.DirectoryScope
	// Chain is the order a value is resolved in for work that runs in a place.
	Chain = scoped.Chain
)

// Value is one resolved setting: what it is here, and where that answer came from.
//
// The source travels with the value because "off, because the installation says so"
// and "off, because nobody has ever set it" are different facts to an operator about
// to change one — and because an interface must never re-derive inheritance to show
// which is which.
type Value struct {
	// Key is the setting.
	Key Key
	// Enabled is the setting's effective value.
	Enabled bool
	// Scope is where the value that won was set.
	Scope Scope
	// Version is which version of that (key, scope) it is, or 0 when the value is the
	// one the build ships and storage holds none.
	Version int
	// Hash is the content hash of the stored text.
	Hash string
}

// Inherited reports whether this place is using somebody else's answer: the
// installation's, or the shipped default. It is the question an interface asks
// before showing "inherited from …".
func (v Value) Inherited(directory string) bool {
	return v.Scope != DirectoryScope(directory) || directory == ""
}

// Resolution is what one caller resolved: one Value per key it asked for, in the
// order the keys were asked for.
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

// Enabled reports whether key is on: what was resolved, or the shipped default when
// this resolution carries none (a caller that asked for other keys, or work that
// predates the setting).
func (r Resolution) Enabled(key Key) bool {
	if value, ok := r.Value(key); ok {
		return value.Enabled
	}
	if spec, ok := SpecFor(key); ok {
		return spec.Factory
	}
	return false
}

// PublishDefaults publishes every shipped setting into storage, so an upgrade that
// changes one reaches every place that has not overridden it.
func PublishDefaults(ctx context.Context, publisher Publisher) error {
	if publisher == nil {
		return ErrNotConfigured
	}
	for _, spec := range Specs() {
		if err := scoped.PublishDefault(ctx, publisher, spec.Key, Format(spec.Factory)); err != nil {
			return err
		}
	}
	return nil
}

// Request is what a caller asks to have resolved: which settings, and the scope
// chain of the place they are being resolved for (see Chain). No keys means every
// governed setting, which is what a configuration surface asks for.
type Request struct {
	Keys   []Key
	Scopes []Scope
}

// Resolver answers what the settings are for a place. It is the application core of
// the setting catalogue: the same object serves the activity a workflow schedules
// and the read the API answers, so neither can resolve differently from the other.
type Resolver struct {
	// Store is the driven adapter. A nil store makes resolution fail rather than
	// answer from the build's defaults: a silent substitution changes what the tool
	// does with nothing to say it happened.
	Store Reader
}

// Resolve answers what each requested setting is, and where the answer came from.
func (r *Resolver) Resolve(ctx context.Context, req Request) (Resolution, error) {
	if r.Store == nil {
		return nil, ErrNotConfigured
	}
	keys := req.Keys
	if len(keys) == 0 {
		keys = Keys()
	}
	specs := make([]Spec, 0, len(keys))
	for _, key := range keys {
		spec, ok := SpecFor(key)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		specs = append(specs, spec)
	}
	scopes := req.Scopes
	if len(scopes) == 0 {
		// A caller with no place still resolves, through the installation's values.
		scopes = Chain("", "")
	}
	records, err := r.Store.Current(ctx, keys, scopes)
	if err != nil {
		return nil, fmt.Errorf("read the stored settings: %w", err)
	}
	resolution := make(Resolution, 0, len(specs))
	for _, spec := range specs {
		value, err := resolve(spec, scopes, records)
		if err != nil {
			return nil, err
		}
		resolution = append(resolution, value)
	}
	return resolution, nil
}

// resolve answers one key: the chain decides which stored value wins, and the value
// the build ships is the last resort.
//
// A stored value that cannot be read back as the type the key means fails the whole
// resolution. Treating it as the default instead would let one bad row change what
// the tool does while every surface kept reporting the default as if it were chosen.
func resolve(spec Spec, scopes []Scope, records []scoped.Record) (Value, error) {
	record, ok := scoped.Winner(records, spec.Key, scopes)
	if !ok {
		// Nothing is stored for this key anywhere in the chain, not even the published
		// factory row — a database whose defaults have not been published yet. The
		// build's own default answers, as version 0, which is the version no stored row
		// can have.
		return Value{
			Key:     spec.Key,
			Enabled: spec.Factory,
			Scope:   FactoryScope,
			Hash:    scoped.Hash(Format(spec.Factory)),
		}, nil
	}
	enabled, err := spec.Parse(record.Text)
	if err != nil {
		return Value{}, fmt.Errorf("the value stored for %s at %s v%d cannot be read: %w",
			spec.Key, record.Scope, record.Version, err)
	}
	return Value{
		Key:     spec.Key,
		Enabled: enabled,
		Scope:   record.Scope,
		Version: record.Version,
		Hash:    record.Hash,
	}, nil
}

// Activity drives resolution as a Temporal activity, for the workflows that need to
// know whether a behaviour is switched on where they run. It is registered once per
// worker, exactly as the location probe and the instruction resolution are.
type Activity struct {
	Resolver
}

// ResolveSettings answers what the requested settings are. It carries a name of its
// own because a worker registers one activity per method name, and resolving a
// setting is not resolving an instruction.
func (a *Activity) ResolveSettings(ctx context.Context, req Request) (Resolution, error) {
	return a.Resolve(ctx, req)
}
