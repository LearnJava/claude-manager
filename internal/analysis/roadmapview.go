package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// This file is the read side of the roadmap: it turns the ROADMAP.md /
// STATUS-P1.md / tasks/ trio that WriteRoadmapFiles produces back into a
// structure the UI can render as a tree (TaskPanel's "Roadmap" tab).
//
// It is deliberately tolerant: a hand-written roadmap that predates
// WriteRoadmapFiles (no Details column, arbitrary extra columns, no shared
// context) still parses — the same "never break an existing project's
// roadmap" rule the writer follows.

// Task statuses reported to the UI. "Done" is inferred, not stored: a task is
// done exactly when its pointer line is gone from the status file, which is
// the same signal hasTasks() uses to decide whether work remains.
const (
	RoadmapTaskDone    = "done"
	RoadmapTaskCurrent = "current"
	RoadmapTaskPending = "pending"
)

// RoadmapTask is one row of the roadmap table.
type RoadmapTask struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Summary    string `json:"summary"`
	DetailPath string `json:"detail_path"` // relative to the project root; "" for legacy rows
	DependsOn  string `json:"depends_on"`
	Line       int    `json:"line"` // 1-based line in the roadmap file
	Status     string `json:"status"`
}

// RoadmapView is the whole roadmap as the UI needs it.
type RoadmapView struct {
	Title       string        `json:"title"`
	RoadmapFile string        `json:"roadmap_file"` // relative to the project root
	StatusFile  string        `json:"status_file"`
	Context     string        `json:"context"` // shared_context paragraph(s), if any
	Tasks       []RoadmapTask `json:"tasks"`
	Done        int           `json:"done"`
	Total       int           `json:"total"`
}

var (
	// Mirrors taskPointerPattern in internal/session/session.go — a bare
	// "<source>:NN" line. Kept in sync by
	// TestPointerParsingMatchesSessionPackage (internal/session).
	pointerLineRe = regexp.MustCompile(`^(\S+):(\d+)$`)
	// "**Name** -- summary" — the shape the writer emits for the Task cell.
	taskCellRe = regexp.MustCompile(`^\*\*(.+?)\*\*\s*(?:--\s*(.*))?$`)
	// A markdown link, used to spot the Details cell.
	mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
)

// ErrNoRoadmap is returned when the session's task source has no pointer lines
// referring to a readable roadmap file — a legacy "In progress:"/"Next:" task
// file, an empty backlog, or a project that simply has no roadmap.
var ErrNoRoadmap = errors.New("analysis: no roadmap for this task source")

// ReadRoadmap resolves taskSource (relative to projectPath unless absolute),
// finds the roadmap file its pointers refer to, and returns the whole roadmap
// with per-task status. Returns ErrNoRoadmap when there is nothing to show.
//
// Note the asymmetry with hasTasks: an *empty* backlog is not "no roadmap".
// The status file keeps its heading after the last pointer is deleted, so the
// roadmap is still rendered — every task shown as done, which is exactly what
// a finished project should look like.
func ReadRoadmap(projectPath, taskSource string) (*RoadmapView, error) {
	if strings.TrimSpace(taskSource) == "" {
		return nil, ErrNoRoadmap
	}
	statusPath := taskSource
	if !filepath.IsAbs(statusPath) {
		statusPath = filepath.Join(projectPath, statusPath)
	}
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, ErrNoRoadmap
	}

	roadmapRel, openLines := parsePointers(string(statusData))
	if roadmapRel == "" {
		// No pointer line names a roadmap file. Either a legacy-format task
		// file, or an emptied status file — fall back to the conventional
		// name so a finished project still renders its (all-done) roadmap.
		roadmapRel = roadmapFileName
	}
	roadmapPath := roadmapRel
	if !filepath.IsAbs(roadmapPath) {
		roadmapPath = filepath.Join(projectPath, roadmapPath)
	}
	roadmapData, err := os.ReadFile(roadmapPath)
	if err != nil {
		return nil, ErrNoRoadmap
	}

	view := parseRoadmap(string(roadmapData), openLines)
	if len(view.Tasks) == 0 {
		return nil, ErrNoRoadmap
	}
	view.RoadmapFile = roadmapRel
	view.StatusFile = taskSource
	return view, nil
}

