package analysis

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- flattenExecutionOrder ----

func TestFlattenExecutionOrder_GroupOrderPreservedAndDeduped(t *testing.T) {
	subtasks := []PlannedSubtask{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	order := [][]string{{"a", "b"}, {"a"}, {"c"}} // "a" repeated across groups
	got := flattenExecutionOrder(order, subtasks)
	want := []string{"a", "b", "c"}
	if !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFlattenExecutionOrder_AppendsSubtasksMissingFromOrder(t *testing.T) {
	subtasks := []PlannedSubtask{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	order := [][]string{{"b"}} // "a" and "c" never mentioned
	got := flattenExecutionOrder(order, subtasks)
	want := []string{"b", "a", "c"}
	if !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFlattenExecutionOrder_EmptyOrderFallsBackToSubtaskOrder(t *testing.T) {
	subtasks := []PlannedSubtask{{ID: "x"}, {ID: "y"}}
	got := flattenExecutionOrder(nil, subtasks)
	want := []string{"x", "y"}
	if !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- WriteRoadmapFiles ----

func testPlan() *TaskPlan {
	return &TaskPlan{
		Project:       "demo",
		SharedContext: "Go + Wails desktop app.",
		Subtasks: []PlannedSubtask{
			{ID: "setup", Name: "project-setup", Prompt: "Scaffold the repo:\ngo mod init, add Makefile."},
			{ID: "auth", Name: "auth", Prompt: "Add JWT auth middleware.", DependsOn: []string{"setup"}},
			{ID: "ui", Name: "ui", Prompt: "Build the login page.", DependsOn: []string{"setup"}},
		},
		ExecutionOrder: [][]string{{"setup"}, {"auth", "ui"}},
	}
}

func TestWriteRoadmapFiles_LineNumbersMatchPointers(t *testing.T) {
	dir := t.TempDir()
	roadmapPath, statusPath, err := WriteRoadmapFiles(dir, testPlan(), false)
	if err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}
	if roadmapPath != filepath.Join(dir, "ROADMAP.md") || statusPath != filepath.Join(dir, "STATUS-P1.md") {
		t.Fatalf("unexpected paths: %s, %s", roadmapPath, statusPath)
	}

	roadmapLines := readLines(t, roadmapPath)
	statusLines := readLines(t, statusPath)

	pointerLines := 0
	for _, line := range statusLines {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ">") {
			continue
		}
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 || parts[0] != "ROADMAP.md" {
			t.Fatalf("unexpected status line: %q", s)
		}
		lineNo, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("pointer line number not an int: %q", s)
		}
		if lineNo < 1 || lineNo > len(roadmapLines) {
			t.Fatalf("pointer line %d out of range (file has %d lines)", lineNo, len(roadmapLines))
		}
		pointerLines++
	}
	if pointerLines != 3 {
		t.Fatalf("expected 3 pointer lines (one per subtask), got %d", pointerLines)
	}

	// The first pointer must resolve to the "setup" task's row (it has no
	// dependencies, so execution_order puts it first).
	firstPointerLine := 0
	for _, line := range statusLines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "ROADMAP.md:") {
			n, _ := strconv.Atoi(strings.TrimPrefix(s, "ROADMAP.md:"))
			firstPointerLine = n
			break
		}
	}
	if firstPointerLine == 0 {
		t.Fatal("no pointer line found in STATUS-P1.md")
	}
	resolvedRow := roadmapLines[firstPointerLine-1]
	if !strings.Contains(resolvedRow, "project-setup") {
		t.Errorf("first pointer should resolve to the setup task row, got: %q", resolvedRow)
	}
}

func TestWriteRoadmapFiles_GuardsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	roadmapPath := filepath.Join(dir, "ROADMAP.md")
	if err := os.WriteFile(roadmapPath, []byte("# hand-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := WriteRoadmapFiles(dir, testPlan(), false); err != ErrRoadmapFilesExist {
		t.Fatalf("expected ErrRoadmapFilesExist, got %v", err)
	}
	// Must not have touched the existing file or created STATUS-P1.md.
	content, _ := os.ReadFile(roadmapPath)
	if string(content) != "# hand-authored\n" {
		t.Error("existing ROADMAP.md was modified despite overwrite=false")
	}
	if _, err := os.Stat(filepath.Join(dir, "STATUS-P1.md")); err == nil {
		t.Error("STATUS-P1.md should not have been created when the guard blocked the write")
	}

	if _, _, err := WriteRoadmapFiles(dir, testPlan(), true); err != nil {
		t.Fatalf("overwrite=true should succeed: %v", err)
	}
}

func TestWriteRoadmapFiles_NilPlan(t *testing.T) {
	if _, _, err := WriteRoadmapFiles(t.TempDir(), nil, false); err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestWriteRoadmapFiles_NoSubtasks(t *testing.T) {
	plan := &TaskPlan{Project: "empty"}
	if _, _, err := WriteRoadmapFiles(t.TempDir(), plan, false); err == nil {
		t.Error("expected error when the plan has no subtasks")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(string(data), "\n")
}
