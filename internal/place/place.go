// Package place defines the location probe: the port that establishes where a unit
// of work runs, the facts that cross it, and the thin activity that drives it.
//
// It exists so the application core never learns how a place is discovered. The core
// reads places as recorded facts (see agenthub.RecordedPlace), exactly as it reads
// execution outcomes; the fact that git is what answers the question lives at the
// edge, in the adapter that implements Prober (see the gitcli package). The
// workflow-side helper that schedules the activity depends on the Temporal SDK and
// lives in the wfplace package, so this port stays SDK-free.
package place

import (
	"context"
	"fmt"
)

// Facts are what the probe established about where a unit of work runs.
//
// They are facts, never inferences: each field is something git or the filesystem
// stated. The zero value means nothing could be established, which every consumer
// reads as the unknown place rather than as a reason to guess.
type Facts struct {
	// Directory is the absolute path of the working tree the work runs in — a linked
	// worktree's own root, or the repository's root for an ordinary checkout.
	Directory string
	// Repository is the absolute path of the repository that working tree belongs
	// to. It is set only when the two genuinely differ, i.e. when git reports the
	// work runs in a linked worktree. It is never filled in by comparing paths: git
	// puts a worktree outside its repository by default, so path containment would
	// invent wrong parents (and miss right ones).
	Repository string
}

// Established reports whether the probe answered anything at all.
func (f Facts) Established() bool { return f.Directory != "" }

// Prober is the driven port that answers "where does work in this directory run?".
//
// An implementation must answer only from what it can establish. A directory that is
// not in a repository, a git that cannot answer, or any other failure is an error —
// not an empty answer dressed up as success — so the caller can degrade to the
// unknown place deliberately.
type Prober interface {
	// Probe reports the facts about dir.
	Probe(ctx context.Context, dir string) (Facts, error)
}

// Activity drives the Prober as a Temporal activity. Like the notification
// activity, it is registered with the worker once and referenced by every workflow
// that owns a working directory, so there is one probe implementation in the
// process rather than one per workflow bundle.
type Activity struct {
	// Prober is the driven adapter. A nil Prober makes the probe fail, which the
	// workflow side turns into the unknown place: a worker wired without a prober
	// must not silently claim work runs nowhere in particular.
	Prober Prober
}

// Probe answers where work in dir runs.
func (a *Activity) Probe(ctx context.Context, dir string) (Facts, error) {
	if a.Prober == nil {
		return Facts{}, fmt.Errorf("no location prober is configured")
	}
	facts, err := a.Prober.Probe(ctx, dir)
	if err != nil {
		return Facts{}, fmt.Errorf("probe where %s runs: %w", dir, err)
	}
	return facts, nil
}
