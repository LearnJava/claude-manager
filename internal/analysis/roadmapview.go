package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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
//   - A bug tracker (lumen-browser's BUGS.md, which is what STATUS-P1.md's
//     pointers actually address): `ID | Статус | Компонент | Описание`, with
//     the id cell carrying the markdown link to the bug's file and the status
//     written as "FIXED <date>" / "WONTFIX (Phase N+)". Same curated model.
//
// Columns are located by header name, not by position, so neither layout is
// hard-coded and a project can add columns without breaking the panel; header
// names are matched in English and Russian, because failing to recognise a
// status column is not cosmetic — it silently reverts the file to the pointer
// model and reports every open row as done.

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
	// StatusModelMixed is reported when a status file points into several
	// roadmaps that don't agree on a model — the per-source nodes still each
	// use their own, this is only what the header badge says.
	StatusModelMixed = "mixed"
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
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Number     int    `json:"number"` // generated roadmaps only; 0 otherwise
	Name       string `json:"name"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`     // normalized: done|active|blocked|pending
	StatusRaw  string `json:"status_raw"` // as written: planned, opt, ready, wait…
	Current    bool   `json:"current"`    // the first pointer in the status file
	InQueue    bool   `json:"in_queue"`   // any pointer in the status file
	DetailPath string `json:"detail_path"`
	DependsOn  string `json:"depends_on"`
	Bugs       string `json:"bugs"`
	Size       string `json:"size"`
	Line       int    `json:"line"` // 1-based line in the roadmap file
	// File this node was read from, relative to the project root. Set on every
	// node because a status file may point into several roadmaps at once, and
	// the UI needs to know which one to ask for a row's long-form text.
	RoadmapFile string         `json:"roadmap_file"`
	HasDetail   bool           `json:"has_detail"`
	Children    []*RoadmapNode `json:"children,omitempty"`
	Done        int            `json:"done"`  // including descendants
	Total       int            `json:"total"` // including descendants

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
	// [text](target) — group 1 is the label, group 2 the link target.
	mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	// A table separator row: only dashes, colons and pipes.
	separatorRowRe = regexp.MustCompile(`^[\s|:-]+$`)
)

// ErrNoRoadmap is returned when there is no roadmap to show for this task
// source — no readable roadmap file, or a file with no task rows in it.
var ErrNoRoadmap = errors.New("analysis: no roadmap for this task source")

// ReadRoadmap resolves taskSource (relative to projectPath unless absolute),
// finds the roadmap file(s) its pointers refer to, and returns them as one
// tree with per-node status. Returns ErrNoRoadmap when there is nothing to
// show.
//
// A status file may point into several roadmaps at once — lumen-browser's
// STATUS-P1.md queues nineteen BUGS.md rows and one ROADMAP.md task — so every
// referenced file that parses into rows becomes a source, and each gets a
// group node of its own when there is more than one. Showing only the first
// pointer's file hid queued work entirely.
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

	pointers := parsePointers(string(statusData))
	if len(pointers) == 0 {
		// No pointer names a roadmap file: a legacy "In progress:"/"Next:"
		// task file, or an emptied status file. Fall back to the conventional
		// name so those projects still get a tree (without a "current" mark).
		pointers = []filePointers{{file: roadmapFileName, open: map[int]bool{}}}
	}

	var views []*RoadmapView
	for _, p := range pointers {
		roadmapPath := p.file
		if !filepath.IsAbs(roadmapPath) {
			roadmapPath = filepath.Join(projectPath, roadmapPath)
		}
		data, err := os.ReadFile(roadmapPath)
		if err != nil {
			continue // a pointer into a file we can't read is not a roadmap
		}
		v := parseRoadmap(string(data), p.open, p.first)
		if v.Total == 0 {
			continue // a source file, or a document with no task table
		}
		v.RoadmapFile = p.file
		for _, n := range flattenNodes(v.Nodes) {
			n.RoadmapFile = p.file
		}
		views = append(views, v)
	}
	if len(views) == 0 {
		return nil, ErrNoRoadmap
	}

	view := mergeViews(views)
	view.StatusFile = taskSource
	return view, nil
}

