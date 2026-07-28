package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrRoadmapFilesExist is returned by WriteRoadmapFiles when ROADMAP.md or
// STATUS-P1.md already exists in the target project and overwrite is false —
// a hand-maintained roadmap is never silently clobbered.
var ErrRoadmapFilesExist = errors.New("analysis: ROADMAP.md or STATUS-P1.md already exists in this project")

const (
	roadmapFileName = "ROADMAP.md"
	statusFileName  = "STATUS-P1.md"
	tasksDirName    = "tasks"

	// maxRowRunes mirrors maxTaskDescLen in internal/session/session.go:
	// resolveTaskSourceDescription truncates the pointed-at line to 200 runes
	// before the UI ever sees it, so a longer row loses its tail (the Details
	// link and the dependency column) in TaskPanel. Summaries are trimmed to
	// keep the whole rendered row inside this budget.
	maxRowRunes = 200
)

// DefaultP1SessionPrompt is the default Prompt for the "P1" session
// bootstrapped after a roadmap is written. It only ever applies to projects
// whose roadmap was generated through this app (upsertP1Session in app.go is
// only reached via ApproveRoadmap) — a project that was merely added to the
// app (Settings' plain Projects tab) never gets a Prompt written for it here.
// The protocol is intentionally generic across languages/tools/build systems
// (no cargo/npm/pytest/etc. named) and mirrors the session start/end
// discipline this app's own design is modeled on (see CLAUDE.md's Task
// Source Check / crash-recovery sections): sync-then-isolate at the start,
// full quality gates + a clean merge at the end, one task per session.
const DefaultP1SessionPrompt = `You are the sole developer working through this project's task backlog, one
task per session.

SESSION START
1. Sync with the remote first, before reading any task files or touching
   branches: pull the latest main. If this surfaces real conflicts (not a
   fast-forward), resolve them file by file with full understanding of both
   sides' intent - never blindly take one side - and re-verify with a build/
   lint/test pass before committing the merge.
2. Confirm you are isolated in a dedicated git worktree for this task alone
   (check your working directory / "git worktree list"). If you are not
   already isolated, create a fresh worktree and branch off main now, before
   touching any files. Never work directly in the primary checkout, and never
   let two tasks share a worktree.
3. Read STATUS-P1.md in the project root. It contains one task pointer per
   line ("ROADMAP.md:NN"), in priority order top to bottom.
4. If there is uncommitted work in this worktree from an earlier, interrupted
   attempt at this same task, continue it instead of starting over.
5. Otherwise, take the first pointer line as this session's task. Open
   ROADMAP.md at the line number it gives and read that task's row. If the row
   has a Details link (a "tasks/NN-....md" file), open it and work from that
   file: it holds the full description and the acceptance criteria, and the
   one-line row alone is never enough to start. Older roadmaps have no Details
   column - there the row itself is the whole task.

DOING THE WORK
Implement the task completely. While writing code, run only quick, light
checks (compile/syntax/type-check for the piece you touched) - save the full
project-wide check for the end. One run of any check is one log file; filter
it by re-reading that file, never rerun the check just to see it filtered
differently.

SESSION END (do all of this, in order, before stopping)
1. Run the strictest static-analysis/lint check this project has, across the
   whole project, at maximum strictness (treat warnings as errors if the
   tool supports it). Fix every finding before continuing; any suppressed
   warning needs an explicit reason.
2. Run the tests for the modules/areas you touched (the full suite if the
   project is small enough that this is cheap). Never commit failing tests.
3. Update STATUS-P1.md: delete this task's pointer line (only that one line -
   leave the rest of the file untouched).
4. If this project keeps capability/architecture docs, a decision log, or a
   bug tracker, update whatever entries this task affects.
5. Commit the documentation updates if they were not already part of the code
   commit.
6. Merge your branch into main with a non-fast-forward merge (so the history
   stays visible). Main must not be checked out elsewhere when you do this.
7. Delete your branch and remove your worktree.
8. Push main to the remote immediately - do not leave a completed merge only
   local.
9. Confirm the merge commit is visible in the log, then stop. Do not start
   the next task in this session.

If STATUS-P1.md has no pointer lines left when you start, say so and stop
without making any changes.`

