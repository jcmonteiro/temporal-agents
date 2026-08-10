// Package hubplace is the driven adapter that answers what a directory an operator
// named actually is.
//
// It is the registration's half of the location probe: the same git question the
// recorded places come from, plus the one question a probe of running work never has
// to ask — is anything there at all? Work that runs somewhere has already proven the
// directory exists; a directory an operator types has proven nothing.
//
// The two refusals are kept apart because they are different mistakes with different
// fixes: a typo, and a directory no repository holds.
package hubplace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/place"
)

// Inspector implements agenthub.PlaceInspector over the filesystem and the location
// probe.
type Inspector struct {
	// Prober is what establishes the working tree and the repository it belongs to.
	// It is the very same port the workflows probe through, so a registered place and
	// an observed one can never disagree about the same directory.
	Prober place.Prober
}

// Compile-time proof the adapter satisfies the port it is injected as.
var _ agenthub.PlaceInspector = Inspector{}

// Inspect reports the facts about directory.
func (i Inspector) Inspect(ctx context.Context, directory string) (agenthub.RecordedPlace, error) {
	if i.Prober == nil {
		return agenthub.RecordedPlace{}, errors.New("no location prober is configured")
	}
	info, err := os.Stat(directory)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return agenthub.RecordedPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNoSuchDirectory, directory)
	case err != nil:
		// The path cannot be looked at — a permission the server does not have, a mount
		// that is gone. It is not the operator's mistake, so it is not reported as one.
		return agenthub.RecordedPlace{}, fmt.Errorf("look at %s: %w", directory, err)
	case !info.IsDir():
		return agenthub.RecordedPlace{}, fmt.Errorf("%w: %s is not a directory",
			agenthub.ErrNoSuchDirectory, directory)
	}
	facts, err := i.Prober.Probe(ctx, directory)
	if err != nil || !facts.Established() {
		// The probe fails for a directory no repository holds, and that is the only
		// reason it can fail for a directory that demonstrably exists and can be read.
		return agenthub.RecordedPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNotARepository, directory)
	}
	return agenthub.RecordedPlace{Directory: facts.Directory, Repository: facts.Repository}, nil
}
