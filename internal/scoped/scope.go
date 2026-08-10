// Package scoped owns the mechanism the tool's per-place configuration resolves
// through: the chain of scopes a value is looked for in, the append-only versioned
// storage it lives in, and the port that storage is reached through.
//
// It is the mechanism and nothing else. What values exist, what they mean, and what
// each one ships as belong to the catalogues built on top of it — the instructions
// the agent is given (see the instruction package) and the settings that switch
// behaviour on (see the setting package). One mechanism serves both on purpose: two
// copies of "which scope wins" would be two rules an operator has to learn, and
// they would disagree the first time one of them changed.
//
// Nothing here knows SQL or Temporal. The driven adapter lives in the nested
// scopedpg package, and the workflow-side helper that schedules resolution lives in
// wfinstruction.
package scoped

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Key names one configured value. Keys are stable and stored, and every catalogue
// shares one key space, so a key is chosen once and can never mean two things.
type Key string

// A scope is where a value was set. Scopes form the chain a value is resolved
// through: the most specific place first, then the places it belongs to, then the
// installation, and finally what the build ships.
//
// A place is a scope of its own rather than a group of its own: a second grouping
// concept beside places could disagree with places, and the disagreement would be
// invisible. Only relations the location probe established make a chain — a path
// containing another path never does, because git puts a worktree outside its
// repository by default, so containment invents wrong parents and misses right ones.
type Scope string

const (
	// GlobalScope is the installation-wide value: what every place inherits unless it
	// overrode the key itself.
	GlobalScope Scope = "global"
	// FactoryScope is the shipped default as published into storage at startup. It is
	// the last scope in every chain, and the one "return to the shipped default"
	// resolves to.
	FactoryScope Scope = "factory"
)

// directoryScopePrefix namespaces a directory place's scope, so a place can never
// collide with the two fixed scopes above whatever it is called on disk.
const directoryScopePrefix = "directory:"

// DirectoryScope is the scope of one directory place: a working tree, or the
// repository a working tree belongs to.
func DirectoryScope(directory string) Scope {
	if directory == "" {
		return ""
	}
	return Scope(directoryScopePrefix + directory)
}

// Kind reports which sort of scope this is, without exposing what it names. It is
// what a listing prints: a scope carries an absolute path, and a provenance line is
// read for "where was this set", not for the server's directory layout.
func (s Scope) Kind() string {
	switch {
	case s == GlobalScope:
		return "global"
	case s == FactoryScope:
		return "factory"
	case strings.HasPrefix(string(s), directoryScopePrefix):
		return "directory"
	default:
		return "unknown"
	}
}

// Chain is the order a value is resolved in for work that runs in directory, inside
// repository (empty when the two do not differ, i.e. the work runs in an ordinary
// checkout rather than in a linked worktree).
//
// The chain is built from probed facts only, and every chain ends the same way:
// global, then factory. Work whose place could not be established still resolves —
// through the installation's value — rather than failing or guessing a place.
func Chain(directory, repository string) []Scope {
	chain := make([]Scope, 0, 4)
	if directory != "" {
		chain = append(chain, DirectoryScope(directory))
	}
	if repository != "" && repository != directory {
		chain = append(chain, DirectoryScope(repository))
	}
	return append(chain, GlobalScope, FactoryScope)
}

// Hash is the content hash recorded beside a resolved value, so "which value
// produced this run?" can be answered even if the version it names were ever
// doubted. It is the full SHA-256 of the stored text, hex-encoded: a short digest
// invites collisions in a record that is kept forever.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
