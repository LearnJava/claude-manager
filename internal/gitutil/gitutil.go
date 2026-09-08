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

// MainBranch reports the name of root's integration branch — the one a task
// branch is cut from and merged back into. Order of evidence: what the remote
// says its HEAD is, then the checked-out branch, then whichever of main/master
// exists locally. Falls back to "main" for a repository with no commits yet
// (git's own default since 2.28), so a freshly bootstrapped project still gets
// a usable protocol.
func MainBranch(ctx context.Context, root string) string {
	if out, err := runGit(ctx, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			// "origin/main" -> "main"
			if i := strings.LastIndex(name, "/"); i >= 0 && i+1 < len(name) {
				name = name[i+1:]
			}
			if name != "" {
				return name
			}
		}
	}
	if out, err := runGit(ctx, root, "symbolic-ref", "--short", "HEAD"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			return name
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := runGit(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return candidate
		}
	}
	return "main"
}
