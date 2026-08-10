package gitcli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"temporal-agents/internal/place"
)

// Probe reports where work in dir runs. It implements the place.Prober port.
//
// It answers with two facts and no inference: the working tree git says dir belongs
// to, and — only when git says the work runs in a *linked worktree* — the repository
// that worktree was created from. Whether the two differ is git's own answer (the
// worktree's git directory against the repository's common one), never a comparison
// of path prefixes: git puts a worktree outside its repository by default, so prefix
// logic would state parents that are wrong and miss the ones that are right.
//
// A directory that is not in a repository, and a git that cannot answer, are
// failures rather than empty answers. The caller degrades a failure to the unknown
// place deliberately (see wfplace.Probe), so nothing between here and the API has to
// decide whether an empty answer meant "nowhere" or "did not ask".
func (g Git) Probe(ctx context.Context, dir string) (place.Facts, error) {
	// One call answers all three questions, in the order they are asked, one per line
	// — a path can contain spaces, so only the line breaks separate the answers.
	// --path-format=absolute is what makes the answers comparable at all: without it
	// git may answer with paths relative to dir, and a relative path is neither an
	// identity nor something the core can hold.
	out, err := run(ctx, dir, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return place.Facts{}, err
	}
	answers := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(answers) != 3 {
		return place.Facts{}, fmt.Errorf(
			"git rev-parse answered %d value(s), want the working tree, its git directory and the common one",
			len(answers))
	}
	workingTree, gitDir, commonGitDir := answers[0], answers[1], answers[2]
	facts := place.Facts{Directory: filepath.Clean(workingTree)}
	if gitDir == commonGitDir {
		// The work runs in the repository's own working tree. There is no second place
		// here, and inventing one from the path would draw the same place twice.
		return facts, nil
	}
	repository, err := mainWorkingTree(ctx, dir)
	if err != nil {
		// Which repository the worktree belongs to could not be established. The
		// worktree itself still was, and reporting it without a parent is the honest
		// half-answer; claiming the whole probe failed would throw away a fact.
		return facts, nil
	}
	facts.Repository = repository
	return facts, nil
}

// mainWorkingTree asks git which working tree the repository itself is checked out
// in. `git worktree list` reports it first, by definition, and asking git is what
// keeps this a probed fact — deriving it from the common git directory's path would
// assume that directory is always named ".git" inside the repository, which a bare
// or relocated repository breaks.
//
// -z is required for the same reason as in List: without it git C-quotes paths that
// contain unusual bytes, and a quoted path is not the path.
func mainWorkingTree(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Split(out, "\x00") {
		if path, found := strings.CutPrefix(field, "worktree "); found {
			return filepath.Clean(path), nil
		}
	}
	return "", fmt.Errorf("git worktree list named no working tree")
}
