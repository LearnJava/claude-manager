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
	if got := nodeByName(t, view, "CC-15").Status; got != RoadmapTaskActive {
		t.Errorf("CC-15 is queued by the session, want active, got %q", got)
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
	got := normalizeHeader([]string{"#", "Task", "Details", "Depends on", "Note", "**Bugs**"})
	want := []string{"number", "title", "details", "depends", "note", "bugs"}
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
		"planned": RoadmapTaskPending, "queued": RoadmapTaskPending,
		"ready": RoadmapTaskPending, "opt": RoadmapTaskPending, "": RoadmapTaskPending,
	}
	for raw, want := range cases {
		node := &RoadmapNode{StatusRaw: raw, Line: 5}
		if got := resolveStatus(node, StatusModelCurated, map[int]bool{}); got != want {
			t.Errorf("status %q → %q, want %q", raw, got, want)
		}
	}
	// A planned task the session actually took reads as active.
	node := &RoadmapNode{StatusRaw: "planned", Line: 5, InQueue: true}
	if got := resolveStatus(node, StatusModelCurated, map[int]bool{5: true}); got != RoadmapTaskActive {
		t.Errorf("queued planned task → %q", got)
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
