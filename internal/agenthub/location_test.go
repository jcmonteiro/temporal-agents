package agenthub_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
)

// These tests are behaviour, not structure: they use the package from outside, which
// is the only way to show that the union really is closed — a test inside the package
// could reach the unexported fields the contract depends on being unreachable.

func TestAnItemWithNoRecordedPlaceIsInTheUnknownPlace(t *testing.T) {
	// The zero value is the unknown location. That is what makes "no place was
	// recorded" a real place rather than a null branch every consumer must handle.
	var unset agenthub.Location

	require.Equal(t, agenthub.LocationUnknown, unset.Kind())
	require.Equal(t, "unknown", unset.ID())
	require.Equal(t, unset.ID(), agenthub.UnknownLocation().ID())

	directory, hasDirectory := unset.Directory()
	require.False(t, hasDirectory)
	require.Empty(t, directory)
	ref, hasRef := unset.Ref()
	require.False(t, hasRef)
	require.Empty(t, ref)
	_, hasParent := unset.Parent()
	require.False(t, hasParent, "the unknown place is a root")
}

func TestAVariantReportsOnlyItsOwnFields(t *testing.T) {
	directory, err := agenthub.NewDirectoryLocation("/srv/work/pricing", nil)
	require.NoError(t, err)
	remote, err := agenthub.NewRemoteLocation("github.com/acme/pricing", nil)
	require.NoError(t, err)

	path, hasPath := directory.Directory()
	require.True(t, hasPath)
	require.Equal(t, "/srv/work/pricing", path)
	_, hasRef := directory.Ref()
	require.False(t, hasRef, "a directory place must not answer with a ref")

	ref, hasRef := remote.Ref()
	require.True(t, hasRef)
	require.Equal(t, "github.com/acme/pricing", ref)
	_, hasPath = remote.Directory()
	require.False(t, hasPath, "a remote place must not answer with a directory")
}

func TestConstructionRefusesADirectoryThatIsNotAnAbsoluteCleanedPath(t *testing.T) {
	// Subtests rather than one loop with require: every violating case is reported,
	// instead of one arbitrary case per run.
	for name, directory := range map[string]string{
		"relative":     "work/pricing",
		"undotted":     "/srv/work/../work/pricing",
		"trailing":     "/srv/work/pricing/",
		"empty":        "",
		"padded":       " /srv/work ",
		"control char": "/srv/work\n/pricing",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := agenthub.NewDirectoryLocation(directory, nil)
			require.ErrorIs(t, err, agenthub.ErrInvalidLocation, "directory %q was accepted", directory)
		})
	}
}

func TestARecordedPlaceTheSystemCannotExpressIsNotARequestFailure(t *testing.T) {
	// A location is built from a fact the system recorded, never from what a consumer
	// asked for, so refusing one must not tell a consumer to change its request (the
	// transport answers ErrInvalid with the message itself, which would also publish
	// the recorded path).
	_, err := agenthub.NewDirectoryLocation("srv/work/pricing", nil)

	require.ErrorIs(t, err, agenthub.ErrInvalidLocation)
	require.NotErrorIs(t, err, agenthub.ErrInvalid)
}

func TestConstructionRefusesARefThatIsEmptyOrUnbounded(t *testing.T) {
	oversized := make([]byte, 513)
	for i := range oversized {
		oversized[i] = 'a'
	}
	for name, ref := range map[string]string{
		"empty":     "",
		"blank":     "   ",
		"oversized": string(oversized),
		"control":   "acme\u0000pricing",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := agenthub.NewRemoteLocation(ref, nil)
			require.ErrorIs(t, err, agenthub.ErrInvalidLocation, "the ref was accepted")
		})
	}
}

func TestTheUnknownPlaceCanNeverBeAnAncestor(t *testing.T) {
	unknown := agenthub.UnknownLocation()

	_, err := agenthub.NewDirectoryLocation("/srv/work", &unknown)

	require.ErrorIs(t, err, agenthub.ErrInvalidLocation,
		"the unknown place became a parent, which would hang every known place under it")
}

func TestALabelIsComputedByTheServerForEveryVariant(t *testing.T) {
	directory, err := agenthub.NewDirectoryLocation("/srv/work/pricing", nil)
	require.NoError(t, err)
	remote, err := agenthub.NewRemoteLocation("github.com/acme/pricing", nil)
	require.NoError(t, err)
	root, err := agenthub.NewDirectoryLocation("/", nil)
	require.NoError(t, err)

	require.Equal(t, "pricing", directory.Label())
	require.Equal(t, "github.com/acme/pricing", remote.Label())
	require.Equal(t, "/", root.Label())
	require.Equal(t, "Unknown", agenthub.UnknownLocation().Label())
}

