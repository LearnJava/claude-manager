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
   ROADMAP.md at the line number it gives and read that task's row - that is
   your task for this session.

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

// WriteRoadmapFiles renders plan's subtasks into <projectPath>/ROADMAP.md and
// <projectPath>/STATUS-P1.md, in the exact bare-pointer-line format that
// hasTasks/resolveTaskSourceDescription (internal/session/session.go) already
// consume — no changes are needed on that side.
//
// Line numbers in STATUS-P1.md are computed by re-reading the just-written
// ROADMAP.md rather than trusting the in-memory builder, so they are
// guaranteed to match what those functions will see on disk.
//
// Refuses to touch anything (returning ErrRoadmapFilesExist) if either file
// already exists and overwrite is false.
func WriteRoadmapFiles(projectPath string, plan *TaskPlan, overwrite bool) (roadmapPath, statusPath string, err error) {
	if plan == nil {
		return "", "", errors.New("analysis: nil plan")
	}
	roadmapPath = filepath.Join(projectPath, roadmapFileName)
	statusPath = filepath.Join(projectPath, statusFileName)

	if !overwrite {
		if _, statErr := os.Stat(roadmapPath); statErr == nil {
			return "", "", ErrRoadmapFilesExist
		}
		if _, statErr := os.Stat(statusPath); statErr == nil {
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

	rows := make([]string, len(order))
	for i, id := range order {
		sub := byID[id]
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
		desc := oneLine(sub.Prompt)
		rows[i] = fmt.Sprintf("| %d | **%s** -- %s | %s |", i+1, name, desc, deps)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Roadmap -- %s\n\n", plan.Project)
	if sc := strings.TrimSpace(plan.SharedContext); sc != "" {
		b.WriteString(sc)
		b.WriteString("\n\n")
	}
	b.WriteString("## Tasks\n\n")
	b.WriteString("| # | Task | Depends on |\n")
	b.WriteString("|---|------|------------|\n")
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
