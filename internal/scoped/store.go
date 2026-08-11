package scoped

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotConfigured is returned when resolution is asked of a process that was wired
// without a store. Resolution then fails, loudly, instead of quietly substituting a
// default: a silent substitution changes what the tool does with no record that it
// happened, which is the one failure stored configuration exists to prevent.
var ErrNotConfigured = errors.New("scoped value store is not configured (is DATABASE_URL set?)")

// ErrNoSuchVersion is returned when a recorded provenance names a version the store
// does not hold. It is a sentinel of its own so a caller can tell "that version was
// never stored" apart from a store outage, and report each honestly.
var ErrNoSuchVersion = errors.New("no such stored version")

// Record is one stored version as the port reports it: the text a (key, scope)
// currently points at, and which version that is.
//
// The text is opaque here. A catalogue decides what it means — an instruction, a
// boolean setting — so the storage discipline stays one thing however many kinds of
// value are configured.
type Record struct {
	// Key is the configured value.
	Key Key
	// Scope is where the value was set.
	Scope Scope
	// Version is which version of that (key, scope) this is. Versions are
	// append-only and start at 1, so a version number, once recorded, always names
	// the same text.
	Version int
	// Text is the stored value.
	Text string
	// Hash is the content hash of Text (see Hash).
	Hash string
	// SavedBy identifies the principal that created this version. It is empty for a
	// shipped default and on an explicitly unauthenticated local deployment.
	SavedBy string
}

// Reader is the driven port resolution reads through. An adapter answers only what
// it stores: which version each (key, scope) currently points at, and what one named
// version says. Deciding which of them wins is the rule below, stated once, never in
// SQL.
type Reader interface {
	// Current returns the pointed-at version of every (key, scope) pair that has one.
	// A pair with no pointer is simply absent; that is a gap in the chain, not an
	// error.
	Current(ctx context.Context, keys []Key, scopes []Scope) ([]Record, error)
	// Version returns one stored version, reporting ErrNoSuchVersion when there is
	// none. It is what turns an execution's recorded provenance — key, scope, version
	// — back into the value that produced it, which is why the text is never copied
	// into an execution's own row.
	Version(ctx context.Context, key Key, scope Scope, version int) (Record, error)
}

// Publisher is the driven port the shipped defaults are published through at
// startup.
type Publisher interface {
	// PublishFactory records text as the factory value of key, appending a version
	// only when the shipped text has actually changed. It must be idempotent and safe
	// to run concurrently: two processes starting together both call it.
	PublishFactory(ctx context.Context, key Key, text string) (Record, error)
}

// Writer is the driven port an override is saved through. Saving is append-only by
// contract: the adapter adds a version and moves the scope's pointer to it, so
// nothing a finished run referenced is ever rewritten.
//
// What may be saved is the catalogue's business, not the store's: an instruction is
// validated as a template, a setting as the type its key means. An adapter stores
// text.
type Writer interface {
	// Set appends a version of key at scope and points that scope at it, returning
	// the stored record. savedBy is the principal responsible for the version.
	Set(ctx context.Context, key Key, scope Scope, text, savedBy string) (Record, error)
	// Reset removes the pointer for key at scope. Versions remain append-only and
	// recoverable; resolution therefore returns to the next scope in the chain.
	Reset(ctx context.Context, key Key, scope Scope) error
}

// Store is every half, for the composition root that owns one adapter.
type Store interface {
	Reader
	Writer
	Publisher
}

// Winner is the resolution rule itself: the first scope of the chain that has a
// stored value for the key wins.
//
// It is a free function over values, so the rule is unit testable without a store,
// and so no adapter can express a different one by ordering its rows. What happens
// when nothing is stored anywhere is the catalogue's business: it holds the value
// the build ships.
func Winner(records []Record, key Key, scopes []Scope) (Record, bool) {
	for _, scope := range scopes {
		for _, record := range records {
			if record.Key == key && record.Scope == scope {
				return record, true
			}
		}
	}
	return Record{}, false
}

// PublishDefault publishes one shipped default, so an upgrade that changes it
// reaches every place that has not overridden it, and "return to the shipped
// default" means the default this build carries.
func PublishDefault(ctx context.Context, publisher Publisher, key Key, text string) error {
	if publisher == nil {
		return ErrNotConfigured
	}
	if _, err := publisher.PublishFactory(ctx, key, text); err != nil {
		return fmt.Errorf("publish the shipped value for %s: %w", key, err)
	}
	return nil
}