func TestIdentityIsTheNaturalKeyAndNothingElse(t *testing.T) {
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	worktree, err := agenthub.NewDirectoryLocation("/srv/work/pricing", &repository)
	require.NoError(t, err)
	sameWorktreeNoParent, err := agenthub.NewDirectoryLocation("/srv/work/pricing", nil)
	require.NoError(t, err)
	collidingRemote, err := agenthub.NewRemoteLocation("/srv/work/pricing", nil)
	require.NoError(t, err)

	require.Equal(t, worktree.ID(), sameWorktreeNoParent.ID(), "the same place must be the same id")
	require.NotEqual(t, worktree.ID(), repository.ID())
	require.NotEqual(t, worktree.ID(), collidingRemote.ID(), "the variant is part of the identity")
	require.NotContains(t, worktree.ID(), "srv", "the id must not leak the path it identifies")
}

func TestARegistryEmitsEachPlaceOnceAndClosesOverAncestry(t *testing.T) {
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	first, err := agenthub.NewDirectoryLocation("/srv/work/one", &repository)
	require.NoError(t, err)
	second, err := agenthub.NewDirectoryLocation("/srv/work/two", &repository)
	require.NoError(t, err)

	// The repository is never referenced directly, and the worktrees are referenced
	// twice each.
	registry := agenthub.NewLocationRegistry(first, second, first, second)

	ids := registryIDs(registry)
	require.Len(t, ids, 4, "want unknown, the repository, and the two worktrees exactly once: %v", ids)
	require.Contains(t, ids, repository.ID(), "the ancestry closure is missing an ancestor")
	require.True(t, registry.Contains(first.ID()))
	require.True(t, registry.Contains("unknown"), "the unknown place is always in the registry")
}

func TestARegistryIsOrderedParentsFirstAndIndependentOfInputOrder(t *testing.T) {
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	worktree, err := agenthub.NewDirectoryLocation("/srv/work/pricing", &repository)
	require.NoError(t, err)
	deeper, err := agenthub.NewDirectoryLocation("/srv/work/pricing/sub", &worktree)
	require.NoError(t, err)

	forwards := registryIDs(agenthub.NewLocationRegistry(deeper, worktree, repository))
	backwards := registryIDs(agenthub.NewLocationRegistry(repository, worktree, deeper))

	require.Equal(t, forwards, backwards, "the order depends on the caller's order")
	require.Less(t, indexOf(forwards, repository.ID()), indexOf(forwards, worktree.ID()))
	require.Less(t, indexOf(forwards, worktree.ID()), indexOf(forwards, deeper.ID()))
	for _, location := range agenthub.NewLocationRegistry(deeper).Locations() {
		parent, ok := location.Parent()
		if !ok {
			continue
		}
		require.Less(t, indexOf(forwards, parent.ID()), indexOf(forwards, location.ID()),
			"a child was published before its parent")
	}
}

func TestAPlaceReferencedWithAndWithoutItsAncestryIsPublishedTheSameWayEitherOrder(t *testing.T) {
	// A place's identity is its natural key, not its parent, so the same directory can
	// be referenced by one source that knows its repository and by another that does
	// not. If arrival order decided which value is published, the same facts would
	// render as two different payloads — a different parent, a different depth, a
	// different order, and therefore a different entity tag.
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	withAncestry, err := agenthub.NewDirectoryLocation("/srv/work/pricing", &repository)
	require.NoError(t, err)
	withoutAncestry, err := agenthub.NewDirectoryLocation("/srv/work/pricing", nil)
	require.NoError(t, err)

	first := agenthub.NewLocationRegistry(withAncestry, withoutAncestry)
	second := agenthub.NewLocationRegistry(withoutAncestry, withAncestry)

	require.Equal(t, registryIDs(first), registryIDs(second), "the published set depends on the caller's order")
	require.Equal(t, registryParents(first), registryParents(second),
		"the same place is published with two different ancestries")
	require.Equal(t, repository.ID(), registryParents(first)[withAncestry.ID()],
		"the better-known ancestry was dropped, orphaning the repository")
}

func TestAPlacePublishedDeeperThanTheCopyInsideItsChildStillPrecedesThatChild(t *testing.T) {
	// Publication order is a property of the graph that is published, not of the values
	// that were handed in. One recorder knows the worktree's repository; another knows
	// that repository sits inside a group. The published repository is then a level
	// deeper than the copy embedded in the worktree, and an order taken from the
	// embedded copy would compare parent and child as equals and let the id tie-break
	// publish the child first, breaking the single pass a client builds its tree in.
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	worktree, err := agenthub.NewDirectoryLocation("/srv/work/pricing", &repository)
	require.NoError(t, err)
	group, err := agenthub.NewDirectoryLocation("/srv/repos", nil)
	require.NoError(t, err)
	repositoryUnderGroup, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", &group)
	require.NoError(t, err)

	forwards := agenthub.NewLocationRegistry(worktree, repositoryUnderGroup)
	backwards := agenthub.NewLocationRegistry(repositoryUnderGroup, worktree)

	require.Equal(t, registryIDs(forwards), registryIDs(backwards), "the order depends on the caller's order")
	requireParentsFirst(t, forwards)
	ids := registryIDs(forwards)
	require.Less(t, indexOf(ids, group.ID()), indexOf(ids, repository.ID()))
	require.Less(t, indexOf(ids, repository.ID()), indexOf(ids, worktree.ID()))
}

