package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectLogsDir(t *testing.T) {
	got := ProjectLogsDir(filepath.Join("proj"))
	want := filepath.Join("proj", ".claude-manager", "logs")
	if got != want {
		t.Errorf("ProjectLogsDir: got %q want %q", got, want)
	}
}

func sampleEntries() []LogEntry {
	return []LogEntry{
		{Timestamp: time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC), Level: "text", Message: "hello"},
		{Timestamp: time.Date(2026, 1, 2, 15, 4, 6, 0, time.UTC), Level: "tool", Message: "ran a tool", ToolName: "Bash", ToolInput: "ls"},
	}
}

func TestRenderExportFormats(t *testing.T) {
	entries := sampleEntries()

	md, err := RenderExport("proj/S1", entries, "md")
	if err != nil {
		t.Fatalf("RenderExport md: %v", err)
	}
	if s := string(md); !strings.Contains(s, "# Session log — proj/S1") || !strings.Contains(s, "hello") || !strings.Contains(s, "tool: `Bash`") {
		t.Errorf("md export missing expected content: %s", s)
	}

	js, err := RenderExport("proj/S1", entries, "json")
	if err != nil {
		t.Fatalf("RenderExport json: %v", err)
	}
	if s := string(js); !strings.Contains(s, `"session": "proj/S1"`) || !strings.Contains(s, "hello") {
		t.Errorf("json export missing expected content: %s", s)
	}

	txt, err := RenderExport("proj/S1", entries, "txt")
	if err != nil {
		t.Fatalf("RenderExport txt: %v", err)
	}
	if s := string(txt); !strings.Contains(s, "Session log — proj/S1") || !strings.Contains(s, "hello") {
		t.Errorf("txt export missing expected content: %s", s)
	}
}

func TestSaveSessionLogFileWritesMarkdown(t *testing.T) {
	dir := t.TempDir()
	entries := sampleEntries()

	path, err := SaveSessionLogFile(dir, "proj/S1", entries)
	if err != nil {
		t.Fatalf("SaveSessionLogFile: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if filepath.Dir(path) != ProjectLogsDir(dir) {
		t.Errorf("expected file under %q, got %q", ProjectLogsDir(dir), path)
	}
	if !strings.HasSuffix(path, ".md") {
		t.Errorf("expected .md file, got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("saved file missing log content: %s", data)
	}

	gitignore, err := os.ReadFile(filepath.Join(dir, ".claude-manager", ".gitignore"))
	if err != nil {
		t.Fatalf("expected .gitignore to be written: %v", err)
	}
	if !strings.Contains(string(gitignore), "logs/") {
		t.Errorf("expected .gitignore to contain logs/, got %s", gitignore)
	}
}

func TestSaveSessionLogFileEmptyEntriesNoop(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveSessionLogFile(dir, "proj/S1", nil)
	if err != nil {
		t.Fatalf("SaveSessionLogFile: %v", err)
	}
	if path != "" {
		t.Errorf("expected no-op for empty entries, got path %q", path)
	}
	if _, err := os.Stat(ProjectLogsDir(dir)); !os.IsNotExist(err) {
		t.Errorf("expected logs dir not to be created, err=%v", err)
	}
}

func TestSaveSessionLogFileEmptyProjectPathNoop(t *testing.T) {
	path, err := SaveSessionLogFile("", "proj/S1", sampleEntries())
	if err != nil {
		t.Fatalf("SaveSessionLogFile: %v", err)
	}
	if path != "" {
		t.Errorf("expected no-op for empty project path, got path %q", path)
	}
}

func TestListProjectLogFilesMissingDir(t *testing.T) {
	dir := t.TempDir()
	files, err := ListProjectLogFiles(dir)
	if err != nil {
		t.Fatalf("ListProjectLogFiles: %v", err)
	}
	if files != nil {
		t.Errorf("expected nil slice for missing dir, got %v", files)
	}
}

func TestListProjectLogFilesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveSessionLogFile(dir, "proj/S1", sampleEntries()); err != nil {
		t.Fatalf("SaveSessionLogFile 1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := SaveSessionLogFile(dir, "proj/S2", sampleEntries()); err != nil {
		t.Fatalf("SaveSessionLogFile 2: %v", err)
	}

	files, err := ListProjectLogFiles(dir)
	if err != nil {
		t.Fatalf("ListProjectLogFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !strings.HasPrefix(files[0].Name, "proj_S2-") {
		t.Errorf("expected newest (S2) first, got %q then %q", files[0].Name, files[1].Name)
	}
	for _, f := range files {
		if f.Size == 0 {
			t.Errorf("expected non-zero size for %q", f.Name)
		}
	}
}

func TestClearProjectLogFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveSessionLogFile(dir, "proj/S1", sampleEntries()); err != nil {
		t.Fatalf("SaveSessionLogFile 1: %v", err)
	}
	if _, err := SaveSessionLogFile(dir, "proj/S2", sampleEntries()); err != nil {
		t.Fatalf("SaveSessionLogFile 2: %v", err)
	}

	removed, freed, err := ClearProjectLogFiles(dir)
	if err != nil {
		t.Fatalf("ClearProjectLogFiles: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if freed == 0 {
		t.Errorf("expected non-zero freed bytes")
	}

	files, err := ListProjectLogFiles(dir)
	if err != nil {
		t.Fatalf("ListProjectLogFiles after clear: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files after clear, got %d", len(files))
	}
}

func TestClearProjectLogFilesMissingDirNoop(t *testing.T) {
	dir := t.TempDir()
	removed, freed, err := ClearProjectLogFiles(dir)
	if err != nil {
		t.Fatalf("ClearProjectLogFiles: %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("expected no-op, got removed=%d freed=%d", removed, freed)
	}
}
