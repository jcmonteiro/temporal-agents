package agenthub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A location is where work runs. It is a closed tagged union — unknown, directory,
// or remote — because three independently nullable fields would admit contradictory
// combinations (a path *and* a ref, neither, both empty) and every consumer would
// have to re-derive which combination means what.
//
// The union is closed by construction, not by convention: Location's fields are
// unexported, so the only values that exist are the ones the constructors in this
// file produced, and each constructor validates. A struct literal outside this
// package cannot express an invalid location; it can only express the zero value,
// which is deliberately the unknown location (an item whose place was never recorded
// is in the unknown place, not in a null one).
//
// Nothing here knows about git, SQL, or a filesystem. A location is built from facts
// the system recorded, handed in as values, exactly as execution outcomes already
// are.

// ErrInvalidLocation marks a recorded place the system cannot express: a directory
// that is not an absolute cleaned path, a ref that is empty or unbounded, the unknown
// place used as an ancestor.
//
// It is a sentinel of its own rather than ErrInvalid because a location is never
// built from a request: it is built from a fact the system recorded. Answering a
// consumer "change your request" for a directory it never sent — and echoing that
// directory back to it — would name the wrong culprit and leak a server path. This is
// a defect here, so the transport reports it as one.
var ErrInvalidLocation = errors.New("invalid location")

// LocationKind discriminates the union. It is the wire discriminator too, so a
// consumer switches on one field.
type LocationKind string

const (
	// LocationUnknown is the place work runs when nothing was recorded about where.
	// It is a real, rendered place with no directory, no ref, and no parent — never a
	// null branch.
	LocationUnknown LocationKind = "unknown"
	// LocationDirectory is a place on a machine's filesystem: an absolute, cleaned
	// path.
	LocationDirectory LocationKind = "directory"
	// LocationRemote is a place with no local directory, identified by a bounded ref.
	LocationRemote LocationKind = "remote"
)

// LocationKinds lists the union's variants in a stable order, for validation and for
// the published schema.
func LocationKinds() []LocationKind {
	return []LocationKind{LocationUnknown, LocationDirectory, LocationRemote}
}

// Bounds on what a location may carry. They exist so a recorded fact that is absurd
// (a path a filesystem cannot hold, a ref the size of a document) is refused at
// construction rather than stored, published, and rendered.
const (
	maxLocationPathLength = 4096
	maxLocationRefLength  = 512
)

// unknownLocationID is the unknown place's identity. It is a constant rather than a
// digest because there is exactly one unknown place, in every response, forever.
const unknownLocationID = "unknown"

// unknownLocationLabel is the unknown place's server-computed label.
const unknownLocationLabel = "Unknown"

// Location is one place work runs. It is immutable: every accessor is a value method
// and the parent is only ever a location this package already validated.
type Location struct {
	// kind is the variant. The zero value ("") reads as LocationUnknown, which is
	// what makes an unset location on an item mean "the unknown place".
	kind LocationKind
	// directory is set only for the directory variant.
	directory string
	// ref is set only for the remote variant.
	ref string
	// parent is the place this one is part of, or nil for a root. It is a pointer to
	// an already-validated value, so an ancestry chain cannot contain an invalid
	// location.
	parent *Location
}

// UnknownLocation is the place work runs when nothing was recorded about where. It
// takes no parent: the unknown place is a root, and it can never be an ancestor of a
// known one — otherwise every known place would hang under it.
func UnknownLocation() Location { return Location{kind: LocationUnknown} }

// NewDirectoryLocation builds a directory place from a recorded working directory,
// optionally inside a parent place (a worktree inside its repository). parent is nil
// for a root.
//
// The path must be absolute and already cleaned. Cleaning it here instead would make
// two different recorded facts collapse into one identity silently; refusing says
// which fact was wrong. Paths are validated as slash paths rather than through the
// host's path rules, so the same recorded fact is judged the same way wherever the
// contract is served or tested.
func NewDirectoryLocation(directory string, parent *Location) (Location, error) {
	if err := validateLocationText("directory", directory, maxLocationPathLength); err != nil {
		return Location{}, err
	}
	if !strings.HasPrefix(directory, "/") {
		return Location{}, fmt.Errorf("%w: the location directory %q must be absolute", ErrInvalidLocation, directory)
	}
	if path.Clean(directory) != directory {
		return Location{}, fmt.Errorf("%w: the location directory %q must be cleaned (%q)",
			ErrInvalidLocation, directory, path.Clean(directory))
	}
	validParent, err := validateLocationParent(parent)
	if err != nil {
		return Location{}, err
	}
	return Location{kind: LocationDirectory, directory: directory, parent: validParent}, nil
}

