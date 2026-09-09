package experience

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

// TestAppendEntry_WritesExpectedFormat locks in the exact markdown shape
// from LEARN-TASKS.md LN-06's spec.
func TestAppendEntry_WritesExpectedFormat(t *testing.T) {
	dir := t.TempDir()

	entry := Entry{
		Date:      mustDate(t, "2026-09-08"),
		TaskPtr:   "ROADMAP.md:92",
		Done:      "wired the SQLite migration",
		Surprises: []string{"the driver silently ignored a duplicate column"},
		Avoid:     []string{"don't re-run ALTER TABLE unconditionally"},
	}
	if err := AppendEntry(dir, false, entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude-manager", JournalFileName))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	want := "## 2026-09-08 — ROADMAP.md:92\n" +
		"**Сделано:** wired the SQLite migration\n" +
		"**Неожиданно:** the driver silently ignored a duplicate column\n" +
		"**Не делать:** don't re-run ALTER TABLE unconditionally\n"
	if string(data) != want {
		t.Errorf("journal.md content:\n%q\nwant:\n%q", string(data), want)
	}
}

// TestAppendEntry_NoTaskPtrOmitsDash: a session with no task_source still
// gets a plain date header, not a dangling " — ".
func TestAppendEntry_NoTaskPtrOmitsDash(t *testing.T) {
	dir := t.TempDir()
	entry := Entry{Date: mustDate(t, "2026-09-08"), Done: "did something"}
	if err := AppendEntry(dir, false, entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude-manager", JournalFileName))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	header, _, _ := strings.Cut(string(data), "\n")
	if header != "## 2026-09-08" {
		t.Errorf("header = %q, want plain date with no dash", header)
	}
}

// TestAppendEntry_EmptyFieldsRenderDash: empty Surprises/Avoid render as "—"
// rather than an empty trailing line, so the file stays readable.
func TestAppendEntry_EmptyFieldsRenderDash(t *testing.T) {
	dir := t.TempDir()
	entry := Entry{Date: mustDate(t, "2026-09-08"), Done: "did something"}
	if err := AppendEntry(dir, false, entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	entries, err := LastEntries(dir, 1)
	if err != nil {
		t.Fatalf("LastEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if len(entries[0].Surprises) != 0 || len(entries[0].Avoid) != 0 {
		t.Errorf("expected empty Surprises/Avoid, got %+v", entries[0])
	}
}

// TestAppendEntry_MultipleAndLastEntries: appends accumulate and LastEntries
// returns the requested tail, oldest of the n first.
func TestAppendEntry_MultipleAndLastEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 5; i++ {
		e := Entry{
			Date: mustDate(t, fmt.Sprintf("2026-09-0%d", i)),
			Done: fmt.Sprintf("task %d", i),
		}
		if err := AppendEntry(dir, false, e); err != nil {
			t.Fatalf("AppendEntry %d: %v", i, err)
		}
	}

	all, err := LastEntries(dir, 100)
	if err != nil {
		t.Fatalf("LastEntries: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("got %d entries, want 5", len(all))
	}
	for i, e := range all {
		want := fmt.Sprintf("task %d", i+1)
		if e.Done != want {
			t.Errorf("entry %d Done = %q, want %q", i, e.Done, want)
		}
	}

	last2, err := LastEntries(dir, 2)
	if err != nil {
		t.Fatalf("LastEntries(2): %v", err)
	}
	if len(last2) != 2 || last2[0].Done != "task 4" || last2[1].Done != "task 5" {
		t.Errorf("LastEntries(2) = %+v, want tasks 4 and 5", last2)
	}
}

// TestAppendEntry_RotatesOldestIntoArchive: once journal.md exceeds
// MaxJournalEntries sections, the oldest overflow moves into
// journal-archive-<YYYY-MM>.md and journal.md keeps exactly the cap.
func TestAppendEntry_RotatesOldestIntoArchive(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxJournalEntries+3; i++ {
		e := Entry{
			Date: mustDate(t, "2026-01-01").AddDate(0, 0, i),
			Done: fmt.Sprintf("task %d", i),
		}
		if err := AppendEntry(dir, false, e); err != nil {
			t.Fatalf("AppendEntry %d: %v", i, err)
		}
	}

	kept, err := LastEntries(dir, 1000)
	if err != nil {
		t.Fatalf("LastEntries: %v", err)
	}
	if len(kept) != MaxJournalEntries {
		t.Fatalf("journal.md holds %d entries, want %d", len(kept), MaxJournalEntries)
	}
	// The oldest 3 entries (task 0, 1, 2) rotated out; journal.md now starts
	// at task 3.
	if kept[0].Done != "task 3" {
		t.Errorf("oldest kept entry = %q, want %q", kept[0].Done, "task 3")
	}

	archivePath := filepath.Join(dir, ".claude-manager", "journal-archive-2026-01.md")
	sections, err := readSections(archivePath)
	if err != nil {
		t.Fatalf("readSections(archive): %v", err)
	}
	if len(sections) != 3 {
		t.Fatalf("archive holds %d sections, want 3", len(sections))
	}
	for i, s := range sections {
		want := fmt.Sprintf("**Сделано:** task %d", i)
		if !strings.Contains(s, want) {
			t.Errorf("archive section %d = %q, want to contain %q", i, s, want)
		}
	}
}

// TestAppendEntry_Atomic: no leftover .tmp file after a successful append.
func TestAppendEntry_Atomic(t *testing.T) {
	dir := t.TempDir()
	if err := AppendEntry(dir, false, Entry{Date: mustDate(t, "2026-09-08"), Done: "x"}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".claude-manager"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestAppendEntry_Gitignore: commit=false adds journal.md to .gitignore;
// commit=true leaves it out (a project that wants its journal committed and
// reviewed can do so).
func TestAppendEntry_Gitignore(t *testing.T) {
	dir := t.TempDir()
	if err := AppendEntry(dir, false, Entry{Date: mustDate(t, "2026-09-08"), Done: "x"}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(config.ProjectConfigDir(dir), ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), JournalFileName) {
		t.Errorf(".gitignore missing %s: %q", JournalFileName, string(gitignore))
	}

	dir2 := t.TempDir()
	if err := AppendEntry(dir2, true, Entry{Date: mustDate(t, "2026-09-08"), Done: "x"}); err != nil {
		t.Fatalf("AppendEntry (commit=true): %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(config.ProjectConfigDir(dir2), ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("commit=true must not create .gitignore, got err=%v", err)
	}
}

// TestLastEntries_NoJournalYet: an empty result, no error, when the journal
// has never been written for this project.
func TestLastEntries_NoJournalYet(t *testing.T) {
	dir := t.TempDir()
	entries, err := LastEntries(dir, 3)
	if err != nil {
		t.Fatalf("LastEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries, got %+v", entries)
	}
}

// TestParseSection_MultiItemLists round-trips Surprises/Avoid with more than
// one item through the "; "-joined rendering.
func TestParseSection_MultiItemLists(t *testing.T) {
	e := Entry{
		Date:      mustDate(t, "2026-09-08"),
		Done:      "did the thing",
		Surprises: []string{"first surprise", "second surprise"},
		Avoid:     []string{"avoid A", "avoid B"},
	}
	got, ok := parseSection(e.render())
	if !ok {
		t.Fatal("parseSection returned ok=false")
	}
	if len(got.Surprises) != 2 || got.Surprises[0] != "first surprise" || got.Surprises[1] != "second surprise" {
		t.Errorf("Surprises = %+v", got.Surprises)
	}
	if len(got.Avoid) != 2 || got.Avoid[0] != "avoid A" || got.Avoid[1] != "avoid B" {
		t.Errorf("Avoid = %+v", got.Avoid)
	}
}
