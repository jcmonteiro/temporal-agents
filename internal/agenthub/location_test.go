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
	for name, directory := range map[string]string{
		"relative":     "work/pricing",
		"undotted":     "/srv/work/../work/pricing",
		"trailing":     "/srv/work/pricing/",
		"empty":        "",
		"padded":       " /srv/work ",
		"control char": "/srv/work\n/pricing",
	} {
		_, err := agenthub.NewDirectoryLocation(directory, nil)
		require.ErrorIs(t, err, agenthub.ErrInvalid, "%s directory %q was accepted", name, directory)
	}
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
		_, err := agenthub.NewRemoteLocation(ref, nil)
		require.ErrorIs(t, err, agenthub.ErrInvalid, "%s ref was accepted", name)
	}
}

func TestTheUnknownPlaceCanNeverBeAnAncestor(t *testing.T) {
	unknown := agenthub.UnknownLocation()

	_, err := agenthub.NewDirectoryLocation("/srv/work", &unknown)

	require.ErrorIs(t, err, agenthub.ErrInvalid,
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

// registryIDs renders a registry as the ids it publishes, in publication order.
func registryIDs(registry agenthub.LocationRegistry) []string {
	ids := make([]string, 0, registry.Len())
	for _, location := range registry.Locations() {
		ids = append(ids, location.ID())
	}
	return ids
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