// filePointers is one roadmap file referenced by the status file: the lines
// pointed at, and the first of them when this file holds the very first
// pointer overall (the row the session takes next).
type filePointers struct {
	file  string
	open  map[int]bool
	first int
}

// parsePointers groups the status file's pointer lines by the file they
// address, in order of first appearance. Only the first pointer overall marks
// a row "current" — that is the one the session picks up next; the rest are
// queue.
func parsePointers(status string) []filePointers {
	var out []filePointers
	index := map[string]int{}
	firstSeen := false
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
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			continue
		}
		i, ok := index[m[1]]
		if !ok {
			i = len(out)
			index[m[1]] = i
			out = append(out, filePointers{file: m[1], open: map[int]bool{}})
		}
		if !firstSeen {
			out[i].first = n
			firstSeen = true
		}
		out[i].open[n] = true
	}
	return out
}

// mergeViews folds per-file roadmaps into the single tree the UI renders. With
// one source it *is* that view; with several, each becomes a collapsible group
// node named after its file, so a queue spanning BUGS.md and ROADMAP.md shows
// both instead of silently dropping one.
func mergeViews(views []*RoadmapView) *RoadmapView {
	if len(views) == 1 {
		return views[0]
	}
	merged := &RoadmapView{
		Title:       views[0].Title,
		RoadmapFile: views[0].RoadmapFile,
		Context:     views[0].Context,
		StatusModel: views[0].StatusModel,
	}
	for _, v := range views {
		if v.StatusModel != merged.StatusModel {
			merged.StatusModel = StatusModelMixed
		}
		group := &RoadmapNode{
			Kind:        RoadmapNodePhase,
			ID:          v.RoadmapFile,
			Name:        v.RoadmapFile,
			Summary:     v.Title,
			RoadmapFile: v.RoadmapFile,
			Children:    v.Nodes,
			Done:        v.Done,
			Total:       v.Total,
		}
		merged.Nodes = append(merged.Nodes, group)
		merged.Done += v.Done
		merged.Total += v.Total
	}
	return merged
}