// NewRemoteLocation builds a place that has no local directory, identified by a ref.
// parent is nil for a root.
//
// A ref here identifies the *place*, not a revision: a git ref of a piece of work is
// an attribute of that work, and belongs on the item, not on a location.
func NewRemoteLocation(ref string, parent *Location) (Location, error) {
	if err := validateLocationText("ref", ref, maxLocationRefLength); err != nil {
		return Location{}, err
	}
	validParent, err := validateLocationParent(parent)
	if err != nil {
		return Location{}, err
	}
	return Location{kind: LocationRemote, ref: ref, parent: validParent}, nil
}

// validateLocationText applies the rules every location value shares: present, not
// padded, valid text, bounded, and free of characters that cannot be displayed or
// safely logged.
func validateLocationText(field, value string, maxLength int) error {
	if value == "" {
		return fmt.Errorf("%w: the location %s is required", ErrInvalidLocation, field)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: the location %s %q must not be padded with whitespace", ErrInvalidLocation, field, value)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: the location %s is not valid text", ErrInvalidLocation, field)
	}
	if utf8.RuneCountInString(value) > maxLength {
		return fmt.Errorf("%w: the location %s must be at most %d characters", ErrInvalidLocation, field, maxLength)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: the location %s contains control characters", ErrInvalidLocation, field)
	}
	return nil
}

// validateLocationParent copies the parent so the caller cannot mutate a location
// through the pointer it handed in, and refuses the unknown place as an ancestor.
func validateLocationParent(parent *Location) (*Location, error) {
	if parent == nil {
		return nil, nil
	}
	if parent.Kind() == LocationUnknown {
		return nil, fmt.Errorf("%w: the unknown location is a root and cannot be a parent", ErrInvalidLocation)
	}
	copied := *parent
	return &copied, nil
}

// Kind reports which variant this is. The zero value is the unknown place.
func (l Location) Kind() LocationKind {
	if l.kind == "" {
		return LocationUnknown
	}
	return l.kind
}

// Directory reports the path, and whether this variant has one. A remote or unknown
// place answers ("", false): an accessor never invents a value for a variant that
// does not carry it.
func (l Location) Directory() (string, bool) {
	if l.Kind() != LocationDirectory {
		return "", false
	}
	return l.directory, true
}

// Ref reports the ref, and whether this variant has one.
func (l Location) Ref() (string, bool) {
	if l.Kind() != LocationRemote {
		return "", false
	}
	return l.ref, true
}

// Parent reports the place this one is part of, and whether it has one.
func (l Location) Parent() (Location, bool) {
	if l.Kind() == LocationUnknown || l.parent == nil {
		return Location{}, false
	}
	return *l.parent, true
}

// ID is the location's server-issued identity: opaque, stable, and derived only from
// the variant and its natural key, so the same place is the same id in every response
// and in every process, with no coordination and nothing stored.
//
// It is opaque on purpose — a consumer must never take an id apart to recover a path.
// The natural key stays a field of its own for anything that needs the fact itself.
func (l Location) ID() string {
	switch l.Kind() {
	case LocationDirectory:
		return locationDigest(string(LocationDirectory), l.directory)
	case LocationRemote:
		return locationDigest(string(LocationRemote), l.ref)
	default:
		return unknownLocationID
	}
}

// locationDigest hashes a variant and its natural key into a short, stable id. The
// variant is part of the input, so a directory and a remote that happen to share a
// spelling are still two places.
func locationDigest(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + key))
	return hex.EncodeToString(sum[:])[:16]
}

// Label is what to call this place, computed by the server. No consumer derives a
// display name from a path: two consumers that each parse one diverge from each other
// and from this rule the moment it changes.
func (l Location) Label() string {
	switch l.Kind() {
	case LocationDirectory:
		if base := path.Base(l.directory); base != "" && base != "." {
			return base
		}
		return l.directory
	case LocationRemote:
		return l.ref
	default:
		return unknownLocationLabel
	}
}

// depth is how many ancestors this place has. It is what orders a registry
// parents-first: a parent's depth is always strictly smaller than its child's.
func (l Location) depth() int {
	depth := 0
	for current, ok := l.Parent(); ok; current, ok = current.Parent() {
		depth++
	}
	return depth
}