func TestConflictingAncestriesCannotPublishACycle(t *testing.T) {
	// A place's identity excludes its parent, so two recorders can each place the
	// other's place inside their own. Choosing the better-known value per id is a local
	// decision that cannot see the loop two such decisions close, so the loop is opened
	// after the choosing: a client that follows parentId in one pass must always finish.
	firstAlone, err := agenthub.NewDirectoryLocation("/srv/a", nil)
	require.NoError(t, err)
	secondAlone, err := agenthub.NewDirectoryLocation("/srv/b", nil)
	require.NoError(t, err)
	firstUnderSecond, err := agenthub.NewDirectoryLocation("/srv/a", &secondAlone)
	require.NoError(t, err)
	secondUnderFirst, err := agenthub.NewDirectoryLocation("/srv/b", &firstAlone)
	require.NoError(t, err)

	registry := agenthub.NewLocationRegistry(firstUnderSecond, secondUnderFirst)
	reversed := agenthub.NewLocationRegistry(secondUnderFirst, firstUnderSecond)

	requireParentsFirst(t, registry)
	require.Equal(t, registryIDs(registry), registryIDs(reversed), "the break depends on the caller's order")
	require.Equal(t, registryParents(registry), registryParents(reversed),
		"the same conflicting facts publish two different trees")
	// Only what closes the loop is dropped: the member with the smallest id is
	// republished as a root, and the other keeps the ancestry it was recorded with.
	parents := registryParents(registry)
	rooted, kept := firstAlone.ID(), secondAlone.ID()
	if kept < rooted {
		rooted, kept = kept, rooted
	}
	require.Equal(t, "", parents[rooted], "the loop was not opened at the smallest id: %v", parents)
	require.Equal(t, rooted, parents[kept], "more ancestry was dropped than the loop needed: %v", parents)
}

func TestARegistryOfTheSamePlacesIsAlwaysTheSameSequence(t *testing.T) {
	// The API computes entity tags over response bytes, so the order must come from
	// the set alone and never from map iteration.
	repository, err := agenthub.NewDirectoryLocation("/srv/repos/pricing", nil)
	require.NoError(t, err)
	remote, err := agenthub.NewRemoteLocation("github.com/acme/pricing", nil)
	require.NoError(t, err)
	worktree, err := agenthub.NewDirectoryLocation("/srv/work/pricing", &repository)
	require.NoError(t, err)

	want := registryIDs(agenthub.NewLocationRegistry(worktree, remote))
	for range 50 {
		require.Equal(t, want, registryIDs(agenthub.NewLocationRegistry(worktree, remote)))
	}
}

func TestARegistryCannotBeReorderedThroughWhatItReturns(t *testing.T) {
	directory, err := agenthub.NewDirectoryLocation("/srv/work/pricing", nil)
	require.NoError(t, err)
	registry := agenthub.NewLocationRegistry(directory)
	require.Equal(t, 2, registry.Len())

	returned := registry.Locations()
	before := registryIDs(registry)
	returned[0], returned[1] = agenthub.Location{}, agenthub.Location{}

	require.Equal(t, before, registryIDs(registry))
	require.True(t, registry.Contains(directory.ID()))
}

// requireParentsFirst asserts the guarantee the flat shape exists for: every
// published parent reference resolves inside the registry and appears before the
// entry that names it, so following parentId terminates.
func requireParentsFirst(t *testing.T, registry agenthub.LocationRegistry) {
	t.Helper()
	ids := registryIDs(registry)
	for _, location := range registry.Locations() {
		parent, ok := location.Parent()
		if !ok {
			continue
		}
		require.True(t, registry.Contains(parent.ID()),
			"%q hangs under %q, which the registry does not publish", location.ID(), parent.ID())
		require.Less(t, indexOf(ids, parent.ID()), indexOf(ids, location.ID()),
			"%q is published before its parent %q", location.ID(), parent.ID())
	}
}

// registryIDs renders a registry as the ids it publishes, in publication order.
func registryIDs(registry agenthub.LocationRegistry) []string {
	ids := make([]string, 0, registry.Len())
	for _, location := range registry.Locations() {
		ids = append(ids, location.ID())
	}
	return ids
}

// registryParents renders which place each published place hangs under, with "" for a
// root. It is what shows that the tree a client draws is the same tree.
func registryParents(registry agenthub.LocationRegistry) map[string]string {
	parents := make(map[string]string, registry.Len())
	for _, location := range registry.Locations() {
		if parent, ok := location.Parent(); ok {
			parents[location.ID()] = parent.ID()
			continue
		}
		parents[location.ID()] = ""
	}
	return parents
}

// indexOf reports where an id appears, or -1.
func indexOf(ids []string, id string) int {
	for i, candidate := range ids {
		if candidate == id {
			return i
		}
	}
	return -1
}