// flattenNodes returns every node of the tree, depth-first.
func flattenNodes(nodes []*RoadmapNode) []*RoadmapNode {
	out := make([]*RoadmapNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
		out = append(out, flattenNodes(n.Children)...)
	}
	return out
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
			// A *blank* line does not end the table: hand-maintained trackers
			// break a long table into visual chunks (lumen-browser's BUGS.md
			// has one at row 366 of 435), and forgetting the header there
			// silently dropped every row below it. Only real content ends a
			// table. `inTable` is still cleared, so a genuinely new table
			// separated by one blank line is re-detected from its own header.
			if trimmed != "" {
				header = nil
			}
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
//
// Russian column names are recognised alongside the English ones: the roadmaps
// this app *generates* are English, but the ones it *reads* are whatever the
// project keeps (lumen-browser's BUGS.md is `ID | Статус | Компонент |
// Описание`), and an unrecognised status column is not a cosmetic loss — it
// drops the roadmap back to the pointer model, where every row without a
// pointer reads as done.
func normalizeHeader(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		name := strings.ToLower(strings.TrimSpace(c))
		name = strings.Trim(name, "*_ ")
		switch name {
		case "#", "№", "no", "num":
			name = "number"
		case "ид":
			name = "id"
		case "depends on", "depends_on", "deps", "after", "зависит от", "зависимости":
			name = "depends"
		case "detail", "details", "link", "file", "детали", "файл", "ссылка":
			name = "details"
		case "note", "notes", "description", "desc", "описание", "примечание", "комментарий":
			name = "note"
		case "state", "статус", "состояние":
			name = "status"
		case "phase", "фаза":
			name = "phase"
		case "parent", "родитель":
			name = "parent"
		case "size", "размер", "оценка":
			name = "size"
		case "bugs", "баги":
			name = "bugs"
		case "name", "title", "task", "subject", "название", "задача", "заголовок":
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
	open, firstOpen = remapPointers(content, rows, open, firstOpen)
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

	id := plainCell(row.get("id"))
	titleCell := plainCell(row.get("title"))
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
	// The detail file is usually linked from a dedicated column; a bug tracker
	// links it from the id cell itself ("[BUG-349](bugs/BUG-349-OPEN.md)").
	for _, cell := range []string{row.get("details"), row.get("id"), row.get("title")} {
		if node.DetailPath = linkTarget(cell); node.DetailPath != "" {
			break
		}
	}

	// The task cell is either "**Name** -- summary" (generated) or a plain
	// title (curated).
	if m := taskCellRe.FindStringSubmatch(titleCell); m != nil {
		node.Name = strings.TrimSpace(m[1])
		node.Summary = strings.TrimSpace(m[2])
	} else {
		node.Name = titleCell
	}
	note := strings.TrimSpace(row.get("note"))
	if node.Name == "" && node.Summary == "" {
		node.Name = node.ID
		// A table keyed only by id ("BUG-349") says nothing on its own, so the
		// description column doubles as the summary — truncated, since the
		// full text is what the "+" expander fetches per row.
		node.Summary = shortSummary(note)
	}
	if node.Name == "" && node.ID == "" {
		return nil, "" // an empty or malformed row
	}
	node.HasDetail = node.DetailPath != "" || note != ""

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
		if target := linkTarget(cell); target != "" && node.DetailPath == "" {
			node.DetailPath = target
			continue
		}
		if node.DependsOn == "" && cell != "" {
			node.DependsOn = cell
		}
	}
	node.HasDetail = node.DetailPath != ""
	return node, RoadmapNodeTask
}

// plainCell strips markdown link syntax from a cell, keeping the link text —
// a bug tracker writes its id as "[BUG-349](bugs/BUG-349-OPEN.md)" and the
// panel must show "BUG-349", not the raw markdown.
func plainCell(cell string) string {
	return strings.TrimSpace(mdLinkRe.ReplaceAllString(cell, "$1"))
}

// linkTarget returns the first markdown link target in a cell, if any.
func linkTarget(cell string) string {
	if m := mdLinkRe.FindStringSubmatch(cell); m != nil {
		return strings.TrimSpace(m[2])
	}
	return ""
}

// maxSummaryRunes bounds the inline summary taken from a description column:
// a curated tracker's descriptions run to kilobytes each and the tree payload
// carries one node per row (435 of them in lumen-browser's BUGS.md).
const maxSummaryRunes = 120

func shortSummary(s string) string {
	s = strings.TrimSpace(plainCell(s))
	r := []rune(s)
	if len(r) <= maxSummaryRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxSummaryRunes])) + "…"
}

// statusWord reduces a status cell to its leading keyword. Curated trackers
// write the status with its date or scope attached — "FIXED 2026-05-15",
// "WONTFIX (Phase 1+)" — and matching those literally left them unrecognised,
// i.e. silently pending.
func statusWord(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.Trim(s, "*_`")
	s = strings.TrimSpace(trimStatusGlyphs(s))
	for _, multi := range []string{"in progress", "in-progress", "in_progress", "в работе", "не начат"} {
		if strings.HasPrefix(s, multi) {
			return "in progress"
		}
	}
	if i := strings.IndexAny(s, " \t(,;:"); i > 0 {
		s = s[:i]
	}
	return s
}

