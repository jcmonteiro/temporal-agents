package instruction

import "strings"

// A scope is where a stored instruction was set. Scopes form the chain a value is
// resolved through: the most specific place first, then the places it belongs to,
// then the installation, and finally what the build ships.
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