// parsePointers extracts the roadmap file every pointer refers to plus the set
// of still-open line numbers. The first pointer wins for the file name (the
// same "first pointer is the current task" rule Run() follows); pointers into
// a different file are ignored rather than mixed in.
func parsePointers(status string) (roadmapRel string, open map[int]bool) {
	open = map[int]bool{}
	for _, line := range strings.Split(status, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ">") ||
			strings.HasPrefix(s, "-") || strings.HasPrefix(s, "_") || strings.HasPrefix(s, "*") {
			continue
		}
		m := pointerLineRe.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		if roadmapRel == "" {
			roadmapRel = m[1]
		}
		if m[1] != roadmapRel {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil && n > 0 {
			open[n] = true
		}
	}
	return roadmapRel, open
}

// parseRoadmap walks the markdown, collecting the title, the leading prose
// (shared context) and every table row that starts with a task number.
func parseRoadmap(content string, open map[int]bool) *RoadmapView {
	view := &RoadmapView{}
	var context []string
	inTable := false
	firstOpen := 0

	for i, raw := range strings.Split(content, "\n") {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "# ") && view.Title == "":
			view.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		case strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "|"):
			inTable = true
			task, ok := parseTaskRow(trimmed)
			if !ok {
				continue
			}
			task.Line = lineNo
			if open[lineNo] {
				task.Status = RoadmapTaskPending
				if firstOpen == 0 || lineNo < firstOpen {
					firstOpen = lineNo
				}
			} else {
				task.Status = RoadmapTaskDone
				view.Done++
			}
			view.Tasks = append(view.Tasks, task)
			continue
		}

		if !inTable && trimmed != "" {
			context = append(context, trimmed)
		}
	}

	// The first still-open pointer is what the session picks up next.
	for i := range view.Tasks {
		if view.Tasks[i].Line == firstOpen {
			view.Tasks[i].Status = RoadmapTaskCurrent
			break
		}
	}
	view.Total = len(view.Tasks)
	view.Context = strings.Join(context, " ")
	return view
}

// parseTaskRow parses one markdown table row into a task, or reports false for
// the header/separator rows. Column count is not fixed: the Details column
// only exists in roadmaps written after the per-task-file split, and a
// hand-written roadmap may have neither it nor the dependency column.
func parseTaskRow(row string) (RoadmapTask, bool) {
	cells := splitRow(row)
	if len(cells) < 2 {
		return RoadmapTask{}, false
	}
	num, err := strconv.Atoi(cells[0])
	if err != nil || num < 1 {
		return RoadmapTask{}, false // header ("#") or separator ("---")
	}

	task := RoadmapTask{Number: num}
	if m := taskCellRe.FindStringSubmatch(cells[1]); m != nil {
		task.Name = strings.TrimSpace(m[1])
		task.Summary = strings.TrimSpace(m[2])
	} else {
		task.Summary = cells[1]
	}

	// Remaining cells: a markdown link is the Details column, anything else is
	// taken as the dependency column. Position-independent so an extra column
	// in a hand-written roadmap doesn't shift everything.
	for _, cell := range cells[2:] {
		if m := mdLinkRe.FindStringSubmatch(cell); m != nil && task.DetailPath == "" {
			task.DetailPath = strings.TrimSpace(m[1])
			continue
		}
		if task.DependsOn == "" && cell != "" {
			task.DependsOn = cell
		}
	}
	return task, true
}

// splitRow splits a markdown table row into trimmed cells, dropping the empty
// leading/trailing cells the pipe delimiters produce.
func splitRow(row string) []string {
	parts := strings.Split(strings.TrimSpace(row), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// ReadRoadmapTaskDetail returns the body of a task's detail file. relPath is
// whatever the roadmap row linked to, so it is confined to projectPath before
// use — a roadmap file is editable by anyone with repo access, and a crafted
// "../../.ssh/id_rsa" link must not turn the UI into a file reader.
func ReadRoadmapTaskDetail(projectPath, relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", errors.New("analysis: empty task detail path")
	}
	root, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("analysis: task detail path escapes the project: %s", relPath)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
