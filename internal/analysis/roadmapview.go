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

// This file is the read side of the roadmap: it turns a project's ROADMAP.md
// (plus the session's STATUS-PN.md pointer file) into the tree the UI renders
// in TaskPanel's "Roadmap" tab.
//
// It handles two families of roadmap, because real projects have both:
//
//   - Generated (WriteRoadmapFiles): `| # | Task | Details | Depends on |`,
//     flat, and a task is done exactly when its pointer line is gone from the
//     status file — the same signal hasTasks() uses.
//   - Curated (hand-maintained, e.g. lumen-browser): `| id | phase | parent |
//     status | size | bugs | note | title |`, a real tree via phase/parent,
//     with a `status` column that a project's own generator keeps up to date
//     from bug trackers and spec inventories. There the status column is the
//     truth and the pointer file only says which row *this session* took —
//     inferring "done" from missing pointers would mark 568 of 570 tasks done,
//     since one session's queue is not the whole backlog.
//
// Columns are located by header name, not by position, so neither layout is
// hard-coded and a project can add columns without breaking the panel.

// Normalized task statuses reported to the UI.
const (
	RoadmapTaskDone    = "done"
	RoadmapTaskActive  = "active"
	RoadmapTaskBlocked = "blocked"
	RoadmapTaskPending = "pending"
)

// Status models: how a task's status is decided for this roadmap.
const (
	// StatusModelPointer derives status from the status file: no pointer means
	// done. Used for roadmaps without a status column.
	StatusModelPointer = "pointer"
	// StatusModelCurated reads the roadmap's own status column; pointers only
	// mark what the current session picked up.
	StatusModelCurated = "curated"
)

// Node kinds.
const (
	RoadmapNodePhase = "phase"
	RoadmapNodeTask  = "task"
)

// RoadmapNode is one node of the roadmap tree: a phase, a task, or a subtask
// (tasks nest arbitrarily deep via the `parent` column).
//
// Long-form text (the `note` column, or a tasks/NN-*.md file) is deliberately
// NOT included: a curated roadmap can carry kilobytes of closing notes per row
// and serializing all of them would mean a multi-megabyte payload per panel
// refresh. The UI fetches one node's text on expand.
type RoadmapNode struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Number     int            `json:"number"` // generated roadmaps only; 0 otherwise
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Status     string         `json:"status"`     // normalized: done|active|blocked|pending
	StatusRaw  string         `json:"status_raw"` // as written: planned, opt, ready, wait…
	Current    bool           `json:"current"`    // the first pointer in the status file
	InQueue    bool           `json:"in_queue"`   // any pointer in the status file
	DetailPath string         `json:"detail_path"`
	DependsOn  string         `json:"depends_on"`
	Bugs       string         `json:"bugs"`
	Size       string         `json:"size"`
	Line       int            `json:"line"` // 1-based line in the roadmap file
	HasDetail  bool           `json:"has_detail"`
	Children   []*RoadmapNode `json:"children,omitempty"`
	Done       int            `json:"done"`  // including descendants
	Total      int            `json:"total"` // including descendants

	// Wiring only, not serialized: the raw phase/parent cells buildTree uses.
	phase  string
	parent string
}

// RoadmapView is the whole roadmap as the UI needs it.
type RoadmapView struct {
	Title       string         `json:"title"`
	RoadmapFile string         `json:"roadmap_file"` // relative to the project root
	StatusFile  string         `json:"status_file"`
	Context     string         `json:"context"`
	StatusModel string         `json:"status_model"`
	Nodes       []*RoadmapNode `json:"nodes"` // roots: phases, or tasks when there are none
	Done        int            `json:"done"`
	Total       int            `json:"total"`
}

var (
	// Mirrors taskPointerPattern in internal/session/session.go — a bare
	// "<source>:NN" line. Kept in sync by
	// TestRoadmapView_AgreesWithPointerParsing (internal/session).
	pointerLineRe = regexp.MustCompile(`^(\S+):(\d+)$`)
	// "**Name** -- summary" — the Task cell WriteRoadmapFiles emits.
	taskCellRe = regexp.MustCompile(`^\*\*(.+?)\*\*\s*(?:--\s*(.*))?$`)
	mdLinkRe   = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	// A table separator row: only dashes, colons and pipes.
	separatorRowRe = regexp.MustCompile(`^[\s|:-]+$`)
)

// ErrNoRoadmap is returned when there is no roadmap to show for this task
// source — no readable roadmap file, or a file with no task rows in it.
var ErrNoRoadmap = errors.New("analysis: no roadmap for this task source")

