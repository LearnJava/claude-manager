package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProtocolFiles_WritesAllArtifacts(t *testing.T) {
	dir := t.TempDir()

	written, err := WriteProtocolFiles(dir, ProtocolParams{
		QueueFile:  "STATUS-P1.md",
		MainBranch: "main",
	})
	if err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}
	if len(written) != len(protocolFiles) {
		t.Fatalf("written = %v, want %d files", written, len(protocolFiles))
	}
	for _, f := range protocolFiles {
		p := filepath.Join(dir, f.rel)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s not written: %v", f.rel, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", f.rel)
		}
	}
	// The pool holds working copies of the repo itself — never committed.
	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".claude/worktrees/") {
		t.Errorf(".gitignore missing the worktree pool entry: %q", gitignore)
	}
}

func TestWriteProtocolFiles_RendersProjectSpecifics(t *testing.T) {
	dir := t.TempDir()

	if _, err := WriteProtocolFiles(dir, ProtocolParams{
		QueueFile:  "TASKS-QUEUE.md",
		MainBranch: "master",
	}); err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}

	workflow := readFile(t, filepath.Join(dir, "docs", "git-workflow.md"))
	if !strings.Contains(workflow, "TASKS-QUEUE.md") {
		t.Error("workflow doc does not name the project's queue file")
	}
	// A protocol that merges into the wrong branch is worse than none.
	if !strings.Contains(workflow, "git push origin master") {
		t.Error("workflow doc does not use the detected integration branch")
	}
	if strings.Contains(workflow, "{{") {
		t.Error("workflow doc has unrendered template actions")
	}

	pool := readFile(t, filepath.Join(dir, "scripts", "worktree-pool.sh"))
	if !strings.Contains(pool, "INTEGRATION=master") {
		t.Error("pool script does not use the detected integration branch")
	}

	start := readFile(t, filepath.Join(dir, ".claude", "skills", "cm-task-start", "SKILL.md"))
	if !strings.Contains(start, "git pull origin master") {
		t.Error("start skill does not use the detected integration branch")
	}
	if !strings.Contains(start, "TASKS-QUEUE.md") {
		t.Error("start skill does not name the project's queue file")
	}
}

func TestWriteProtocolFiles_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "docs", "git-workflow.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("# our own protocol\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := WriteProtocolFiles(dir, ProtocolParams{MainBranch: "main"})
	if err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}
	for _, w := range written {
		if w == "docs/git-workflow.md" {
			t.Fatal("overwrote the project's own protocol doc")
		}
	}
	if got := readFile(t, own); got != "# our own protocol\n" {
		t.Errorf("project's own doc changed: %q", got)
	}

	// Second run is a no-op: everything is already in place.
	again, err := WriteProtocolFiles(dir, ProtocolParams{MainBranch: "main"})
	if err != nil {
		t.Fatalf("second WriteProtocolFiles: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second run wrote %v, want nothing", again)
	}
}

func TestWriteProtocolFiles_GatesRenderedWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteProtocolFiles(dir, ProtocolParams{
		QueueFile:  "STATUS-P1.md",
		MainBranch: "main",
		Gates:      []string{"cargo clippy --workspace -- -D warnings", "cargo test"},
	}); err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}

	finish := readFile(t, filepath.Join(dir, ".claude", "skills", "cm-task-finish", "SKILL.md"))
	for _, want := range []string{"cargo clippy --workspace -- -D warnings", "cargo test"} {
		if !strings.Contains(finish, want) {
			t.Errorf("finish skill missing configured gate %q", want)
		}
	}
	workflow := readFile(t, filepath.Join(dir, "docs", "git-workflow.md"))
	if !strings.Contains(workflow, "cargo clippy --workspace -- -D warnings && cargo test") {
		t.Error("workflow doc does not chain the configured gates")
	}
}

func TestWriteProtocolFiles_GateDescribedWhenNoGatesConfigured(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteProtocolFiles(dir, ProtocolParams{MainBranch: "main"}); err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}
	finish := readFile(t, filepath.Join(dir, ".claude", "skills", "cm-task-finish", "SKILL.md"))
	// No build system may be named: the app writes this into projects whose
	// language it does not know.
	for _, forbidden := range []string{"cargo ", "go test", "npm ", "pytest"} {
		if strings.Contains(finish, forbidden) {
			t.Errorf("finish skill names a build system (%q) with no gates configured", forbidden)
		}
	}
	if !strings.Contains(finish, "static-analysis") {
		t.Error("finish skill does not describe the gate at all")
	}
}

func TestProtocolParams_DefaultsToStatusFileAndMain(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteProtocolFiles(dir, ProtocolParams{}); err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}
	workflow := readFile(t, filepath.Join(dir, "docs", "git-workflow.md"))
	if !strings.Contains(workflow, statusFileName) {
		t.Errorf("default queue file not used, want %s", statusFileName)
	}
	if !strings.Contains(workflow, "origin main") {
		t.Error("default integration branch not used")
	}
}

func TestWriteProtocolFiles_EmptyPath(t *testing.T) {
	if _, err := WriteProtocolFiles("  ", ProtocolParams{}); err == nil {
		t.Fatal("expected an error for an empty project path")
	}
}

// TestDefaultP1SessionPrompt_PointsAtTheProtocol locks in the split the whole
// design rests on: the prompt delegates to the files in the repository instead
// of restating them, but still carries the branch-reservation rule, which is
// what makes an interrupted session continue rather than start over.
func TestDefaultP1SessionPrompt_PointsAtTheProtocol(t *testing.T) {
	for _, want := range []string{"docs/git-workflow.md", "/cm-task-start", "/cm-task-finish", "git branch -a"} {
		if !strings.Contains(DefaultP1SessionPrompt, want) {
			t.Errorf("DefaultP1SessionPrompt does not mention %q", want)
		}
	}
}

// A rendered shell script must be LF-only whatever the checkout did to the
// templates: bash rejects a CRLF shebang line outright, and the project this
// lands in may well be built or run on Linux.
func TestWriteProtocolFiles_ShellScriptHasNoCarriageReturns(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteProtocolFiles(dir, ProtocolParams{MainBranch: "main"}); err != nil {
		t.Fatalf("WriteProtocolFiles: %v", err)
	}
	body := readFile(t, filepath.Join(dir, "scripts", "worktree-pool.sh"))
	if strings.Contains(body, "\r") {
		t.Error("rendered worktree-pool.sh contains carriage returns")
	}
	if !strings.HasPrefix(body, "#!/usr/bin/env bash\n") {
		t.Errorf("shebang line is not clean: %q", body[:40])
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
