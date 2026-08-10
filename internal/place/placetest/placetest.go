// Package placetest provides an in-memory location prober for tests.
//
// It is a fake, not a mock: what matters about a probe is the facts it establishes,
// so a test that hands in a repository layout and asserts on the place the API
// publishes says something about the behaviour, while one that asserts the probe was
// called only restates the implementation.
package placetest

import (
	"context"
	"sync"

	"temporal-agents/internal/place"
)

// Prober answers where work runs from a layout a test describes.
//
// Its default answer is an ordinary checkout: the directory asked about is the
// working tree, and there is no second place. A test that needs a worktree, or a
// directory in no repository at all, says so.
type Prober struct {
	mu sync.Mutex
	// answers holds the directories the test described, keyed by the directory
	// probed.
	answers map[string]place.Facts
	// unknown holds the directories that are in no repository, so a probe of one
	// fails exactly as git's would.
	unknown map[string]bool
	// err, when set, fails every probe, standing in for a git that cannot answer.
	err error
}

// Compile-time proof the fake satisfies the port it is injected as.
var _ place.Prober = (*Prober)(nil)

// New returns a prober that answers every directory as its own working tree.
func New() *Prober {
	return &Prober{answers: map[string]place.Facts{}, unknown: map[string]bool{}}
}

// Failing returns a prober whose every probe fails with err.
func Failing(err error) *Prober {
	p := New()
	p.err = err
	return p
}

// InWorktree describes dir as a linked worktree of repository.
func (p *Prober) InWorktree(dir, repository string) *Prober {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[dir] = place.Facts{Directory: dir, Repository: repository}
	return p
}

// InRepository describes dir as a directory whose working tree is repository, which
// is what git answers for a subdirectory of a checkout.
func (p *Prober) InRepository(dir, repository string) *Prober {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[dir] = place.Facts{Directory: repository}
	return p
}

// Nowhere describes dir as a directory in no repository, so probing it fails.
func (p *Prober) Nowhere(dir string) *Prober {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unknown[dir] = true
	return p
}

// Probe implements place.Prober.
func (p *Prober) Probe(_ context.Context, dir string) (place.Facts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return place.Facts{}, p.err
	}
	if p.unknown[dir] {
		return place.Facts{}, errNoRepository
	}
	if facts, described := p.answers[dir]; described {
		return facts, nil
	}
	return place.Facts{Directory: dir}, nil
}

// errNoRepository is what a probe of a directory in no repository fails with.
var errNoRepository = errNotARepository("not a git repository")

type errNotARepository string

func (e errNotARepository) Error() string { return string(e) }
