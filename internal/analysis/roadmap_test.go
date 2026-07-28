package analysis

import (
	"os"
	"path/filepath"
	"regexp"
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
			{
				ID:              "setup",
				Name:            "project-setup",
				Summary:         "Scaffold the repo and wire the build",
				Prompt:          "Scaffold the repo:\ngo mod init, add Makefile.",
				EstimatedTokens: 40000,
				FilesToTouch:    []string{"go.mod", "Makefile"},
			},
			// No Summary — must fall back to the first sentence of Prompt.
			{ID: "auth", Name: "auth", Prompt: "Add JWT auth middleware. Cover it with tests.", DependsOn: []string{"setup"}},
			{ID: "ui", Name: "ui", Prompt: "Build the login page.", DependsOn: []string{"setup"}},
		},
		ExecutionOrder: [][]string{{"setup"}, {"auth", "ui"}},
	}
}

// tableRows returns the task rows of a written ROADMAP.md (skipping the header
// and separator rows).
func tableRows(t *testing.T, roadmapPath string) []string {
	t.Helper()
	var out []string
	for _, line := range readLines(t, roadmapPath) {
		if strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| # ") {
			out = append(out, line)
		}
	}
	return out
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

func TestWriteRoadmapFiles_WritesOneDetailFilePerTask(t *testing.T) {
	dir := t.TempDir()
	roadmapPath, _, err := WriteRoadmapFiles(dir, testPlan(), false)
	if err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatalf("read tasks dir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected one detail file per subtask, got %d", len(entries))
	}

	rows := tableRows(t, roadmapPath)
	if len(rows) != 3 {
		t.Fatalf("expected 3 table rows, got %d", len(rows))
	}

	// Every row's Details link must point at a file that actually exists —
	// a dangling link leaves the session with nothing but the one-line row.
	linkRe := regexp.MustCompile(`\[details\]\(([^)]+)\)`)
	for i, row := range rows {
		m := linkRe.FindStringSubmatch(row)
		if m == nil {
			t.Fatalf("row %d has no Details link: %q", i+1, row)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("row %d links to %s which does not exist: %v", i+1, m[1], err)
		}
	}

	// The first task's full prompt belongs in its file, not in the row.
	first := linkRe.FindStringSubmatch(rows[0])
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(first[1])))
	if err != nil {
		t.Fatalf("read detail file: %v", err)
	}
	text := string(body)
	for _, want := range []string{"# 1. project-setup", "go mod init, add Makefile.", "~40k", "go.mod, Makefile"} {
		if !strings.Contains(text, want) {
			t.Errorf("detail file missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(rows[0], "go mod init") {
		t.Errorf("full prompt leaked into the table row: %q", rows[0])
	}
}

func TestWriteRoadmapFiles_RowsFitTheUITruncationBudget(t *testing.T) {
	dir := t.TempDir()
	plan := testPlan()
	// A verbose analyst: no summary, a very long prompt.
	plan.Subtasks[1].Prompt = strings.Repeat("Port the entire storage layer with great care ", 30)
	roadmapPath, _, err := WriteRoadmapFiles(dir, plan, false)
	if err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}

	linkRe := regexp.MustCompile(`\[details\]\(([^)]+)\)`)
	for _, row := range tableRows(t, roadmapPath) {
		if n := len([]rune(row)); n > maxRowRunes {
			t.Errorf("row is %d runes, over the %d-rune budget: %q", n, maxRowRunes, row)
		}
		// Truncation must never eat the link — that is the part a session
		// cannot reconstruct on its own.
		if !linkRe.MatchString(row) {
			t.Errorf("row lost its Details link: %q", row)
		}
	}
}

func TestWriteRoadmapFiles_FallsBackToFirstSentenceWithoutSummary(t *testing.T) {
	dir := t.TempDir()
	roadmapPath, _, err := WriteRoadmapFiles(dir, testPlan(), false)
	if err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}
	rows := tableRows(t, roadmapPath)
	// Task 2 ("auth") has no Summary; its prompt is two sentences.
	if !strings.Contains(rows[1], "Add JWT auth middleware.") {
		t.Errorf("row 2 should carry the first prompt sentence, got: %q", rows[1])
	}
	if strings.Contains(rows[1], "Cover it with tests") {
		t.Errorf("row 2 should stop at the first sentence, got: %q", rows[1])
	}
}

