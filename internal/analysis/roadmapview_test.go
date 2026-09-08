package analysis

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDemoRoadmap materializes testPlan() and returns the project dir.
func writeDemoRoadmap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, _, err := WriteRoadmapFiles(dir, testPlan(), false); err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}
	return dir
}

// flatten returns every node of the tree, depth-first, in display order.
func flatten(nodes []*RoadmapNode) []*RoadmapNode {
	var out []*RoadmapNode
	for _, n := range nodes {
		out = append(out, n)
		out = append(out, flatten(n.Children)...)
	}
	return out
}

func nodeByName(t *testing.T, view *RoadmapView, name string) *RoadmapNode {
	t.Helper()
	for _, n := range flatten(view.Nodes) {
		if n.Name == name || n.ID == name {
			return n
		}
	}
	t.Fatalf("no node named %q in the roadmap", name)
	return nil
}

// ---- generated (pointer-model) roadmaps ----

func TestReadRoadmap_RoundTripsTheWriter(t *testing.T) {
	dir := writeDemoRoadmap(t)

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Title != "Roadmap -- demo" {
		t.Errorf("title: %q", view.Title)
	}
	if view.StatusModel != StatusModelPointer {
		t.Errorf("status model: %q", view.StatusModel)
	}
	if view.Total != 3 || len(view.Nodes) != 3 {
		t.Fatalf("expected 3 top-level tasks, got %d nodes / total %d", len(view.Nodes), view.Total)
	}
	if view.Done != 0 {
		t.Errorf("nothing is done yet, got Done=%d", view.Done)
	}
	if !strings.Contains(view.Context, "Go + Wails desktop app.") {
		t.Errorf("shared context not picked up: %q", view.Context)
	}

	first := view.Nodes[0]
	if first.Number != 1 || first.Name != "project-setup" {
		t.Errorf("first task: %+v", first)
	}
	if first.Summary != "Scaffold the repo and wire the build" {
		t.Errorf("summary: %q", first.Summary)
	}
	if first.DetailPath != "tasks/01-setup.md" || !first.HasDetail {
		t.Errorf("detail: %q has_detail=%v", first.DetailPath, first.HasDetail)
	}
	if first.Status != RoadmapTaskActive || !first.Current {
		t.Errorf("first open task must be active+current, got %q current=%v", first.Status, first.Current)
	}
	if view.Nodes[1].Status != RoadmapTaskPending {
		t.Errorf("second task should be pending, got %q", view.Nodes[1].Status)
	}
	if view.Nodes[2].DependsOn != "1" {
		t.Errorf("depends_on: %q", view.Nodes[2].DependsOn)
	}
}

