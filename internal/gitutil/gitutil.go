// Package gitutil provides small git repository helpers used to fix up a
// project folder so `claude --worktree` can run in it.
package gitutil

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"claude-manager/internal/proc"
)

// runGit invokes git directly (no shell) so paths/messages never pass
// through shell quoting/interpolation.
func runGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// EnsureRepoWithCommit makes root usable by `claude --worktree`: it
// initializes a git repository if root isn't one yet, and creates an initial
// commit if HEAD can't be resolved yet. A bare `git init` alone leaves an
// unborn branch — `--worktree` branches from HEAD and fails with
// "Failed to resolve base branch \"HEAD\"" until a commit exists.
func EnsureRepoWithCommit(ctx context.Context, root string) error {
	if _, err := runGit(ctx, root, "rev-parse", "--is-inside-work-tree"); err != nil {
		if _, err := runGit(ctx, root, "init"); err != nil {
			return fmt.Errorf("gitutil: init: %w", err)
		}
	}
	if _, err := runGit(ctx, root, "rev-parse", "HEAD"); err != nil {
		if _, err := runGit(ctx, root, "add", "-A"); err != nil {
			return fmt.Errorf("gitutil: add: %w", err)
		}
		if _, err := runGit(ctx, root, "commit", "--allow-empty", "-m", "Initial commit (claude-manager)"); err != nil {
			return fmt.Errorf("gitutil: commit: %w", err)
		}
	}
	return nil
}