// ancestry renders the chain of ids from this place's parent upwards. Identity
// deliberately excludes the parent (see ID), so this is what tells two values of the
// *same* place with different ancestries apart, and it is a value derived from the
// locations alone — never from the order a caller supplied them in.
func (l Location) ancestry() string {
	var chain strings.Builder
	for current, ok := l.Parent(); ok; current, ok = current.Parent() {
		chain.WriteString(current.ID())
		chain.WriteByte('/')
	}
	return chain.String()
}

// LocationRegistry is the flat set of places one response refers to: every referenced
// location plus all of its ancestors, each exactly once, ordered so a parent always
// precedes its children.
//
// It travels flat rather than nested because a client then builds the tree in one
// pass, a cycle cannot be expressed, and the same place cannot appear twice in one
// payload with two different sets of facts.
type LocationRegistry struct {
	// locations is the closure, already ordered. The field is unexported so a
	// registry cannot be assembled by a struct literal that skips the closure or the
	// ordering.
	locations []Location
	// index is where each id sits in locations, so resolving a reference costs no
	// digest at all. It is built beside the slice and never afterwards.
	index map[string]int
}

// placed is one candidate entry with everything the registry decides on precomputed.
// The id is a digest and the depth is an ancestry walk, so computing them once per
// place instead of twice per comparison keeps a sort of n places to n digests.
type placed struct {
	location Location
	id       string
	depth    int
	ancestry string
}

// place decorates a location with what ordering and deduplication need.
func place(location Location) placed {
	return placed{
		location: location,
		id:       location.ID(),
		depth:    location.depth(),
		ancestry: location.ancestry(),
	}
}

// betterKnownThan reports whether this entry is the one to publish when two entries
// share an id. The deeper ancestry wins, and equal depths are settled by comparing
// the ancestries themselves, so the winner is a function of the two values and of
// nothing else.
func (p placed) betterKnownThan(other placed) bool {
	if p.depth != other.depth {
		return p.depth > other.depth
	}
	return p.ancestry < other.ancestry
}

// NewLocationRegistry builds the registry for the locations a response refers to.
//
// It closes the set under ancestry, removes duplicates by identity, and orders it
// parents-first. Both the set and its order are fully determined by the locations
// handed in, never by the order the caller supplied them in or by a map iteration —
// the API computes entity tags over response bytes, so anything that let the same
// facts render as two different payloads would silently break conditional requests.
//
// Deduplication needs a rule of its own for that to hold. A location's identity is
// its variant and its natural key, deliberately not its parent, so the same place can
// arrive both with and without its ancestry (two recorders, one directory). Keeping
// whichever arrived last would make the payload depend on the caller's order, so the
// better-known ancestry wins instead.
//
// The unknown place is always present: every item whose place was never recorded
// refers to it, and it is a real place an operator sees rather than an absence.
func NewLocationRegistry(referenced ...Location) LocationRegistry {
	byID := map[string]placed{unknownLocationID: place(UnknownLocation())}
	for _, location := range referenced {
		for current, ok := location, true; ok; current, ok = current.Parent() {
			candidate := place(current)
			if existing, seen := byID[candidate.id]; seen && !candidate.betterKnownThan(existing) {
				continue
			}
			byID[candidate.id] = candidate
		}
	}
	entries := make([]placed, 0, len(byID))
	for _, entry := range byID {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.depth != right.depth {
			return left.depth < right.depth
		}
		return left.id < right.id
	})
	locations := make([]Location, 0, len(entries))
	index := make(map[string]int, len(entries))
	for position, entry := range entries {
		locations = append(locations, entry.location)
		index[entry.id] = position
	}
	return LocationRegistry{locations: locations, index: index}
}

// Locations returns the closure in publication order. The slice is a copy, so a
// consumer of the core cannot reorder a registry another consumer is reading.
func (r LocationRegistry) Locations() []Location {
	return append([]Location(nil), r.locations...)
}

// Len is how many places the registry holds. It is never zero: the unknown place is
// always in it.
func (r LocationRegistry) Len() int { return len(r.locations) }

// Contains reports whether an id is in the registry, which is what makes "every
// reference resolves" assertable.
func (r LocationRegistry) Contains(id string) bool {
	_, ok := r.index[id]
	return ok
}