// trimStatusGlyphs drops the leading marker a hand-maintained status column
// puts in front of the word — "✓ DONE", "○ TODO", "● IN PROGRESS", "[x] done".
// Without this the keyword extracted below is the glyph itself, every row
// falls through to "pending", and a finished roadmap reads 0/N done.
func trimStatusGlyphs(s string) string {
	// A checkbox is a marker too, but its own letter ("[x]") would survive the
	// rune trim below and become the keyword.
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 && i <= 3 {
			s = s[i+1:]
		}
	}
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// resolveStatus maps a row onto a normalized status.
func resolveStatus(node *RoadmapNode, model string, open map[int]bool) string {
	if model == StatusModelCurated {
		switch statusWord(node.StatusRaw) {
		case "done", "fixed", "closed", "complete", "completed", "готово", "закрыт", "исправлено":
			return RoadmapTaskDone
		case "active", "inprogress", "in progress", "wip", "работа":
			return RoadmapTaskActive
		case "blocker", "blocked", "wait", "waiting", "wontfix", "заблокирован":
			// wontfix is not done: these rows are deferred ("WONTFIX (Phase
			// N+)"), and counting them as finished would inflate progress.
			return RoadmapTaskBlocked
		default:
			// planned / queued / ready / opt / unset
			//
			// Only the task the session actually took reads as active. Every
			// other pointer is a queue entry, and a status file may well hold
			// the whole backlog (LEARN-STATUS.md queues all 18 open tasks) —
			// treating each as active would paint the entire roadmap
			// in-progress. The UI marks the rest as queued from InQueue.
			if node.Current {
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

// ---- pointer remapping ----

// pointerIDRe pulls the task id out of a pointed-at line that is not a table
// row: "## LN-02: Сигнатуры…" → "LN-02", "### BUG-349 …" → "BUG-349".
var pointerIDRe = regexp.MustCompile(`^[#>\-*\s\x60]*([A-Za-zА-Яа-я]+[-_]?\d+(?:\.\d+)*)`)

// remapPointers resolves pointer lines that do not land on a table row.
//
// A generated roadmap's queue points straight at ROADMAP.md table rows, so the
// line number matches and nothing happens here. A curated breakdown
// (LEARN-TASKS.md, MIXED-TASKS.md) is the opposite shape: the queue points at
// the task's *heading* ("LEARN-TASKS.md:292" → "## LN-02: …") while the table
// at the top of the same file is the index. Without this the pointed-at line
// belongs to no row, nothing is marked "← now" or queued, and a session's
// current task is invisible in the panel.
//
// The mapping is by id: the leading token of the pointed-at line matched
// against each row's id (or the leading token of its name). An unmatched
// pointer is kept as-is — a pointer into a source file stays meaningless, as
// before.
func remapPointers(content string, rows []tableRow, open map[int]bool, first int) (map[int]bool, int) {
	if len(open) == 0 && first == 0 {
		return open, first
	}
	rowLines := make(map[int]bool, len(rows))
	byID := make(map[string]int, len(rows))
	for _, row := range rows {
		rowLines[row.line] = true
		node, _ := rowToNode(row)
		if node == nil {
			continue
		}
		for _, key := range []string{node.ID, node.Name} {
			if k := pointerKey(key); k != "" {
				if _, seen := byID[k]; !seen {
					byID[k] = row.line
				}
			}
		}
	}
	if len(byID) == 0 {
		return open, first
	}
	lines := strings.Split(content, "\n")
	resolve := func(line int) int {
		if line <= 0 || rowLines[line] || line > len(lines) {
			return line
		}
		m := pointerIDRe.FindStringSubmatch(strings.TrimSpace(lines[line-1]))
		if m == nil {
			return line
		}
		if row, ok := byID[pointerKey(m[1])]; ok {
			return row
		}
		return line
	}
	remapped := make(map[int]bool, len(open))
	for line := range open {
		remapped[resolve(line)] = true
	}
	return remapped, resolve(first)
}

// pointerKey normalizes an id for matching: case-insensitive, without the
// markdown emphasis and link syntax a cell may carry.
func pointerKey(s string) string {
	s = plainCell(s)
	if m := pointerIDRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return strings.ToUpper(strings.TrimSpace(s))
}
