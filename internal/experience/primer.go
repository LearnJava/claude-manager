package experience

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"claude-manager/internal/gitutil"
	"claude-manager/internal/proc"
	"claude-manager/internal/store"
)

// MaxPrimerChars caps BuildPrimer's output (LEARN-TASKS.md LN-05). A context
// primer's entire point is saving tokens a fresh session would otherwise
// spend re-discovering state, so it must never itself become a meaningful
// fraction of the context window.
const MaxPrimerChars = 2000

// gitTimeout bounds every direct git exec in gitSection: a hung or huge
// repository must never block a session's start (see LEARN-TASKS.md LN-05
// "Готово когда: тест, что падение git не ломает старт").
const gitTimeout = 3 * time.Second

// PrimerInput is BuildPrimer's input.
type PrimerInput struct {
	Project     string
	Session     string
	ProjectPath string
	// TaskDesc is the resolved task_source description for the run about to
	// start (Session.taskSourceDesc) — empty for a session with no queue.
	TaskDesc string
	// Gates are the project's blocking check commands (ProjectConfig.Gates).
	Gates []string

	// Store looks up the files this session's previous run touched. Nil
	// skips that section entirely (e.g. cmd/playwright-server runs with no
	// store) rather than erroring.
	Store *store.Store
}

// BuildPrimer renders the context-primer text: a short, auto-generated block
// of state a fresh session would otherwise spend several tool calls
// re-discovering — current task, git state, files the previous run touched,
// the project's gate commands, and (LN-18) how long this project's slow
// commands actually take. Sections are appended in a fixed priority order
// (matching LEARN-TASKS.md LN-05's numbered list, with the LN-18 timing
// section appended last as the lowest priority) and the whole result is
// capped at MaxPrimerChars: truncation drops whole trailing sections, lowest
// priority first, never mid-section.
//
// Sections 5 (files re-read 3+ times, LN-08) and 6 (journal "avoid" lines,
// LN-06) are not implemented yet — both depend on features that land after
// this one. Plan-driven runs (internal/analysis/executor.go) don't go
// through Session at all, so their plan_subtasks.files_changed is not a
// source here; only the action_signatures lookup below is wired.
func BuildPrimer(in PrimerInput) string {
	var sections []string

	if strings.TrimSpace(in.TaskDesc) != "" {
		sections = append(sections, "Current task: "+in.TaskDesc)
	}

	if git := gitSection(in.ProjectPath); git != "" {
		sections = append(sections, git)
	}

	if in.Store != nil {
		if files := previousRunFilesSection(in.Store, in.Project, in.Session); files != "" {
			sections = append(sections, files)
		}
	}

	if len(in.Gates) > 0 {
		sections = append(sections, "Gate commands:\n"+strings.Join(in.Gates, "\n"))
	}

	if in.Store != nil {
		if profile, err := DurationProfile(in.Store, in.Project); err == nil {
			if dur := durationSection(profile); dur != "" {
				sections = append(sections, dur)
			}
		}
	}

	return truncateSections(sections, MaxPrimerChars)
}

// truncateSections joins sections with blank lines, dropping whole trailing
// sections (lowest priority first, since sections is already in priority
// order) until the result fits within max. A single section longer than max
// on its own is hard-truncated rather than dropped, so the highest-priority
// section is never silently empty.
func truncateSections(sections []string, max int) string {
	for len(sections) > 0 {
		joined := strings.Join(sections, "\n\n")
		if len(joined) <= max {
			return joined
		}
		if len(sections) == 1 {
			return sections[0][:max]
		}
		sections = sections[:len(sections)-1]
	}
	return ""
}

// gitSection reports the integration branch, remote presence/name, current
// branch and the last few commits — the two lines that fixed 13%/23% of
// tbank corpus failures (`origin does not appear to be a git repository`,
// `ambiguous argument 'main'`). A failed or missing git simply omits the
// section (returns "") rather than propagating an error: the primer must
// never block a session's start.
func gitSection(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	if _, err := runGit(ctx, projectPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		return ""
	}

	var lines []string
	lines = append(lines, "main branch: "+gitutil.MainBranch(ctx, projectPath))

	if name := gitRemoteName(ctx, projectPath); name != "" {
		url, _ := runGit(ctx, projectPath, "remote", "get-url", name)
		lines = append(lines, fmt.Sprintf("remote: %s (%s)", name, strings.TrimSpace(url)))
	} else {
		lines = append(lines, "remote: none")
	}

	if cur, err := runGit(ctx, projectPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if cur = strings.TrimSpace(cur); cur != "" {
			lines = append(lines, "current branch: "+cur)
		}
	}

	if log, err := runGit(ctx, projectPath, "log", "--oneline", "-3"); err == nil {
		if log = strings.TrimSpace(log); log != "" {
			lines = append(lines, "recent commits:\n"+log)
		}
	}

	return "Git state:\n" + strings.Join(lines, "\n")
}

// gitRemoteName returns the first configured remote's name, or "" when the
// repository has none.
func gitRemoteName(ctx context.Context, root string) string {
	out, err := runGit(ctx, root, "remote")
	if err != nil {
		return ""
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// runGit invokes git directly (no shell), matching internal/gitutil's own
// convention. Not reused from there because gitutil's helper is unexported
// and returns combined output on error, where this caller only ever wants
// stdout (a stray stderr line must not end up inside the primer text).
func runGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = root
	out, err := cmd.Output()
	return string(out), err
}

// previousRunFilesSection lists the files Edit/Write touched in this
// session's most recent completed run — the concrete answer to "what did I
// just change" that would otherwise cost a `git log -p`/`git diff` round
// trip. Returns "" when there is no previous run or it touched nothing.
func previousRunFilesSection(st *store.Store, project, sessionName string) string {
	runs, err := st.ListRuns(project, sessionName, 1)
	if err != nil || len(runs) == 0 {
		return ""
	}
	actions, err := st.ActionsForRun(runs[0].ID)
	if err != nil {
		return ""
	}

	seen := make(map[string]bool)
	var files []string
	for _, a := range actions {
		if a.Tool != "Edit" && a.Tool != "Write" {
			continue
		}
		f := strings.TrimSpace(a.Arg)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	if len(files) == 0 {
		return ""
	}
	return "Files touched in the previous run:\n" + strings.Join(files, "\n")
}