// ReadRoadmap resolves taskSource (relative to projectPath unless absolute),
// finds the roadmap file its pointers refer to, and returns the roadmap as a
// tree with per-node status. Returns ErrNoRoadmap when there is nothing to
// show.
//
// An *empty* backlog is not "no roadmap": the status file keeps its heading
// after the last pointer is deleted, so a finished project still renders —
// every task done, which is what it should look like.
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

	roadmapRel, openLines, firstOpen := parsePointers(string(statusData))
	if roadmapRel == "" {
		// No pointer names a roadmap file: a legacy "In progress:"/"Next:"
		// task file, or an emptied status file. Fall back to the conventional
		// name so those projects still get a tree (without a "current" mark).
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

	view := parseRoadmap(string(roadmapData), openLines, firstOpen)
	if view.Total == 0 {
		return nil, ErrNoRoadmap
	}
	view.RoadmapFile = roadmapRel
	view.StatusFile = taskSource
	return view, nil
}

// parsePointers extracts the roadmap file the pointers refer to, the set of
// pointed-at line numbers, and the first one (the row this session takes
// next). The first pointer decides the file; pointers into other files
// (BUGS.md, a source file) are ignored rather than mixed in.
func parsePointers(status string) (roadmapRel string, open map[int]bool, firstOpen int) {
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
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			continue
		}
		if firstOpen == 0 {
			firstOpen = n
		}
		open[n] = true
	}
	return roadmapRel, open, firstOpen
}

// ---- table parsing ----

// tableRow is one parsed markdown table row: its cells keyed by (lowercased)
// header name when the table has a header, plus the raw cells for
// header-less tables.
type tableRow struct {
	cells  []string
	byName map[string]string
	line   int
}

func (r tableRow) get(name string) string { return r.byName[name] }

// parseTables walks the markdown and returns every table row, with header
// names resolved. Rows from all tables are returned together — a roadmap may
// legitimately have a phases table and a tasks table (lumen-browser does), and
// they are told apart later by which columns they carry.
func parseTables(content string) (rows []tableRow, title string, context []string) {
	lines := strings.Split(content, "\n")
	var header []string
	inTable := false

	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(raw)

		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			header = nil
			switch {
			case strings.HasPrefix(trimmed, "# ") && title == "":
				title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			case strings.HasPrefix(trimmed, "#"), trimmed == "":
				// headings and blank lines are not context
			default:
				if len(rows) == 0 {
					// Prose before the first table is the shared context.
					context = append(context, trimmed)
				}
			}
			continue
		}

		cells := splitRow(trimmed)
		if len(cells) == 0 {
			continue
		}
		// A separator row confirms the previous row was this table's header.
		if separatorRowRe.MatchString(trimmed) {
			if !inTable && i > 0 {
				header = normalizeHeader(splitRow(strings.TrimSpace(lines[i-1])))
				// The header row was appended as data; drop it.
				if len(rows) > 0 && rows[len(rows)-1].line == i {
					rows = rows[:len(rows)-1]
				}
			}
			inTable = true
			continue
		}

		row := tableRow{cells: cells, line: i + 1, byName: map[string]string{}}
		for j, name := range header {
			if name == "" || j >= len(cells) {
				continue
			}
			row.byName[name] = cells[j]
		}
		rows = append(rows, row)
	}
	return rows, title, context
}

// normalizeHeader lowercases header cells and maps synonyms onto the canonical
// column names this package understands.
func normalizeHeader(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		name := strings.ToLower(strings.TrimSpace(c))
		name = strings.Trim(name, "*_ ")
		switch name {
		case "#", "№", "no", "num":
			name = "number"
		case "depends on", "depends_on", "deps", "after":
			name = "depends"
		case "detail", "details", "link", "file":
			name = "details"
		case "note", "notes", "description", "desc":
			name = "note"
		case "name", "title", "task", "subject":
			// "task" is the generated format's "**Name** -- summary" cell;
			// "title" is the curated format's plain title. Both name the task,
			// so they share a slot — a table with both is not a thing.
			name = "title"
		}
		out[i] = name
	}
	return out
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

// ---- roadmap assembly ----

