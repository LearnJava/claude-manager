package experience

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// TestBuildPrimer_Golden locks in the exact rendered text for a fixed input
// with no git repository at ProjectPath (so the git section is deterministic
// — absent) and a previous run recorded in the store.
func TestBuildPrimer_Golden(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	run := &store.SessionRun{Project: "proj", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if err := s.InsertActions([]store.ActionRow{
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 0, Tool: "Edit", Sig: "Edit:internal/foo/*.go", Arg: "internal/foo/bar.go", Timestamp: now},
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 1, Tool: "Write", Sig: "Write:internal/foo/*.go", Arg: "internal/foo/baz.go", Timestamp: now.Add(time.Second)},
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 2, Tool: "Read", Sig: "Read:internal/foo/*.go", Arg: "internal/foo/bar.go", Timestamp: now.Add(2 * time.Second)},
	}); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	got := BuildPrimer(PrimerInput{
		Project:     "proj",
		Session:     "S1",
		ProjectPath: t.TempDir(), // not a git repo -> git section absent
		TaskDesc:    "ROADMAP.md:92 — wire the context primer",
		Gates:       []string{"go build ./...", "go test ./..."},
		Store:       s,
	})

	want := "Current task: ROADMAP.md:92 — wire the context primer\n\n" +
		"Files touched in the previous run:\ninternal/foo/bar.go\ninternal/foo/baz.go\n\n" +
		"Gate commands:\ngo build ./...\ngo test ./..."

	if got != want {
		t.Errorf("BuildPrimer() =\n%q\nwant\n%q", got, want)
	}
}

// TestBuildPrimer_NoPreviousRun checks that an empty store (no prior run for
// this session) simply omits the files section instead of erroring.
func TestBuildPrimer_NoPreviousRun(t *testing.T) {
	s := newTestStore(t)
	got := BuildPrimer(PrimerInput{
		Project:     "proj",
		Session:     "S1",
		ProjectPath: t.TempDir(),
		TaskDesc:    "task",
		Store:       s,
	})
	if strings.Contains(got, "Files touched") {
		t.Errorf("BuildPrimer() = %q, should not mention files with no previous run", got)
	}
	if got != "Current task: task" {
		t.Errorf("BuildPrimer() = %q, want just the task section", got)
	}
}

// TestBuildPrimer_NilStore checks that a nil Store (e.g.
// cmd/playwright-server, which runs with no store at all) skips the files
// section rather than panicking.
func TestBuildPrimer_NilStore(t *testing.T) {
	got := BuildPrimer(PrimerInput{
		Project:     "proj",
		Session:     "S1",
		ProjectPath: t.TempDir(),
		TaskDesc:    "task",
		Gates:       []string{"go vet ./..."},
	})
	want := "Current task: task\n\nGate commands:\ngo vet ./..."
	if got != want {
		t.Errorf("BuildPrimer() = %q, want %q", got, want)
	}
}

// TestBuildPrimer_Empty checks that an input with nothing to report (no task,
// no git, no previous run, no gates) renders to "" rather than stray
// separators.
func TestBuildPrimer_Empty(t *testing.T) {
	s := newTestStore(t)
	got := BuildPrimer(PrimerInput{Project: "proj", Session: "S1", ProjectPath: t.TempDir(), Store: s})
	if got != "" {
		t.Errorf("BuildPrimer() = %q, want empty", got)
	}
}

// TestBuildPrimer_MissingGitDoesNotError exercises a ProjectPath that exists
// but has no .git — gitSection must return "" instead of blocking the
// primer, mirroring "тест, что падение git не ломает старт" (LN-05).
func TestBuildPrimer_MissingGitDoesNotError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	got := BuildPrimer(PrimerInput{Project: "proj", Session: "S1", ProjectPath: t.TempDir(), TaskDesc: "task"})
	if strings.Contains(got, "Git state") {
		t.Errorf("BuildPrimer() = %q, should have no git section for a non-repo path", got)
	}
}

// TestBuildPrimer_GitRepo checks that a real repository does contribute a git
// section, without pinning the exact commit hash (non-deterministic).
func TestBuildPrimer_GitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "initial")

	got := BuildPrimer(PrimerInput{Project: "proj", Session: "S1", ProjectPath: dir, TaskDesc: "task"})
	if !strings.Contains(got, "Git state:") {
		t.Fatalf("BuildPrimer() = %q, want a Git state section", got)
	}
	if !strings.Contains(got, "main branch: main") {
		t.Errorf("BuildPrimer() = %q, want \"main branch: main\"", got)
	}
	if !strings.Contains(got, "remote: none") {
		t.Errorf("BuildPrimer() = %q, want \"remote: none\" (no remote configured)", got)
	}
	if !strings.Contains(got, "current branch: main") {
		t.Errorf("BuildPrimer() = %q, want \"current branch: main\"", got)
	}
	if !strings.Contains(got, "recent commits:") {
		t.Errorf("BuildPrimer() = %q, want \"recent commits:\"", got)
	}
}

// TestTruncateSections_Order checks that truncation drops whole trailing
// (lowest-priority) sections first, and never returns something over max.
func TestTruncateSections_Order(t *testing.T) {
	sections := []string{"AAAA", "BBBB", "CCCC"}
	// "AAAA\n\nBBBB\n\nCCCC" is 16 chars; max=9 must drop CCCC then BBBB,
	// leaving only the highest-priority section.
	got := truncateSections(sections, 9)
	if got != "AAAA" {
		t.Errorf("truncateSections() = %q, want %q (only the highest-priority section fits)", got, "AAAA")
	}

	// "AAAA\n\nBBBB" is exactly 10 chars: CCCC is dropped, the rest fits as-is.
	got = truncateSections(sections, 10)
	if got != "AAAA\n\nBBBB" {
		t.Errorf("truncateSections() = %q, want %q", got, "AAAA\n\nBBBB")
	}

	got = truncateSections(sections, 100)
	if got != "AAAA\n\nBBBB\n\nCCCC" {
		t.Errorf("truncateSections() = %q, want everything to fit", got)
	}
}

// TestTruncateSections_SingleOverlongSection checks that a lone section
// bigger than max is hard-truncated rather than dropped to "" — the
// highest-priority section must never disappear entirely.
func TestTruncateSections_SingleOverlongSection(t *testing.T) {
	got := truncateSections([]string{"0123456789"}, 5)
	if got != "01234" {
		t.Errorf("truncateSections() = %q, want %q", got, "01234")
	}
}

// TestBuildPrimer_RespectsMaxChars feeds an oversized task description and
// checks the whole primer never exceeds MaxPrimerChars.
func TestBuildPrimer_RespectsMaxChars(t *testing.T) {
	huge := strings.Repeat("x", MaxPrimerChars*2)
	got := BuildPrimer(PrimerInput{Project: "proj", Session: "S1", TaskDesc: huge})
	if len(got) > MaxPrimerChars {
		t.Errorf("BuildPrimer() len = %d, want <= %d", len(got), MaxPrimerChars)
	}
}
