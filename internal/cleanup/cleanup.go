// Package cleanup implements the interactive removal of the git worktrees that
// `temporal-agents code develop --worktree` accumulates under the user config
// directory. The Cleaner orchestrates the flow; git access and user prompting
// are ports so the behavior can be tested without touching a real repository or
// a terminal.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Worktree is a git worktree created by temporal-agents, identified by its
// on-disk path and the branch it has checked out.
type Worktree struct {
	// Path is the absolute worktree directory.
	Path string
	// Branch is the checked-out branch name (without the refs/heads/ prefix).
	Branch string
}

// Git is the port for the local repository operations cleanup needs. The
// concrete adapter is a driven adapter over the `git` CLI.
type Git interface {
	// List returns the worktrees under baseDir that belong to the repository at
	// repoDir, i.e. the ones temporal-agents created with `--worktree`.
	List(ctx context.Context, repoDir, baseDir string) ([]Worktree, error)
	// Merged reports whether branch is fully contained in the repository's
	// current HEAD (its commits are already merged in).
	Merged(ctx context.Context, repoDir, branch string) (bool, error)
	// Remove deletes the worktree and its branch. When force is set it removes a
	// worktree with local changes and deletes an unmerged branch; otherwise git
	// refuses both.
	Remove(ctx context.Context, repoDir string, wt Worktree, force bool) error
}

// Prompter is the port for asking the user a yes/no question. defaultYes
// selects the answer used when the user just presses Enter (rendered as [Y/n]
// when true and [y/N] when false by the adapter).
type Prompter interface {
	Confirm(question string, defaultYes bool) (bool, error)
}

// Cleaner drives the interactive cleanup: it lists the temporal-agents
// worktrees, asks before deleting each, checks whether the branch is merged,
// and asks for a forced delete when it is not.
type Cleaner struct {
	Git    Git
	Prompt Prompter
	Out    io.Writer
}

// Run performs the interactive cleanup for the repository at repoDir over the
// worktrees under baseDir. It returns the number of worktrees removed.
func (c *Cleaner) Run(ctx context.Context, repoDir, baseDir string) (int, error) {
	worktrees, err := c.Git.List(ctx, repoDir, baseDir)
	if err != nil {
		return 0, fmt.Errorf("list worktrees: %w", err)
	}
	if len(worktrees) == 0 {
		fmt.Fprintln(c.Out, "No temporal-agents worktrees found.")
		return 0, nil
	}

	removed := 0
	var errs []error
	for _, wt := range worktrees {
		done, err := c.handle(ctx, repoDir, wt)
		if err != nil {
			// A single failure (e.g. a branch that will not delete) should not
			// abandon the remaining worktrees; report it and keep going so the
			// batch and its closing summary still complete.
			fmt.Fprintf(c.Out, "Error on %s: %v\n", wt.Path, err)
			errs = append(errs, err)
			continue
		}
		if done {
			removed++
		}
	}

	fmt.Fprintf(c.Out, "\nDone. %d of %d worktree(s) removed.\n", removed, len(worktrees))
	return removed, errors.Join(errs...)
}

// handle runs the delete decision for a single worktree, returning whether it
// was removed. The user is asked before anything happens, defaulting to "no"
// so a stray Enter never deletes; only then is the merge status checked, and an
// unmerged branch requires a second, force confirmation that also defaults to
// "no".
func (c *Cleaner) handle(ctx context.Context, repoDir string, wt Worktree) (bool, error) {
	ok, err := c.Prompt.Confirm(fmt.Sprintf("Delete worktree %s (branch %s)?", wt.Path, wt.Branch), false)
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Fprintf(c.Out, "Skipped %s.\n", wt.Path)
		return false, nil
	}

	merged, err := c.Git.Merged(ctx, repoDir, wt.Branch)
	if err != nil {
		return false, fmt.Errorf("check merge status of %s: %w", wt.Branch, err)
	}

	force := false
	if !merged {
		force, err = c.Prompt.Confirm(fmt.Sprintf("Branch %s is not merged. Delete by force?", wt.Branch), false)
		if err != nil {
			return false, err
		}
		if !force {
			fmt.Fprintf(c.Out, "Skipped %s (branch not merged).\n", wt.Path)
			return false, nil
		}
	}

	if err := c.Git.Remove(ctx, repoDir, wt, force); err != nil {
		return false, fmt.Errorf("remove worktree %s: %w", wt.Path, err)
	}
	fmt.Fprintf(c.Out, "Removed %s.\n", wt.Path)
	return true, nil
}