func TestWriteRoadmapFiles_GuardsPopulatedTasksDir(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "01-old.md"), []byte("# older roadmap\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ROADMAP.md/STATUS-P1.md are absent, but tasks/ is not — writing a fresh
	// numbered set here would interleave two roadmaps.
	if _, _, err := WriteRoadmapFiles(dir, testPlan(), false); err != ErrRoadmapFilesExist {
		t.Fatalf("expected ErrRoadmapFilesExist, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ROADMAP.md")); err == nil {
		t.Error("ROADMAP.md should not have been created when the guard blocked the write")
	}
	if _, _, err := WriteRoadmapFiles(dir, testPlan(), true); err != nil {
		t.Fatalf("overwrite=true should succeed: %v", err)
	}
}

func TestWriteRoadmapFiles_EmptyTasksDirIsNotAConflict(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteRoadmapFiles(dir, testPlan(), false); err != nil {
		t.Fatalf("an empty tasks/ must not block the write: %v", err)
	}
}

// ---- row/detail helpers ----

func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Add JWT auth. Then tests.", "Add JWT auth."},
		{"No sentence break here", "No sentence break here"},
		{"Edit go.mod and Makefile.", "Edit go.mod and Makefile."},
		{"Multi\nline\nprompt. Rest.", "Multi line prompt."},
		{"Does it work? Probably.", "Does it work?"},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("no-op truncate returned %q", got)
	}
	if got := truncateRunes("hello world", 8); got != "hello w…" {
		t.Errorf("truncate returned %q", got)
	}
	// Cyrillic must be cut by rune, never mid-byte.
	got := truncateRunes("привет мир", 7)
	if []rune(got)[len([]rune(got))-1] != '…' || len([]rune(got)) != 7 {
		t.Errorf("cyrillic truncate returned %q (%d runes)", got, len([]rune(got)))
	}
	if got := truncateRunes("abc", 0); got != "" {
		t.Errorf("zero budget returned %q", got)
	}
}

func TestSlugifyAndTaskFileName(t *testing.T) {
	cases := []struct{ id, want string }{
		{"auth", "03-auth.md"},
		{"Auth Middleware", "03-auth-middleware.md"},
		{"t1_storage/layer", "03-t1-storage-layer.md"},
		{"хранилище", "03.md"}, // non-ASCII id: fall back to the bare number
		{"", "03.md"},
		{"---", "03.md"},
	}
	for _, c := range cases {
		if got := taskFileName(3, c.id); got != c.want {
			t.Errorf("taskFileName(3, %q) = %q, want %q", c.id, got, c.want)
		}
	}
	if got := slugify(strings.Repeat("a", 60)); len(got) != 40 {
		t.Errorf("slug should be capped at 40 chars, got %d", len(got))
	}
}

func TestRowSummary_PrefersSummaryOverPrompt(t *testing.T) {
	sub := &PlannedSubtask{Summary: "Short label", Prompt: "A much longer prompt. With sentences."}
	if got := rowSummary(sub, 100); got != "Short label" {
		t.Errorf("rowSummary = %q, want the Summary field", got)
	}
	sub.Summary = ""
	if got := rowSummary(sub, 100); got != "A much longer prompt." {
		t.Errorf("rowSummary fallback = %q", got)
	}
	if got := rowSummary(sub, 10); len([]rune(got)) > 10 {
		t.Errorf("rowSummary ignored its budget: %q", got)
	}
}

func TestRenderTaskFile_IncludesAnalystMetadata(t *testing.T) {
	sub := &PlannedSubtask{
		ID: "auth", Name: "auth middleware", Prompt: "Do the thing.",
		EstimatedTokens: 90000, FilesToTouch: []string{"a.go", "b.go"}, Model: "sonnet",
	}
	got := renderTaskFile(2, sub, "1")
	for _, want := range []string{"# 2. auth middleware", "**Depends on:** 1", "~90k", "a.go, b.go", "sonnet", "Do the thing."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderTaskFile_UnnamedSubtaskFallsBackToID(t *testing.T) {
	got := renderTaskFile(1, &PlannedSubtask{ID: "x7", Prompt: "p"}, "-")
	if !strings.Contains(got, "# 1. x7") {
		t.Errorf("expected the id as heading fallback:\n%s", got)
	}
}

func TestTasksDirBlocked(t *testing.T) {
	dir := t.TempDir()
	if tasksDirBlocked(filepath.Join(dir, "missing")) {
		t.Error("a missing dir must not count as a conflict")
	}
	sub := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if tasksDirBlocked(sub) {
		t.Error("an empty dir must not count as a conflict")
	}
	if err := os.WriteFile(filepath.Join(sub, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tasksDirBlocked(sub) {
		t.Error("a non-markdown file must not count as a conflict")
	}
	if err := os.WriteFile(filepath.Join(sub, "01-a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tasksDirBlocked(sub) {
		t.Error("a markdown file must block the write")
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
