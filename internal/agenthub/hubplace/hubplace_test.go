package hubplace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/hubplace"
	"temporal-agents/internal/place"
)

// The filesystem is real here — the adapter's whole job is to answer about it — and
// only the git question is stood in for, because what git answers is gitcli's
// contract and not this adapter's.

// prober answers with fixed facts, or with a failure, standing in for git.
type prober struct {
	facts place.Facts
	err   error
}

func (p prober) Probe(context.Context, string) (place.Facts, error) { return p.facts, p.err }

func TestADirectoryInARepositoryIsInspectedIntoTheFactsTheProbeEstablished(t *testing.T) {
	directory := t.TempDir()
	inspector := hubplace.Inspector{Prober: prober{facts: place.Facts{
		Directory:  "/srv/worktrees/pricing-fix",
		Repository: "/srv/repos/pricing",
	}}}

	facts, err := inspector.Inspect(context.Background(), directory)

	require.NoError(t, err)
	require.Equal(t, "/srv/worktrees/pricing-fix", facts.Directory)
	require.Equal(t, "/srv/repos/pricing", facts.Repository,
		"the repository a worktree belongs to is the probe's answer, not this adapter's")
}

func TestADirectoryThatIsNotThereIsSaidToBeMissing(t *testing.T) {
	inspector := hubplace.Inspector{Prober: prober{facts: place.Facts{Directory: "/anywhere"}}}

	_, err := inspector.Inspect(context.Background(), filepath.Join(t.TempDir(), "gone"))

	require.ErrorIs(t, err, agenthub.ErrNoSuchDirectory)
}

func TestAFileIsNotAPlace(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	inspector := hubplace.Inspector{Prober: prober{facts: place.Facts{Directory: "/anywhere"}}}

	_, err := inspector.Inspect(context.Background(), file)

	require.ErrorIs(t, err, agenthub.ErrNoSuchDirectory)
}

func TestADirectoryNoRepositoryHoldsIsSaidToBeUnversioned(t *testing.T) {
	directory := t.TempDir()

	cases := map[string]prober{
		"the probe refuses":       {err: errors.New("not a git repository")},
		"the probe knows nothing": {facts: place.Facts{}},
	}
	for name, git := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := hubplace.Inspector{Prober: git}.Inspect(context.Background(), directory)

			require.ErrorIs(t, err, agenthub.ErrNotARepository)
		})
	}
}
