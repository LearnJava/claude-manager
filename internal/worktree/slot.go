// Package worktree manages a session's persistent work folder: a slot of the
// project's worktree pool (<project>/.claude/worktrees/<slot>), the same one
// scripts/worktree-pool.sh and the task protocol use. The manager only
// guarantees the folder exists and is sound; which branch the slot sits on is
// the protocol's business.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"claude-manager/internal/config"
	"claude-manager/internal/proc"
)

var (
	statusSourceRe = regexp.MustCompile(`(?i)^STATUS-P(\d+)\.md$`)
	nonSlotCharsRe = regexp.MustCompile(`[^a-z0-9]+`)
)

// SlotName picks the pool slot for a session: an explicit worktree_name, else
// p<N>-work for a STATUS-P<N>.md queue, else "<session-name>-work". The result
// matches ^[a-z0-9-]+$ (worktree-pool.sh's slot check) unless an explicit
// worktree_name says otherwise.
func SlotName(cfg config.SessionConfig) string {
	if n := strings.TrimSpace(cfg.WorktreeName); n != "" {
		return n
	}
	if m := statusSourceRe.FindStringSubmatch(strings.TrimSpace(cfg.TaskSource)); m != nil {
		return "p" + m[1] + "-work"
	}
	base := strings.Trim(nonSlotCharsRe.ReplaceAllString(strings.ToLower(cfg.Name), "-"), "-")
	if base == "" {
		return "session-work"
	}
	return base + "-work"
}

// SlotPath is the slot's folder inside the project.
func SlotPath(projectPath, slot string) string {
	return filepath.Join(projectPath, ".claude", "worktrees", slot)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// EnsureSlot makes sure the slot's folder is a registered, sound worktree and
// returns its path; created reports whether it had to be made. It never
// deletes anything: a folder that is not a worktree is an error for the user.
func EnsureSlot(ctx context.Context, projectPath, slot, mainBranch string, progress func(string)) (path string, created bool, err error) {
	if progress == nil {
		progress = func(string) {}
	}
	path = SlotPath(projectPath, slot)
	if _, statErr := os.Stat(path); statErr == nil {
		gitdir, ok := readGitdir(path)
		if !ok {
			return path, false, fmt.Errorf("worktree: %s exists but is not a git worktree — an orphaned leftover; check `git worktree list`, run `git worktree prune`, remove the folder by hand", path)
		}
		if err := repairInterrupted(ctx, path, gitdir, mainBranch, progress); err != nil {
			return path, false, err
		}
		return path, false, nil
	}

	if err := ensureIgnored(ctx, projectPath); err != nil {
		return path, false, err
	}
	progress("создаю рабочую папку сессии …")
	if _, err := runGit(ctx, projectPath, "worktree", "add", "--detach", path, mainBranch); err != nil {
		return path, false, fmt.Errorf("worktree: %w", err)
	}
	return path, true, nil
}

// readGitdir returns the gitdir a worktree's .git file points to, if path is
// a registered worktree whose gitdir still exists.
func readGitdir(path string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", false
	}
	gd := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(path, gd)
	}
	if st, err := os.Stat(gd); err != nil || !st.IsDir() {
		return "", false
	}
	return gd, true
}

// repairInterrupted fixes a checkout that was killed midway (index.lock left,
// no index) — but only when the slot holds no commits of its own.
func repairInterrupted(ctx context.Context, path, gitdir, mainBranch string, progress func(string)) error {
	lock := filepath.Join(gitdir, "index.lock")
	if _, err := os.Stat(lock); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(gitdir, "index")); err == nil {
		return nil
	}
	out, err := runGit(ctx, path, "log", "--oneline", mainBranch+"..HEAD")
	if err != nil {
		return fmt.Errorf("worktree: cannot check slot %s for own commits: %w", path, err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("worktree: slot %s has an interrupted checkout (index.lock) but also commits of its own — left untouched, resolve by hand", path)
	}
	if err := os.Remove(lock); err != nil {
		return fmt.Errorf("worktree: remove %s: %w", lock, err)
	}
	if _, err := runGit(ctx, path, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	progress("починил прерванный checkout слота " + path + " …")
	return nil
}

// ensureIgnored adds /.claude/worktrees/ to the repository's local
// info/exclude (never .gitignore) unless git already ignores it.
func ensureIgnored(ctx context.Context, projectPath string) error {
	if _, err := runGit(ctx, projectPath, "check-ignore", "-q", ".claude/worktrees/x"); err == nil {
		return nil
	}
	out, err := runGit(ctx, projectPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(projectPath, common)
	}
	file := filepath.Join(common, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	existing, _ := os.ReadFile(file)
	text := string(existing)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "/.claude/worktrees/\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	return nil
}

// Release frees the branch a slot sits on so another checkout (the project
// root, once use_worktree is off) can take it: the slot is switched to a
// detached HEAD at the same commit, nothing is lost. branch is "" when path
// is not a worktree or already detached. A slot with uncommitted changes to
// tracked files is left alone and reported as dirty — moving them is the
// session's job, not the manager's.
func Release(ctx context.Context, path string) (branch string, dirty bool, err error) {
	if _, ok := readGitdir(path); !ok {
		return "", false, nil
	}
	out, err := runGit(ctx, path, "symbolic-ref", "--short", "-q", "HEAD")
	branch = strings.TrimSpace(out)
	if err != nil || branch == "" {
		return "", false, nil
	}
	out, err = runGit(ctx, path, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return branch, false, err
	}
	if strings.TrimSpace(out) != "" {
		return branch, true, nil
	}
	if _, err := runGit(ctx, path, "switch", "-q", "--detach"); err != nil {
		return branch, false, err
	}
	return branch, false, nil
}