// flattenExecutionOrder returns subtask IDs in priority order: groups from
// executionOrder, top to bottom, ids within a group in their original
// relative order (deduped); any subtask id missing from executionOrder (an
// analyst omission) is appended at the end in subtasks order, so nothing is
// silently dropped.
func flattenExecutionOrder(executionOrder [][]string, subtasks []PlannedSubtask) []string {
	seen := make(map[string]bool, len(subtasks))
	out := make([]string, 0, len(subtasks))
	for _, group := range executionOrder {
		for _, id := range group {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, s := range subtasks {
		if !seen[s.ID] {
			seen[s.ID] = true
			out = append(out, s.ID)
		}
	}
	return out
}

// oneLine collapses a multi-line/whitespace-heavy string into a single line
// suitable for a ROADMAP.md row. This matters downstream: resolveTaskSourceDescription
// (internal/session/session.go) returns exactly one line of text for display
// in the UI, so a row must be self-descriptive on its own.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", "/")
}

// truncateRunes shortens s to at most n runes, appending an ellipsis when it
// actually cut something. Rune-based (not byte-based) so a Russian roadmap
// never gets cut mid-character.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

// firstSentence returns the first sentence of s (or the whole string if it has
// no sentence break). Used as the table-row summary for plans whose analyst
// never filled the dedicated `summary` field — older plans, and any model that
// ignores it.
func firstSentence(s string) string {
	s = oneLine(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		rest := s[i+len(string(r)):]
		// A period followed by a space (or end of string) ends a sentence;
		// "go.mod" or "1." mid-word does not.
		if rest == "" {
			return s
		}
		if strings.HasPrefix(rest, " ") && i > 0 {
			return s[:i+len(string(r))]
		}
	}
	return s
}

// rowSummary is the short text that goes into the ROADMAP.md table row: the
// analyst's `summary` when present, else the first sentence of the full
// prompt. Trimmed to fit so the whole row survives the 200-rune truncation in
// resolveTaskSourceDescription.
func rowSummary(sub *PlannedSubtask, budget int) string {
	text := oneLine(sub.Summary)
	if text == "" {
		text = firstSentence(sub.Prompt)
	}
	return truncateRunes(text, budget)
}

// taskFileName renders the per-task detail file name: a zero-padded position
// plus a slug of the analyst's subtask id ("03-auth-middleware.md"). Falls
// back to the bare number when the id has no usable ASCII characters (a
// Cyrillic or symbol-only id), so the name is always a safe relative path.
func taskFileName(num int, id string) string {
	slug := slugify(id)
	if slug == "" {
		return fmt.Sprintf("%02d.md", num)
	}
	return fmt.Sprintf("%02d-%s.md", num, slug)
}

// slugify reduces s to lowercase [a-z0-9-], collapsing runs of anything else
// into single dashes. Non-ASCII is dropped rather than transliterated — the
// number prefix already makes the file name unique and sortable.
func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	return truncateSlug(out, 40)
}

// truncateSlug caps a slug's length without leaving a trailing dash.
func truncateSlug(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimRight(s[:n], "-")
}

// renderTaskFile is the body of one tasks/NN-*.md file: everything the analyst
// produced for that subtask, in a shape a session can work from directly.
// The full prompt lives here rather than in the ROADMAP.md row so the roadmap
// stays navigable at 40+ tasks and a session only reads its own task.
func renderTaskFile(num int, sub *PlannedSubtask, deps string) string {
	var b strings.Builder
	name := oneLine(sub.Name)
	if name == "" {
		name = sub.ID
	}
	fmt.Fprintf(&b, "# %d. %s\n\n", num, name)

	fmt.Fprintf(&b, "**Depends on:** %s\n", deps)
	if sub.EstimatedTokens > 0 {
		fmt.Fprintf(&b, "**Estimated context:** ~%dk\n", sub.EstimatedTokens/1000)
	}
	if len(sub.FilesToTouch) > 0 {
		fmt.Fprintf(&b, "**Files:** %s\n", strings.Join(sub.FilesToTouch, ", "))
	}
	if sub.Model != "" {
		fmt.Fprintf(&b, "**Suggested model:** %s\n", sub.Model)
	}
	b.WriteString("\n## Task\n\n")
	prompt := strings.TrimSpace(sub.Prompt)
	if prompt == "" {
		prompt = "(the analyst produced no description for this task)"
	}
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String()
}

// tasksDirBlocked reports whether dir already holds task files from an earlier
// roadmap. Part of the same "never silently clobber a hand-maintained or
// in-flight roadmap" guard as ErrRoadmapFilesExist: writing a fresh set of
// numbered task files into a populated tasks/ would interleave two roadmaps.
func tasksDirBlocked(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false // missing (or unreadable) dir is not a conflict
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			return true
		}
	}
	return false
}

