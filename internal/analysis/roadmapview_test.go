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

func TestReadRoadmap_RoundTripsTheWriter(t *testing.T) {
	dir := writeDemoRoadmap(t)

	view, err := ReadRoadmap(dir, "STATUS-P1.md")
	if err != nil {
		t.Fatalf("ReadRoadmap: %v", err)
	}
	if view.Title != "Roadmap -- demo" {
		t.Errorf("title: %q", view.Title)
	}
	if view.Total != 3 || len(view.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", view.Total)
	}
	if view.Done != 0 {
		t.Errorf("nothing is done yet, got Done=%d", view.Done)
	}
	if !strings.Contains(view.Context, "Go + Wails desktop app.") {
		t.Errorf("shared context not picked up: %q", view.Context)
	}

	first := view.Tasks[0]
	if first.Number != 1 || first.Name != "project-setup" {
		t.Errorf("first task: %+v", first)
	}
	if first.Summary != "Scaffold the repo and wire the build" {
		t.Errorf("summary: %q", first.Summary)
	}
	if first.DetailPath != "tasks/01-setup.md" {
		t.Errorf("detail path: %q", first.DetailPath)
	}
	if first.Status != RoadmapTaskCurrent {
		t.Errorf("first open task must be current, got %q", first.Status)
	}
	if view.Tasks[1].Status != RoadmapTaskPending {
		t.Errorf("second task should be pending, got %q", view.Tasks[1].Status)
	}
	if view.Tasks[2].DependsOn != "1" {
		t.Errorf("depends_on: %q", view.Tasks[2].DependsOn)
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
	lines := strings.Split(string(data), "\n")
	var kept []string
	dropped := false
	for _, l := range lines {
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
	if view.Tasks[0].Status != RoadmapTaskDone {
		t.Errorf("task 1 should be done, got %q", view.Tasks[0].Status)
	}
	if view.Tasks[1].Status != RoadmapTaskCurrent {
		t.Errorf("task 2 should now be current, got %q", view.Tasks[1].Status)
	}
}

func TestReadRoadmap_EmptyBacklogRendersEverythingDone(t *testing.T) {
	dir := writeDemoRoadmap(t)
	// A finished project: the heading survives, every pointer is gone.
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
	for _, task := range view.Tasks {
		if task.Status != RoadmapTaskDone {
			t.Errorf("task %d: want done, got %q", task.Number, task.Status)
		}
	}
}

func TestReadRoadmap_LegacyThreeColumnRoadmap(t *testing.T) {
	// A roadmap written before the per-task-file split: no Details column, the
	// whole prompt inline. It must still parse — those projects are never
	// migrated.
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
	if view.Tasks[0].DetailPath != "" {
		t.Errorf("legacy rows have no detail file, got %q", view.Tasks[0].DetailPath)
	}
	if view.Tasks[0].Name != "Scaffolding" || !strings.Contains(view.Tasks[0].Summary, "Bootstrap the module") {
		t.Errorf("legacy row parsed as %+v", view.Tasks[0])
	}
	// Line 8 is task 2's row; task 1 (line 7) has no pointer, so it is done.
	if view.Tasks[0].Status != RoadmapTaskDone || view.Tasks[1].Status != RoadmapTaskCurrent {
		t.Errorf("statuses: %q, %q", view.Tasks[0].Status, view.Tasks[1].Status)
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

	// Status file exists but the roadmap it points at does not.
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("ROADMAP.md:12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoadmap(dir, "STATUS-P1.md"); !errors.Is(err, ErrNoRoadmap) {
		t.Errorf("missing roadmap file: want ErrNoRoadmap, got %v", err)
	}
}

func TestReadRoadmap_LegacyFormatTaskFileIsNotARoadmap(t *testing.T) {
	// The old "In progress:"/"Next:" task file has no pointer lines at all.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"),
		[]byte("In progress: refactor the parser\n\nNext:\n- [ ] add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoadmap(dir, "TASKS.md"); !errors.Is(err, ErrNoRoadmap) {
		t.Errorf("want ErrNoRoadmap, got %v", err)
	}
}

func TestReadRoadmap_PointersIntoAnotherFileAreIgnored(t *testing.T) {
	dir := writeDemoRoadmap(t)
	// A mixed status file: the first pointer names the roadmap, a stray one
	// names a source file (the pointer format allows any file). The stray must
	// not mark a roadmap row as open.
	status, err := os.ReadFile(filepath.Join(dir, "STATUS-P1.md"))
	if err != nil {
		t.Fatal(err)
	}
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

// ---- parseTaskRow / splitRow ----

func TestParseTaskRow(t *testing.T) {
	cases := []struct {
		name    string
		row     string
		ok      bool
		want    RoadmapTask
		comment string
	}{
		{
			name: "full row",
			row:  "| 3 | **Pause** -- space pauses the countdown | [details](tasks/03-pause.md) | 2 |",
			ok:   true,
			want: RoadmapTask{Number: 3, Name: "Pause", Summary: "space pauses the countdown",
				DetailPath: "tasks/03-pause.md", DependsOn: "2"},
		},
		{
			name: "no dependencies",
			row:  "| 1 | **Setup** -- scaffold | [details](tasks/01-setup.md) | - |",
			ok:   true,
			want: RoadmapTask{Number: 1, Name: "Setup", Summary: "scaffold",
				DetailPath: "tasks/01-setup.md", DependsOn: "-"},
		},
		{
			name: "legacy three columns",
			row:  "| 2 | **Storage** -- port the SQLite layer | 1 |",
			ok:   true,
			want: RoadmapTask{Number: 2, Name: "Storage", Summary: "port the SQLite layer", DependsOn: "1"},
		},
		{
			name: "name only",
			row:  "| 4 | **Release** | - |",
			ok:   true,
			want: RoadmapTask{Number: 4, Name: "Release", DependsOn: "-"},
		},
		{
			name: "unbolded hand-written row",
			row:  "| 5 | just do the thing | - |",
			ok:   true,
			want: RoadmapTask{Number: 5, Summary: "just do the thing", DependsOn: "-"},
		},
		{name: "header", row: "| # | Task | Details | Depends on |", ok: false},
		{name: "separator", row: "|---|------|---------|------------|", ok: false},
		{name: "not a table", row: "some prose", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseTaskRow(tc.row)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ---- ReadRoadmapTaskDetail ----

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

func TestReadRoadmapTaskDetail_RefusesToEscapeTheProject(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
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
	}
}