func TestReadRoadmap_CompletedTasksAreThoseWithoutPointers(t *testing.T) {
	dir := writeDemoRoadmap(t)
	statusPath := filepath.Join(dir, "STATUS-P1.md")

	// Simulate a session finishing task 1: it deletes that pointer line.
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	dropped := false
	for _, l := range strings.Split(string(data), "\n") {
		if !dropped && strings.HasPrefix(strings.TrimSpace(l), "ROADMAP.md:") {
			dropped = true
			continue
		}
		kept = append(kept, l)
	}
	if err := os.WriteFile(statusPath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Done != 1 {
		t.Errorf("Done: want 1, got %d", view.Done)
	}
	if view.Nodes[0].Status != RoadmapTaskDone {
		t.Errorf("task 1 should be done, got %q", view.Nodes[0].Status)
	}
	if view.Nodes[1].Status != RoadmapTaskActive || !view.Nodes[1].Current {
		t.Errorf("task 2 should now be the current one, got %q", view.Nodes[1].Status)
	}
}

func TestReadRoadmap_EmptyBacklogRendersEverythingDone(t *testing.T) {
	dir := writeDemoRoadmap(t)
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"),
		[]byte("# STATUS-P1\n\n> Priority top to bottom.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("an emptied backlog must still render the roadmap: %v", err)
	}
	if view.Done != view.Total || view.Total != 3 {
		t.Errorf("want all 3 done, got %d/%d", view.Done, view.Total)
	}
}

func TestReadRoadmap_PointersIntoAnotherFileAreIgnored(t *testing.T) {
	dir := writeDemoRoadmap(t)
	status, err := os.ReadFile(filepath.Join(dir, "STATUS-P1.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A stray pointer into a source file (the format allows any file) must not
	// mark a roadmap row as open.
	mixed := string(status) + "internal/foo/bar.go:13\n"
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Done != 0 || view.Total != 3 {
		t.Errorf("stray pointer changed the counts: %d/%d", view.Done, view.Total)
	}
}

// ---- curated roadmaps (lumen-browser layout) ----

// curatedRoadmap mirrors lumen-browser's ROADMAP.md: a phases table, then a
// tasks table with id/phase/parent/status/size/bugs/note/title. Line numbers
// matter — the pointer file addresses rows by line.
const curatedRoadmap = `# ROADMAP.md — task tree

Flat, grep-friendly structure source.

## Phases

| id | status | date | title |
|---|---|---|---|
| P0 | done | 2026-05 | Phase 0 — Prototype |
| P3 | planned | — | Phase 3 — Full Browser |

## Tasks

| id | phase | parent | status | size | bugs | note | title |
|---|---|---|---|---|---|---|---|
| P0-ws | P0 | | done | | | scaffolding note | Workspace + crates |
| CC | P3 | | active | L | | chrome track | CC: Engine-rendered chrome |
| CC-8 | P3 | CC | done | M | | closed 2026-07-25, details here | CC-8: Tab bar |
| CC-14 | P3 | CC | planned | M | BUG-341 | parity checklist | CC-14: Flip the default |
| CC-15 | P3 | CC | planned | S | | legacy removal | CC-15: Remove legacy chrome |
`

func writeCuratedRoadmap(t *testing.T, status string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(curatedRoadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadRoadmap_CuratedUsesItsOwnStatusColumn(t *testing.T) {
	// Two pointers out of five tasks. Under the pointer model the other three
	// would be reported done — which is exactly the bug this guards: one
	// session's queue is not the whole backlog.
	dir := writeCuratedRoadmap(t, "ROADMAP.md:19\nROADMAP.md:20\nBUGS.md:355\n")

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.StatusModel != StatusModelCurated {
		t.Fatalf("status model: %q", view.StatusModel)
	}
	if view.Total != 5 {
		t.Fatalf("want 5 tasks, got %d", view.Total)
	}
	if view.Done != 2 { // P0-ws and CC-8
		t.Errorf("Done: want 2, got %d", view.Done)
	}
	if got := nodeByName(t, view, "CC-14").Status; got != RoadmapTaskActive {
		t.Errorf("CC-14 is pointed at by the session, want active, got %q", got)
	}
	if got := nodeByName(t, view, "CC-15").Status; got != RoadmapTaskPending {
		// Queued, but not the task in hand: only the first pointer is active,
		// or a whole-backlog queue would paint every row in progress.
		t.Errorf("CC-15 is queued behind CC-14, want pending, got %q", got)
	}
	if got := nodeByName(t, view, "CC-8").Status; got != RoadmapTaskDone {
		t.Errorf("CC-8 is done in the status column, got %q", got)
	}
	cc14 := nodeByName(t, view, "CC-14")
	if !cc14.Current || cc14.Bugs != "BUG-341" || cc14.Size != "M" {
		t.Errorf("CC-14: %+v", cc14)
	}
	if !nodeByName(t, view, "CC-15").InQueue || nodeByName(t, view, "CC-15").Current {
		t.Error("CC-15 should be in queue but not current")
	}
}

func TestReadRoadmap_CuratedBuildsPhaseAndParentTree(t *testing.T) {
	dir := writeCuratedRoadmap(t, "ROADMAP.md:19\n")

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if len(view.Nodes) != 2 {
		t.Fatalf("want 2 phase roots, got %d", len(view.Nodes))
	}
	p0, p3 := view.Nodes[0], view.Nodes[1]
	if p0.Kind != RoadmapNodePhase || p0.ID != "P0" {
		t.Errorf("first root: %+v", p0)
	}
	if p0.Total != 1 || p0.Done != 1 {
		t.Errorf("P0 rollup: %d/%d", p0.Done, p0.Total)
	}
	if len(p3.Children) != 1 || p3.Children[0].ID != "CC" {
		t.Fatalf("P3 should hold the CC track, got %+v", p3.Children)
	}
	cc := p3.Children[0]
	if len(cc.Children) != 3 {
		t.Fatalf("CC should have 3 subtasks, got %d", len(cc.Children))
	}
	// CC itself is active and not done; one of its three children is.
	if cc.Done != 1 || cc.Total != 4 {
		t.Errorf("CC rollup: want 1/4, got %d/%d", cc.Done, cc.Total)
	}
	if view.Total != 5 {
		t.Errorf("view total: %d", view.Total)
	}
}

func TestReadRoadmap_CuratedWithoutPointersStillRenders(t *testing.T) {
	// STATUS-P4/P5 in lumen-browser are still the legacy "In progress:"/"Next:"
	// format with no pointer lines at all. The tree must still render — just
	// with nothing marked current.
	dir := writeCuratedRoadmap(t, "# STATUS-P4\n\n## In progress\n\n_(none)_\n\n## Next\n\n- audit\n")

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Total != 5 || view.Done != 2 {
		t.Errorf("counts: %d/%d", view.Done, view.Total)
	}
	for _, n := range flatten(view.Nodes) {
		if n.Current {
			t.Errorf("nothing should be current without pointers, got %q", n.ID)
		}
	}
}

// ---- bug-tracker roadmaps (lumen-browser's BUGS.md) ----

// bugTracker mirrors lumen-browser's BUGS.md, which STATUS-P1.md's pointers
// actually address: Russian headers, the id cell carrying the markdown link to
// the bug file, statuses written with their date/scope attached, and a blank
// line splitting the table part-way down.
const bugTracker = `# BUGS.md — bug tracker

Living list of known engine bugs.

## Bugs

| ID | Статус | Компонент | Описание |
|---|---|---|---|
| [BUG-001](bugs/BUG-001-FIXED.md) | FIXED 2026-05-15 | layout | display:none on inline elements not working |
| [BUG-002](bugs/BUG-002-OPEN.md) | OPEN | paint | broken <img> src shows no alt text |

| [BUG-003](bugs/BUG-003-OPEN.md) | OPEN | js | window.location assignment ignored |
| [BUG-004](bugs/BUG-004-WONTFIX.md) | WONTFIX (Phase 2+) | layout | CSS Masonry |
`

func TestReadRoadmap_BugTrackerWithLocalizedHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "BUGS.md"), []byte(bugTracker), 0o644); err != nil {
		t.Fatal(err)
	}
	// Line 12 is BUG-003 — below the blank line that used to truncate the table.
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("BUGS.md:12\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.StatusModel != StatusModelCurated {
		t.Fatalf("status model %q: a recognised «Статус» column must beat the pointer model", view.StatusModel)
	}
	if view.Total != 4 || view.Done != 1 {
		t.Fatalf("counts: %d/%d, want 1/4", view.Done, view.Total)
	}

	cases := map[string]struct {
		status  string
		current bool
		detail  string
		summary string
	}{
		"BUG-001": {RoadmapTaskDone, false, "bugs/BUG-001-FIXED.md", "display:none on inline elements not working"},
		"BUG-002": {RoadmapTaskPending, false, "bugs/BUG-002-OPEN.md", "broken <img> src shows no alt text"},
		"BUG-003": {RoadmapTaskActive, true, "bugs/BUG-003-OPEN.md", "window.location assignment ignored"},
		"BUG-004": {RoadmapTaskBlocked, false, "bugs/BUG-004-WONTFIX.md", "CSS Masonry"},
	}
	for id, want := range cases {
		node := nodeByName(t, view, id)
		if node.Name != id {
			t.Errorf("%s: name %q — the markdown link must not reach the UI", id, node.Name)
		}
		if node.Status != want.status {
			t.Errorf("%s: status %q, want %q (raw %q)", id, node.Status, want.status, node.StatusRaw)
		}
		if node.Current != want.current {
			t.Errorf("%s: current=%v, want %v", id, node.Current, want.current)
		}
		if node.DetailPath != want.detail {
			t.Errorf("%s: detail path %q, want %q", id, node.DetailPath, want.detail)
		}
		if node.Summary != want.summary {
			t.Errorf("%s: summary %q, want %q", id, node.Summary, want.summary)
		}
	}
}

func TestReadRoadmap_QueueSpanningTwoFiles(t *testing.T) {
	// lumen-browser's STATUS-P1.md queues nineteen BUGS.md rows and one
	// ROADMAP.md task. Showing only the first pointer's file hid the other.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "BUGS.md"), []byte(bugTracker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(curatedRoadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	status := "BUGS.md:12\nROADMAP.md:19\ninternal/foo/bar.go:13\n"
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	// One group per readable roadmap, in pointer order; the source-file pointer
	// contributes nothing.
	if len(view.Nodes) != 2 {
		t.Fatalf("got %d roots, want a group per file", len(view.Nodes))
	}
	if view.Nodes[0].Name != "BUGS.md" || view.Nodes[1].Name != "ROADMAP.md" {
		t.Errorf("groups: %q, %q", view.Nodes[0].Name, view.Nodes[1].Name)
	}
	// 4 bugs (1 fixed) + 5 curated tasks (2 done).
	if view.Total != 9 || view.Done != 3 {
		t.Errorf("counts: %d/%d, want 3/9", view.Done, view.Total)
	}
	if view.RoadmapFile != "BUGS.md" {
		t.Errorf("primary file %q", view.RoadmapFile)
	}

	// Every node knows which file it came from — the panel fetches row text
	// per file, and a BUGS.md line number means nothing in ROADMAP.md.
	for _, n := range flatten(view.Nodes) {
		want := "BUGS.md"
		if strings.HasPrefix(n.ID, "CC") || strings.HasPrefix(n.ID, "P") || n.Name == "ROADMAP.md" {
			want = "ROADMAP.md"
		}
		if n.RoadmapFile != want {
			t.Errorf("node %q: roadmap_file %q, want %q", n.ID, n.RoadmapFile, want)
		}
	}

	// Only the very first pointer is "current"; the ROADMAP.md row is queue.
	current := nodeByName(t, view, "BUG-003")
	if !current.Current {
		t.Error("the first pointer must be the current task")
	}
	cc14 := nodeByName(t, view, "CC-14")
	if cc14.Current || !cc14.InQueue {
		t.Errorf("CC-14: current=%v in_queue=%v, want queued only", cc14.Current, cc14.InQueue)
	}
	if cc14.Status != RoadmapTaskPending {
		t.Errorf("CC-14 status %q — queued behind the current task, want pending", cc14.Status)
	}
}

func TestParseTables_BlankLineDoesNotMergeTwoTables(t *testing.T) {
	// Keeping the header across a blank line must not glue a following table
	// onto the previous one's columns: its own separator row re-detects it.
	content := "# R\n\n| id | status | title |\n|---|---|---|\n| A | done | first |\n\n" +
		"| id | status | note | title |\n|---|---|---|---|\n| B | planned | n | second |\n"
	rows, _, _ := parseTables(content)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[1].get("title") != "second" || rows[1].get("note") != "n" {
		t.Errorf("second table used the first table's header: %+v", rows[1].byName)
	}
}

func TestShortSummary_Truncates(t *testing.T) {
	long := strings.Repeat("я", maxSummaryRunes+40)
	got := shortSummary(long)
	if r := []rune(got); len(r) != maxSummaryRunes+1 || r[len(r)-1] != '…' {
		t.Errorf("truncated to %d runes: %q", len([]rune(got)), got)
	}
	if got := shortSummary("[BUG-9](bugs/BUG-9.md) short"); got != "BUG-9 short" {
		t.Errorf("links must be flattened: %q", got)
	}
}

func TestReadRoadmap_UnreferencedPhaseTableBecomesTasks(t *testing.T) {
	// A single id-keyed table with no phase column: those rows are the tasks,
	// not phase headers.
	dir := t.TempDir()
	roadmap := "# Roadmap\n\n| id | status | title |\n|---|---|---|\n" +
		"| a | done | First |\n| b | planned | Second |\n"
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if len(view.Nodes) != 2 || view.Total != 2 {
		t.Fatalf("want 2 task roots, got %d nodes / total %d", len(view.Nodes), view.Total)
	}
	for _, n := range view.Nodes {
		if n.Kind != RoadmapNodeTask {
			t.Errorf("node %q should be a task, got kind %q", n.ID, n.Kind)
		}
	}
}

// ---- legacy / hand-written roadmaps ----

func TestReadRoadmap_LegacyThreeColumnRoadmap(t *testing.T) {
	dir := t.TempDir()
	roadmap := "# Roadmap -- legacy\n\n## Tasks\n\n" +
		"| # | Task | Depends on |\n|---|------|------------|\n" +
		"| 1 | **Scaffolding** -- Bootstrap the module and add a Makefile. | - |\n" +
		"| 2 | **Storage** -- Port the SQLite layer. | 1 |\n"
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Total != 2 {
		t.Fatalf("want 2 tasks, got %d", view.Total)
	}
	if view.Nodes[0].DetailPath != "" || view.Nodes[0].HasDetail {
		t.Errorf("legacy rows have no detail file: %+v", view.Nodes[0])
	}
	if view.Nodes[0].Name != "Scaffolding" || !strings.Contains(view.Nodes[0].Summary, "Bootstrap the module") {
		t.Errorf("legacy row parsed as %+v", view.Nodes[0])
	}
	if view.Nodes[0].Status != RoadmapTaskDone || view.Nodes[1].Status != RoadmapTaskActive {
		t.Errorf("statuses: %q, %q", view.Nodes[0].Status, view.Nodes[1].Status)
	}
}

func TestReadRoadmap_HeaderlessTableFallsBackToPositions(t *testing.T) {
	dir := t.TempDir()
	roadmap := "# Roadmap\n\n| 1 | **First** -- do a thing | - |\n| 2 | **Second** -- do another | 1 |\n"
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Total != 2 || view.Nodes[0].Name != "First" {
		t.Errorf("got %d tasks, first %+v", view.Total, view.Nodes[0])
	}
}

func TestReadRoadmap_NoTaskSourceOrMissingFiles(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, source string }{
		{"empty task source", ""},
		{"missing status file", "STATUS-P1.md"},
	} {
		if _, err := ReadRoadmap(dir, tc.source); !errors.Is(err, ErrNoRoadmap) {
			t.Errorf("%s: want ErrNoRoadmap, got %v", tc.name, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoadmap(dir, "STATUS-P1.md"); !errors.Is(err, ErrNoRoadmap) {
		t.Errorf("missing roadmap file: want ErrNoRoadmap, got %v", err)
	}
}

func TestReadRoadmap_ProseOnlyFileIsNotARoadmap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"),
		[]byte("# Roadmap\n\nWe will build things. Someday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoadmap(dir, "STATUS-P1.md"); !errors.Is(err, ErrNoRoadmap) {
		t.Errorf("want ErrNoRoadmap, got %v", err)
	}
}

// ---- helpers ----

func TestNormalizeHeader(t *testing.T) {
	got := normalizeHeader([]string{"#", "Task", "Details", "Depends on", "Note", "**Bugs**",
		"Статус", "Описание", "Название", "Фаза"})
	want := []string{"number", "title", "details", "depends", "note", "bugs",
		"status", "note", "title", "phase"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitRow(t *testing.T) {
	got := splitRow("| a | b |  | c |")
	want := []string{"a", "b", "", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d: %q != %q", i, got[i], want[i])
		}
	}
}

func TestResolveStatus_CuratedVocabulary(t *testing.T) {
	cases := map[string]string{
		"done": RoadmapTaskDone, "fixed": RoadmapTaskDone,
		"active": RoadmapTaskActive, "inprogress": RoadmapTaskActive,
		"blocker": RoadmapTaskBlocked, "wait": RoadmapTaskBlocked,
		// Statuses as trackers actually write them: with a date or a scope.
		"fixed 2026-05-15": RoadmapTaskDone, "closed (P1)": RoadmapTaskDone,
		"in progress": RoadmapTaskActive, "wontfix (phase 2+)": RoadmapTaskBlocked,
		"open":    RoadmapTaskPending,
		"planned": RoadmapTaskPending, "queued": RoadmapTaskPending,
		"ready": RoadmapTaskPending, "opt": RoadmapTaskPending, "": RoadmapTaskPending,
	}
	for raw, want := range cases {
		node := &RoadmapNode{StatusRaw: raw, Line: 5}
		if got := resolveStatus(node, StatusModelCurated, map[int]bool{}); got != want {
			t.Errorf("status %q → %q, want %q", raw, got, want)
		}
	}
	// A planned task the session actually took reads as active; one merely
	// queued behind it stays pending.
	node := &RoadmapNode{StatusRaw: "planned", Line: 5, InQueue: true, Current: true}
	if got := resolveStatus(node, StatusModelCurated, map[int]bool{5: true}); got != RoadmapTaskActive {
		t.Errorf("current planned task → %q", got)
	}
	queued := &RoadmapNode{StatusRaw: "planned", Line: 6, InQueue: true}
	if got := resolveStatus(queued, StatusModelCurated, map[int]bool{5: true, 6: true}); got != RoadmapTaskPending {
		t.Errorf("queued planned task → %q, want pending", got)
	}
}

// ---- detail loading ----

func TestReadRoadmapTaskDetail(t *testing.T) {
	dir := writeDemoRoadmap(t)
	body, err := ReadRoadmapTaskDetail(dir, "tasks/01-setup.md")
	if err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if !strings.Contains(body, "# 1. project-setup") {
		t.Errorf("unexpected body:\n%s", body)
	}
}

func TestReadRoadmapRowDetail_ReturnsTheNoteColumn(t *testing.T) {
	dir := writeCuratedRoadmap(t, "ROADMAP.md:19\n")
	// Line 19 is the CC-14 row.
	body, err := ReadRoadmapRowDetail(dir, "ROADMAP.md", 19)
	if err != nil {
		t.Fatalf("ReadRoadmapRowDetail: %v", err)
	}
	if body != "parity checklist" {
		t.Errorf("want the note column, got %q", body)
	}
}

func TestReadRoadmapRowDetail_FallsBackToTheWholeRow(t *testing.T) {
	dir := t.TempDir()
	roadmap := "# Roadmap\n\n| # | Task | Depends on |\n|---|---|---|\n| 1 | **A** -- do it | - |\n"
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := ReadRoadmapRowDetail(dir, "ROADMAP.md", 5)
	if err != nil {
		t.Fatalf("ReadRoadmapRowDetail: %v", err)
	}
	if !strings.Contains(body, "**A** -- do it") {
		t.Errorf("want the raw row, got %q", body)
	}
}

func TestReadRoadmapRowDetail_Bounds(t *testing.T) {
	dir := writeCuratedRoadmap(t, "ROADMAP.md:19\n")
	if _, err := ReadRoadmapRowDetail(dir, "ROADMAP.md", 0); err == nil {
		t.Error("line 0 should be rejected")
	}
	if _, err := ReadRoadmapRowDetail(dir, "ROADMAP.md", 9999); err == nil {
		t.Error("out-of-range line should be rejected")
	}
}

func TestConfinedPath_RefusesToEscapeTheProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"../secret.txt", "..\\secret.txt", "tasks/../../secret.txt", ""} {
		if _, err := ReadRoadmapTaskDetail(project, bad); err == nil {
			t.Errorf("path %q should have been rejected", bad)
		}
		if _, err := ReadRoadmapRowDetail(project, bad, 1); err == nil {
			t.Errorf("row detail path %q should have been rejected", bad)
		}
	}
}

// ---- curated status glyphs and heading pointers ----

// writeGlyphRoadmap mirrors this repo's own LEARN-TASKS.md: an index table
// whose status cells lead with a marker glyph, task headings below it, and a
// status file pointing at the *headings* rather than at the table rows.
func writeGlyphRoadmap(t *testing.T, status string) string {
	t.Helper()
	dir := t.TempDir()
	roadmap := "# Слой опыта — план работ\n\n" +
		"| Задача | Статус | Ключевые файлы |\n" +
		"|---|---|---|\n" +
		"| LN-01 | ✓ DONE (2026-09-08) | internal/experience/transcript.go |\n" +
		"| LN-02 | ○ TODO | internal/experience/signature.go |\n" +
		"| LN-03 | ● IN PROGRESS | internal/experience/indexer.go |\n" +
		"\n" +
		"## LN-01: Чтение транскриптов\n\nтекст\n" +
		"## LN-02: Сигнатуры действий\n\nтекст\n" +
		"## LN-03: Индексатор\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS.md"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadRoadmap_StatusGlyphsAreNotTheStatusWord(t *testing.T) {
	// "✓ DONE" must read as done: taking the leading token literally yields the
	// glyph, every row falls through to pending, and the panel reports 0/N.
	view, err := ReadRoadmap(writeGlyphRoadmap(t, "TASKS.md:9\n"), "STATUS.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.StatusModel != StatusModelCurated {
		t.Fatalf("status model: %q", view.StatusModel)
	}
	if view.Done != 1 || view.Total != 3 {
		t.Fatalf("progress %d/%d, want 1/3", view.Done, view.Total)
	}
	if got := nodeByName(t, view, "LN-01").Status; got != RoadmapTaskDone {
		t.Errorf("LN-01 (✓ DONE) → %q", got)
	}
	if got := nodeByName(t, view, "LN-02").Status; got != RoadmapTaskPending {
		t.Errorf("LN-02 (○ TODO) → %q", got)
	}
	if got := nodeByName(t, view, "LN-03").Status; got != RoadmapTaskActive {
		t.Errorf("LN-03 (● IN PROGRESS) → %q", got)
	}
}

func TestReadRoadmap_PointerAtHeadingMarksItsRow(t *testing.T) {
	// The queue points at "## LN-02: …" (line 12), not at the table row (line
	// 6). Matching by id is what puts the "← now" mark on a curated breakdown.
	view, err := ReadRoadmap(writeGlyphRoadmap(t, "TASKS.md:12\nTASKS.md:15\n"), "STATUS.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	ln02 := nodeByName(t, view, "LN-02")
	if !ln02.Current || !ln02.InQueue {
		t.Errorf("LN-02: current=%v in_queue=%v, want the current task", ln02.Current, ln02.InQueue)
	}
	ln03 := nodeByName(t, view, "LN-03")
	if ln03.Current || !ln03.InQueue {
		t.Errorf("LN-03: current=%v in_queue=%v, want queued only", ln03.Current, ln03.InQueue)
	}
	if ln01 := nodeByName(t, view, "LN-01"); ln01.InQueue {
		t.Error("LN-01 has no pointer and must not be in queue")
	}
}

func TestRemapPointers_UnmatchedPointerIsLeftAlone(t *testing.T) {
	// A pointer into prose that names no known task must not be attached to
	// some row by accident — it simply marks nothing.
	view, err := ReadRoadmap(writeGlyphRoadmap(t, "TASKS.md:1\n"), "STATUS.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	for _, n := range flattenNodes(view.Nodes) {
		if n.Current || n.InQueue {
			t.Errorf("node %q got marked by an unrelated pointer", n.Name)
		}
	}
}

func TestStatusWord_StripsMarkers(t *testing.T) {
	cases := map[string]string{
		"✓ DONE (2026-09-08)": "done",
		"○ TODO":              "todo",
		"● IN PROGRESS":       "in progress",
		"[x] done":            "done",
		"— blocked":           "blocked",
		"done":                "done",
	}
	for raw, want := range cases {
		if got := statusWord(raw); got != want {
			t.Errorf("statusWord(%q) = %q, want %q", raw, got, want)
		}
	}
}