// WriteRoadmapFiles renders plan's subtasks into <projectPath>/ROADMAP.md,
// one <projectPath>/tasks/NN-<id>.md detail file per task, and
// <projectPath>/STATUS-P1.md, in the exact bare-pointer-line format that
// hasTasks/resolveTaskSourceDescription (internal/session/session.go) already
// consume — no changes are needed on that side.
//
// ROADMAP.md holds only a navigable one-row-per-task table (name, one-line
// summary, link to the detail file, dependencies); the full task text lives in
// tasks/. That split keeps the roadmap readable at 40+ tasks and means a
// session reads its own task rather than everyone else's.
//
// Line numbers in STATUS-P1.md are computed by re-reading the just-written
// ROADMAP.md rather than trusting the in-memory builder, so they are
// guaranteed to match what those functions will see on disk.
//
// Refuses to touch anything (returning ErrRoadmapFilesExist) if ROADMAP.md or
// STATUS-P1.md already exists, or tasks/ already holds markdown files, and
// overwrite is false.
func WriteRoadmapFiles(projectPath string, plan *TaskPlan, overwrite bool) (roadmapPath, statusPath string, err error) {
	if plan == nil {
		return "", "", errors.New("analysis: nil plan")
	}
	roadmapPath = filepath.Join(projectPath, roadmapFileName)
	statusPath = filepath.Join(projectPath, statusFileName)
	tasksDir := filepath.Join(projectPath, tasksDirName)

	if !overwrite {
		if _, statErr := os.Stat(roadmapPath); statErr == nil {
			return "", "", ErrRoadmapFilesExist
		}
		if _, statErr := os.Stat(statusPath); statErr == nil {
			return "", "", ErrRoadmapFilesExist
		}
		if tasksDirBlocked(tasksDir) {
			return "", "", ErrRoadmapFilesExist
		}
	}

	byID := make(map[string]*PlannedSubtask, len(plan.Subtasks))
	for i := range plan.Subtasks {
		byID[plan.Subtasks[i].ID] = &plan.Subtasks[i]
	}

	// Drop any execution_order id that doesn't correspond to an actual
	// subtask (a hallucinated/typo'd id) so row numbers stay contiguous.
	order := make([]string, 0, len(plan.Subtasks))
	for _, id := range flattenExecutionOrder(plan.ExecutionOrder, plan.Subtasks) {
		if byID[id] != nil {
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		return "", "", errors.New("analysis: plan has no subtasks to write")
	}

	rowNum := make(map[string]int, len(order))
	for i, id := range order {
		rowNum[id] = i + 1
	}

	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return "", "", fmt.Errorf("analysis: create tasks dir: %w", err)
	}

	rows := make([]string, len(order))
	for i, id := range order {
		sub := byID[id]
		num := i + 1
		deps := "-"
		if len(sub.DependsOn) > 0 {
			nums := make([]string, 0, len(sub.DependsOn))
			for _, d := range sub.DependsOn {
				if n, ok := rowNum[d]; ok {
					nums = append(nums, strconv.Itoa(n))
				}
			}
			if len(nums) > 0 {
				deps = strings.Join(nums, ", ")
			}
		}
		name := oneLine(sub.Name)
		if name == "" {
			name = sub.ID
		}

		fileName := taskFileName(num, sub.ID)
		link := fmt.Sprintf("[details](%s/%s)", tasksDirName, fileName)
		if err := os.WriteFile(filepath.Join(tasksDir, fileName),
			[]byte(renderTaskFile(num, sub, deps)), 0o644); err != nil {
			return "", "", fmt.Errorf("analysis: write task file %s: %w", fileName, err)
		}

		// Budget the summary so the whole row fits the 200-rune window
		// resolveTaskSourceDescription hands to the UI — an overlong row would
		// drop the Details link, which is the one part a session cannot guess.
		fixed := fmt.Sprintf("| %d | **%s** --  | %s | %s |", num, name, link, deps)
		desc := rowSummary(sub, maxRowRunes-len([]rune(fixed)))
		rows[i] = fmt.Sprintf("| %d | **%s** -- %s | %s | %s |", num, name, desc, link, deps)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Roadmap -- %s\n\n", plan.Project)
	if sc := strings.TrimSpace(plan.SharedContext); sc != "" {
		b.WriteString(sc)
		b.WriteString("\n\n")
	}
	b.WriteString("Each task's full description and acceptance criteria live in the file\n")
	b.WriteString("linked from its Details column. The row alone is only for navigation.\n\n")
	b.WriteString("## Tasks\n\n")
	b.WriteString("| # | Task | Details | Depends on |\n")
	b.WriteString("|---|------|---------|------------|\n")
	for _, row := range rows {
		b.WriteString(row)
		b.WriteString("\n")
	}

	if err := os.WriteFile(roadmapPath, []byte(b.String()), 0o644); err != nil {
		return "", "", fmt.Errorf("analysis: write ROADMAP.md: %w", err)
	}

	written, err := os.ReadFile(roadmapPath)
	if err != nil {
		return "", "", fmt.Errorf("analysis: reread ROADMAP.md: %w", err)
	}
	rowToID := make(map[string]string, len(rows))
	for i, row := range rows {
		rowToID[row] = order[i]
	}
	lineOf := make(map[string]int, len(rows))
	for ln, text := range strings.Split(string(written), "\n") {
		text = strings.TrimRight(text, "\r")
		if id, ok := rowToID[text]; ok {
			lineOf[id] = ln + 1
		}
	}

	var sb strings.Builder
	sb.WriteString("# STATUS-P1\n\n")
	sb.WriteString("> Priority top to bottom. Delete a line when its task is fully done and\n")
	sb.WriteString("> committed. Do not reorder unless priorities actually change.\n\n")
	for _, id := range order {
		if ln, ok := lineOf[id]; ok {
			fmt.Fprintf(&sb, "%s:%d\n", roadmapFileName, ln)
		}
	}

	if err := os.WriteFile(statusPath, []byte(sb.String()), 0o644); err != nil {
		return "", "", fmt.Errorf("analysis: write STATUS-P1.md: %w", err)
	}

	return roadmapPath, statusPath, nil
}