func parseRoadmap(content string, open map[int]bool, firstOpen int) *RoadmapView {
	rows, title, context := parseTables(content)
	view := &RoadmapView{Title: title, Context: strings.Join(context, " ")}

	var tasks []*RoadmapNode
	var phaseCandidates []*RoadmapNode
	curated := false

	for _, row := range rows {
		node, kind := rowToNode(row)
		if node == nil {
			continue
		}
		node.Current = node.Line == firstOpen
		node.InQueue = open[node.Line]
		if node.StatusRaw != "" {
			curated = true
		}
		if kind == RoadmapNodePhase {
			phaseCandidates = append(phaseCandidates, node)
			continue
		}
		tasks = append(tasks, node)
	}
	if len(tasks) == 0 {
		// A single table with no phase column: those rows are the tasks, not
		// phase headers — nothing references them, so there is no tree to
		// hang off them.
		tasks, phaseCandidates = phaseCandidates, nil
		for _, t := range tasks {
			t.Kind = RoadmapNodeTask
		}
	}
	if len(tasks) == 0 {
		return view
	}

	view.StatusModel = StatusModelPointer
	if curated {
		view.StatusModel = StatusModelCurated
	}
	for _, t := range tasks {
		t.Status = resolveStatus(t, view.StatusModel, open)
	}

	view.Nodes = buildTree(tasks, phaseCandidates)
	for _, n := range view.Nodes {
		rollUp(n)
		view.Done += n.Done
		view.Total += n.Total
	}
	return view
}

// rowToNode converts one table row into a node, reporting whether it looks
// like a phase (a row with an id that carries no phase/parent columns) or a
// task. Returns nil for header/separator leftovers and empty rows.
func rowToNode(row tableRow) (*RoadmapNode, string) {
	if len(row.byName) == 0 {
		return legacyRowToNode(row)
	}

	id := row.get("id")
	titleCell := row.get("title")
	numText := row.get("number")

	node := &RoadmapNode{
		Kind:      RoadmapNodeTask,
		ID:        id,
		Line:      row.line,
		StatusRaw: strings.ToLower(row.get("status")),
		DependsOn: row.get("depends"),
		Bugs:      row.get("bugs"),
		Size:      row.get("size"),
	}
	if n, err := strconv.Atoi(numText); err == nil && n > 0 {
		node.Number = n
		if node.ID == "" {
			node.ID = numText
		}
	} else if numText != "" && numText != "#" {
		return nil, "" // header row of a numbered table
	}
	if m := mdLinkRe.FindStringSubmatch(row.get("details")); m != nil {
		node.DetailPath = strings.TrimSpace(m[1])
	}

	// The task cell is either "**Name** -- summary" (generated) or a plain
	// title (curated).
	if m := taskCellRe.FindStringSubmatch(titleCell); m != nil {
		node.Name = strings.TrimSpace(m[1])
		node.Summary = strings.TrimSpace(m[2])
	} else {
		node.Name = titleCell
	}
	if node.Name == "" && node.Summary == "" {
		node.Name = node.ID
	}
	if node.Name == "" && node.ID == "" {
		return nil, "" // an empty or malformed row
	}
	node.HasDetail = node.DetailPath != "" || strings.TrimSpace(row.get("note")) != ""

	phase := row.get("phase")
	parent := row.get("parent")
	if phase == "" && parent == "" && id != "" && numText == "" {
		// No phase/parent columns at all → this table is a phase list (or a
		// flat id-keyed task list; buildTree decides by whether anything
		// references these ids).
		node.Kind = RoadmapNodePhase
		return node, RoadmapNodePhase
	}
	node.Kind = RoadmapNodeTask
	// Carried on the node so buildTree can wire it up without re-reading rows.
	node.phase, node.parent = phase, parent
	return node, RoadmapNodeTask
}

// legacyRowToNode handles a table with no header row at all — a hand-written
// roadmap. Falls back to the historical positional layout: number, task,
// [details], [depends on].
func legacyRowToNode(row tableRow) (*RoadmapNode, string) {
	if len(row.cells) < 2 {
		return nil, ""
	}
	num, err := strconv.Atoi(row.cells[0])
	if err != nil || num < 1 {
		return nil, ""
	}
	node := &RoadmapNode{Kind: RoadmapNodeTask, ID: row.cells[0], Number: num, Line: row.line}
	if m := taskCellRe.FindStringSubmatch(row.cells[1]); m != nil {
		node.Name = strings.TrimSpace(m[1])
		node.Summary = strings.TrimSpace(m[2])
	} else {
		node.Summary = row.cells[1]
	}
	for _, cell := range row.cells[2:] {
		if m := mdLinkRe.FindStringSubmatch(cell); m != nil && node.DetailPath == "" {
			node.DetailPath = strings.TrimSpace(m[1])
			continue
		}
		if node.DependsOn == "" && cell != "" {
			node.DependsOn = cell
		}
	}
	node.HasDetail = node.DetailPath != ""
	return node, RoadmapNodeTask
}

