package agenthub_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
)

// A recorded place is the only input the core has about where work ran, so these tests
// state what each shape of recorded fact means. They exercise the package from
// outside, as the rest of the location tests do.

func TestWorkRecordedInAWorktreeHangsUnderItsRepository(t *testing.T) {
	// The one parent edge the system has a fact for: git said the working tree and
	// the repository differ, so the worktree is a place inside the repository.
	recorded := agenthub.RecordedPlace{
		Directory:  "/srv/worktrees/pricing-fix",
		Repository: "/srv/repos/pricing",
	}

	location, err := recorded.Location()

	require.NoError(t, err)
	require.Equal(t, agenthub.LocationDirectory, location.Kind())
	directory, hasDirectory := location.Directory()
	require.True(t, hasDirectory)
	require.Equal(t, "/srv/worktrees/pricing-fix", directory)
	parent, hasParent := location.Parent()
	require.True(t, hasParent, "a worktree must hang under the repository it was created from")
	parentDirectory, _ := parent.Directory()
	require.Equal(t, "/srv/repos/pricing", parentDirectory)
	_, grandparent := parent.Parent()
	require.False(t, grandparent, "the chain is worktree to repository, and stops there")
}

func TestWorkRecordedInItsRepositoryIsOnePlace(t *testing.T) {
	// The probe reports the repository as its own working tree for an ordinary
	// checkout. Publishing a parent identical to the child would draw the same place
	// twice.
	for name, recorded := range map[string]agenthub.RecordedPlace{
		"no repository recorded": {Directory: "/srv/repos/pricing"},
		"repository is the same": {Directory: "/srv/repos/pricing", Repository: "/srv/repos/pricing"},
	} {
		t.Run(name, func(t *testing.T) {
			location, err := recorded.Location()

			require.NoError(t, err)
			require.Equal(t, agenthub.LocationDirectory, location.Kind())
			_, hasParent := location.Parent()
			require.False(t, hasParent)
		})
	}
}

func TestWorkWhosePlaceWasNeverRecordedIsUnknown(t *testing.T) {
	// A probe that failed, was never run, or answered nothing leaves the zero value.
	// Unknown is an answer, not an error: guessing would be the failure.
	var nothing agenthub.RecordedPlace

	location, err := nothing.Location()

	require.NoError(t, err)
	require.Equal(t, agenthub.LocationUnknown, location.Kind())
	require.False(t, nothing.Recorded())
}

func TestWorkWithNoLocalDirectoryIsARemotePlace(t *testing.T) {
	recorded := agenthub.RecordedPlace{Ref: "github.com/acme/pricing#42"}

	location, err := recorded.Location()

	require.NoError(t, err)
	require.Equal(t, agenthub.LocationRemote, location.Kind())
	ref, hasRef := location.Ref()
	require.True(t, hasRef)
	require.Equal(t, "github.com/acme/pricing#42", ref)
}

func TestARefNeverCreatesAPlaceForWorkThatRanInADirectory(t *testing.T) {
	// A git ref of a piece of work is an attribute of that work. Work that ran in a
	// directory is in that directory, whatever it had checked out.
	recorded := agenthub.RecordedPlace{Directory: "/srv/repos/pricing", Ref: "feature/pricing"}

	location, err := recorded.Location()

	require.NoError(t, err)
	require.Equal(t, agenthub.LocationDirectory, location.Kind())
	_, hasRef := location.Ref()
	require.False(t, hasRef)
}

func TestARecordedFactThatCannotBeExpressedIsReportedAsADefect(t *testing.T) {
	// Only the probe writes these facts, and it writes absolute cleaned paths, so a
	// fact that cannot be held is a defect here. Downgrading it to unknown would hide
	// the defect behind the same answer an honest failed probe gives.
	for name, recorded := range map[string]agenthub.RecordedPlace{
		"relative directory":  {Directory: "srv/repos/pricing"},
		"relative repository": {Directory: "/srv/worktrees/fix", Repository: "repos/pricing"},
		"unclean directory":   {Directory: "/srv/repos/pricing/"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := recorded.Location()

			require.ErrorIs(t, err, agenthub.ErrInvalidLocation)
		})
	}
}

func TestRecordedPlacesProjectIntoARegistryClosedUnderAncestry(t *testing.T) {
	// What a response is built from: several recorded places, some sharing a
	// repository, become one registry that holds every place and every ancestor.
	first, err := agenthub.RecordedPlace{
		Directory: "/srv/worktrees/a", Repository: "/srv/repos/pricing",
	}.Location()
	require.NoError(t, err)
	second, err := agenthub.RecordedPlace{
		Directory: "/srv/worktrees/b", Repository: "/srv/repos/pricing",
	}.Location()
	require.NoError(t, err)
	repository, err := agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}.Location()
	require.NoError(t, err)

	registry := agenthub.NewLocationRegistry(first, second)

	require.True(t, registry.Contains(repository.ID()),
		"the repository both worktrees hang under must be published too")
	require.Equal(t, 4, registry.Len(), "unknown, the repository, and the two worktrees")
	requireParentsFirst(t, registry)
}
