package mirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitCount(t *testing.T, root string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestSyncAll_CopiesCommitsAndTracksDeletes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	proj := t.TempDir()
	root := filepath.Join(t.TempDir(), "projects")
	writeFile(t, filepath.Join(proj, "CLAUDE.md"), "# rules\n")
	writeFile(t, filepath.Join(proj, ".claude-manager", "config.toml"), "gates = []\n")
	writeFile(t, filepath.Join(proj, ".claude-manager", "logs", "Dev-1.md"), "log one\n")
	writeFile(t, filepath.Join(proj, "STATUS.md"), "- [ ] task\n")
	writeFile(t, filepath.Join(proj, "main.go"), "package main\n") // not a setting: not copied

	projects := []config.ProjectConfig{
		{Name: "demo app", Path: proj, Mirror: true, Sessions: []config.SessionConfig{{Name: "Dev", TaskSource: "STATUS.md"}}},
		{Name: "other", Path: proj}, // not opted in
	}
	ctx := context.Background()

	res, err := SyncAll(ctx, root, projects)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(res) != 1 || res[0].Error != "" || res[0].Files != 4 {
		t.Fatalf("results = %+v, want one project with 4 files", res)
	}
	dir := filepath.Join(root, "demo_app")
	for _, rel := range []string{"files/CLAUDE.md", "files/.claude-manager/logs/Dev-1.md", "files/STATUS.md", ManifestFile, EffectiveConfigFile} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "main.go")); err == nil {
		t.Error("main.go was mirrored")
	}
	if _, err := os.Stat(filepath.Join(root, "other")); err == nil {
		t.Error("project without mirror = true was mirrored")
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("mirror root is not its own repository: %v", err)
	}
	if got := commitCount(t, root); got != 1 {
		t.Fatalf("commits after first sync = %d, want 1", got)
	}

	// Nothing changed: no new commit.
	if _, err := SyncAll(ctx, root, projects); err != nil {
		t.Fatal(err)
	}
	if got := commitCount(t, root); got != 1 {
		t.Fatalf("commits after idle sync = %d, want 1", got)
	}

	// A new log and a deleted file are both mirrored and committed.
	writeFile(t, filepath.Join(proj, ".claude-manager", "logs", "Dev-2.md"), "log two\n")
	if err := os.Remove(filepath.Join(proj, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	res, err = SyncAll(ctx, root, projects)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Changed == 0 {
		t.Fatalf("second sync reported no change: %+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "CLAUDE.md")); err == nil {
		t.Error("deleted CLAUDE.md is still in the mirror")
	}
	if _, err := os.Stat(filepath.Join(dir, "files", ".claude-manager", "logs", "Dev-2.md")); err != nil {
		t.Error("new log was not mirrored")
	}
	if got := commitCount(t, root); got != 2 {
		t.Fatalf("commits after change = %d, want 2", got)
	}
	// The project folder itself is never written to.
	if _, err := os.Stat(filepath.Join(proj, ".git")); err == nil {
		t.Error("sync created a repository in the project folder")
	}
}

func TestSyncAll_MissingFolderIsReportedNotFatal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	res, err := SyncAll(context.Background(), root, []config.ProjectConfig{
		{Name: "gone", Path: filepath.Join(root, "nope"), Mirror: true},
	})
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(res) != 1 || res[0].Error == "" {
		t.Fatalf("results = %+v, want a per-project error", res)
	}
}

func TestInsideProject(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "p")
	cases := map[string]bool{
		"":                                 false,
		"STATUS.md":                        true,
		"docs/queue.md":                    true,
		"../elsewhere.md":                  false,
		filepath.Join(proj, "Q.md"):        true,
		filepath.Join(t.TempDir(), "Q.md"): false,
	}
	for in, want := range cases {
		if _, ok := insideProject(proj, in); ok != want {
			t.Errorf("insideProject(%q) = %v, want %v", in, ok, want)
		}
	}
}

func TestCheckoutFromExe(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wails.json"), "{}")
	if got := checkoutFromExe(filepath.Join(root, "build", "bin", "claude-manager.exe")); got != root {
		t.Errorf("checkoutFromExe = %q, want %q", got, root)
	}
	if got := checkoutFromExe(filepath.Join(root, "claude-manager.exe")); got != "" {
		t.Errorf("exe outside build/bin resolved to %q", got)
	}
}