// resolveStatus maps a row onto a normalized status.
func resolveStatus(node *RoadmapNode, model string, open map[int]bool) string {
	if model == StatusModelCurated {
		switch node.StatusRaw {
		case "done", "fixed", "closed", "complete", "completed":
			return RoadmapTaskDone
		case "active", "inprogress", "in progress", "in_progress", "wip":
			return RoadmapTaskActive
		case "blocker", "blocked", "wait", "waiting":
			return RoadmapTaskBlocked
		default:
			// planned / queued / ready / opt / unset
			if node.InQueue {
				return RoadmapTaskActive
			}
			return RoadmapTaskPending
		}
	}
	// Pointer model: a task is done exactly when its pointer is gone.
	if !open[node.Line] {
		return RoadmapTaskDone
	}
	if node.Current {
		return RoadmapTaskActive
	}
	return RoadmapTaskPending
}

// buildTree wires tasks under their parents and phases, returning the roots.
// Phase candidates that nothing references are treated as ordinary top-level
// tasks, so a flat id-keyed roadmap doesn't sprout empty phase headers.
func buildTree(tasks, phaseCandidates []*RoadmapNode) []*RoadmapNode {
	byID := make(map[string]*RoadmapNode, len(tasks)+len(phaseCandidates))
	for _, t := range tasks {
		if t.ID != "" {
			byID[t.ID] = t
		}
	}
	phases := map[string]*RoadmapNode{}
	var roots []*RoadmapNode
	for _, p := range phaseCandidates {
		referenced := false
		for _, t := range tasks {
			if t.phase == p.ID {
				referenced = true
				break
			}
		}
		if !referenced {
			continue
		}
		p.Kind = RoadmapNodePhase
		phases[p.ID] = p
		roots = append(roots, p)
	}

	for _, t := range tasks {
		switch {
		case t.parent != "" && byID[t.parent] != nil && byID[t.parent] != t:
			parent := byID[t.parent]
			parent.Children = append(parent.Children, t)
		case t.phase != "" && phases[t.phase] != nil:
			phases[t.phase].Children = append(phases[t.phase].Children, t)
		default:
			roots = append(roots, t)
		}
	}
	return roots
}

// rollUp fills Done/Total for a node and its descendants. A phase counts only
// its tasks; a task counts itself plus everything nested under it, so a parent
// with three finished subtasks reads 3/4 rather than 0/1.
func rollUp(node *RoadmapNode) {
	done, total := 0, 0
	if node.Kind != RoadmapNodePhase {
		total = 1
		if node.Status == RoadmapTaskDone {
			done = 1
		}
	}
	for _, c := range node.Children {
		rollUp(c)
		done += c.Done
		total += c.Total
	}
	node.Done, node.Total = done, total
}

// ---- task detail (loaded on expand) ----

// ReadRoadmapTaskDetail returns the body of a task's detail file. relPath is
// whatever the roadmap row linked to, so it is confined to projectPath before
// use — a roadmap file is editable by anyone with repo access, and a crafted
// "../../.ssh/id_rsa" link must not turn the UI into a file reader.
func ReadRoadmapTaskDetail(projectPath, relPath string) (string, error) {
	full, err := confinedPath(projectPath, relPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadRoadmapRowDetail returns the long-form text of one roadmap row: its
// `note` column when the table has one, else the whole row. Used for curated
// roadmaps, which carry the task's description inline rather than in a file —
// fetched per row on expand, since those notes run to kilobytes each.
func ReadRoadmapRowDetail(projectPath, roadmapFile string, line int) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("analysis: invalid roadmap line %d", line)
	}
	full, err := confinedPath(projectPath, roadmapFile)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return "", fmt.Errorf("analysis: roadmap has no line %d", line)
	}
	rows, _, _ := parseTables(string(data))
	for _, row := range rows {
		if row.line != line {
			continue
		}
		if note := strings.TrimSpace(row.get("note")); note != "" {
			return note, nil
		}
		break
	}
	if row := strings.TrimSpace(lines[line-1]); row != "" {
		return row, nil
	}
	return "", fmt.Errorf("analysis: roadmap line %d is empty", line)
}

// confinedPath resolves rel inside root, refusing anything that escapes it.
func confinedPath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("analysis: empty path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, full)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("analysis: path escapes the project: %s", rel)
	}
	return full, nil
}
