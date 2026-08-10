// Package schema states what a bounded context's schema is at, and what the running
// binary requires of it.
//
// It carries no driver and no SQL on purpose. The processes that verify a schema at
// startup define the port they need at their own side, and that port has to be
// expressible without naming an adapter: a composition root that spoke of a Postgres
// type would tie every context's schema — and the decision to start or refuse — to
// one database. Substance-wise nothing here is Postgres-specific, so nothing here
// knows about it.
package schema

import "errors"

// ErrStale reports a database whose schema is older than the migrations the running
// binary carries. It is the failure a process verifies for at startup, so a stale
// schema stops a process before it takes work rather than at its first missing
// column.
var ErrStale = errors.New("the schema is out of date")

// State is what one schema is at, and what the running binary requires of it. It is a
// value, computed by an adapter, so the decision "migrate, refuse, or proceed" is
// taken by the caller rather than hidden inside a migration runner.
type State struct {
	// Namespace is the namespace the state was read for.
	Namespace string
	// Applied are the migrations recorded as applied for this namespace, by their
	// filename (the namespace prefix is stripped), in apply order.
	Applied []string
	// Required are the migrations the running binary carries, in apply order.
	Required []string
	// Missing are the required migrations that are not recorded as applied, in apply
	// order. It is empty exactly when the schema is at or ahead of what is required.
	Missing []string
}

// NoVersion is what a database that has never been migrated reports as its version.
// It is a word rather than an empty string so an operator reading the report is told
// "none" instead of nothing at all.
const NoVersion = "none"

// Version is the newest migration recorded for this namespace, or "none" when the
// namespace has never been migrated. A database migrated by a newer binary reports
// that newer version, which is why this reads the record rather than the embedded
// files.
func (s State) Version() string {
	if len(s.Applied) == 0 {
		return NoVersion
	}
	return s.Applied[len(s.Applied)-1]
}

// RequiredVersion is the newest migration the running binary carries, or "none" when
// it carries none.
func (s State) RequiredVersion() string {
	if len(s.Required) == 0 {
		return NoVersion
	}
	return s.Required[len(s.Required)-1]
}

// UpToDate reports whether every required migration is recorded as applied. A
// database that is ahead (migrated by a newer binary) is up to date: this binary can
// read everything it knows about.
func (s State) UpToDate() bool { return len(s.Missing) == 0 }
