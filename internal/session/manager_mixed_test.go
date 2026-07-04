package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/worker"
)

func requireGitForManagerTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// newMixedTestRepo initializes a git repo with one committed file at dir, so
// `git worktree add` has a HEAD to branch from.
func newMixedTestRepo(t *testing.T, relFile, content string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	full := filepath.Join(dir, relFile)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
	return dir
}

// newMixedTestManager builds a SessionManager whose single project points at
// repoPath, mixed_programming enabled, gates checking a.txt for marker, and
// one worker ("step37") pointed at srvURL.
func newMixedTestManager(t *testing.T, repoPath, srvURL string) *SessionManager {
	t.Helper()
	t.Setenv("CM_TEST_WORKER_KEY", "test-key")
	dir := t.TempDir()

	gate := "findstr FIXED a.txt"
	if os.PathSeparator != '\\' {
		gate = "grep -q FIXED a.txt"
	}

	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{
			{
				Name:             "lumen",
				Path:             repoPath,
				MixedProgramming: true,
				Gates:            []string{gate},
				MixedMaxRounds:   3,
			},
		},
		Workers: []config.WorkerConfig{
			{
				Name: "step37", BaseURL: srvURL, Model: "test/model",
				KeyEnv: "CM_TEST_WORKER_KEY", Role: "hands",
				ReasoningEffort: "low", MaxOutputTokens: 1000, ContinuationCap: 3, RequestTimeoutSec: 5,
			},
		},
	}
	return NewSessionManager(cfg, filepath.Join(dir, "config.toml"), nil, nil)
}

func patchSSE(w http.ResponseWriter, file, find, replace string) {
	body := fmt.Sprintf("### PATCH 1\nFILE %s\n<<<FIND\n%s\n===REPLACE\n%s\n>>>END\n", file, find, replace)
	chunk := fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":"stop"}]}`, body)
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	fmt.Fprint(w, "data: [DONE]\n\n")
	w.(http.Flusher).Flush()
}

func TestDispatchMixedTaskProjectNotFound(t *testing.T) {
	m := newMixedTestManager(t, t.TempDir(), "http://example.invalid")
	m.RegisterMixedBrief("b1", worker.Brief{Task: "fix"})
	if _, err := m.DispatchMixedTask("no-such-project", "b1", "step37"); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestDispatchMixedTaskMixedProgrammingDisabled(t *testing.T) {
	repo := t.TempDir()
	m := newMixedTestManager(t, repo, "http://example.invalid")
	m.cfg.Projects[0].MixedProgramming = false
	m.RegisterMixedBrief("b1", worker.Brief{Task: "fix"})
	if _, err := m.DispatchMixedTask("lumen", "b1", "step37"); err == nil {
		t.Fatal("expected error when mixed_programming is disabled")
	}
}

func TestDispatchMixedTaskWorkerNotConfigured(t *testing.T) {
	repo := t.TempDir()
	m := newMixedTestManager(t, repo, "http://example.invalid")
	m.RegisterMixedBrief("b1", worker.Brief{Task: "fix"})
	if _, err := m.DispatchMixedTask("lumen", "b1", "no-such-worker"); err == nil {
		t.Fatal("expected error for unconfigured worker")
	}
}

func TestDispatchMixedTaskBriefNotRegistered(t *testing.T) {
	repo := t.TempDir()
	m := newMixedTestManager(t, repo, "http://example.invalid")
	if _, err := m.DispatchMixedTask("lumen", "no-such-brief", "step37"); err == nil {
		t.Fatal("expected error for unregistered brief")
	}
}

func TestDispatchMixedTaskFullRoundTrip(t *testing.T) {
	requireGitForManagerTest(t)
	repo := newMixedTestRepo(t, "a.txt", "start\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		patchSSE(w, "a.txt", "start", "FIXED")
	}))
	defer srv.Close()

	m := newMixedTestManager(t, repo, srv.URL)
	m.RegisterMixedBrief("b1", worker.Brief{Task: "replace start with FIXED"})

	task, err := m.DispatchMixedTask("lumen", "b1", "step37")
	if err != nil {
		t.Fatalf("DispatchMixedTask: %v", err)
	}
	if task.Status != worker.TaskStatusDone {
		t.Fatalf("Status = %v, want done: %+v", task.Status, task)
	}

	rounds, err := m.GetMixedRounds("lumen")
	if err != nil {
		t.Fatalf("GetMixedRounds: %v", err)
	}
	if len(rounds) != 1 || rounds[0].ID != task.ID {
		t.Fatalf("GetMixedRounds = %+v, want the dispatched task", rounds)
	}
}

func TestGetMixedQuality(t *testing.T) {
	m := newMixedTestManager(t, t.TempDir(), "http://example.invalid")

	// Empty state dir: empty report, no error.
	report, err := m.GetMixedQuality("lumen")
	if err != nil {
		t.Fatalf("GetMixedQuality (empty): %v", err)
	}
	if len(report) != 0 {
		t.Fatalf("empty state: want no entries, got %+v", report)
	}

	// Persist two tasks for the project (and one for another project that
	// must not leak into the report).
	save := func(task *worker.MixedTask) {
		t.Helper()
		if err := m.taskStore.Save(task); err != nil {
			t.Fatalf("taskStore.Save(%s): %v", task.ID, err)
		}
	}
	save(&worker.MixedTask{
		ID: "lumen/b1/step37", Project: "lumen", BriefID: "b1", WorkerName: "step37",
		Status: worker.TaskStatusDone,
		Rounds: []worker.RoundRecord{{Number: 1, Applied: []worker.Patch{{File: "a.txt"}}, Passed: true}},
	})
	save(&worker.MixedTask{
		ID: "lumen/b2/step37", Project: "lumen", BriefID: "b2", WorkerName: "step37",
		Status: worker.TaskStatusNeedsHuman,
		Rounds: []worker.RoundRecord{{Number: 1, ParseError: "missing >>>END"}},
	})
	save(&worker.MixedTask{
		ID: "other/b1/step37", Project: "other", BriefID: "b1", WorkerName: "step37",
		Status: worker.TaskStatusDone,
	})

	report, err = m.GetMixedQuality("lumen")
	if err != nil {
		t.Fatalf("GetMixedQuality: %v", err)
	}
	if len(report) != 1 {
		t.Fatalf("want 1 worker entry, got %+v", report)
	}
	q := report[0]
	if q.Worker != "step37" || q.TasksTotal != 2 || q.TasksDone != 1 || q.TasksNeedsHuman != 1 {
		t.Errorf("aggregate wrong: %+v", q)
	}
	if q.ParseErrors != 1 || q.PatchesApplied != 1 {
		t.Errorf("defects/patches wrong: %+v", q)
	}
}

func TestCancelMixedTaskNotRunning(t *testing.T) {
	m := newMixedTestManager(t, t.TempDir(), "http://example.invalid")
	if err := m.CancelMixedTask("not-a-real-task"); err == nil {
		t.Fatal("expected error cancelling a task that is not running")
	}
}

func TestCancelMixedTaskDuringRun(t *testing.T) {
	requireGitForManagerTest(t)
	repo := newMixedTestRepo(t, "a.txt", "start\n")

	// The handler signals once the worker request is actually in flight, then
	// blocks on a test-controlled channel (not r.Context().Done(): Go's
	// net/http does not reliably cancel a request context just because the
	// client gave up — it depends on the handler itself observing a read/
	// write error). Waiting for the "started" signal, rather than polling
	// CancelMixedTask, avoids a race where cancellation instead lands mid
	// `git worktree add`.
	reqStarted := make(chan struct{}, 1)
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reqStarted <- struct{}{}:
		default:
		}
		<-unblock
	}))
	defer srv.Close()
	defer close(unblock) // let the handler return before srv.Close() waits on it (LIFO: runs first)

	m := newMixedTestManager(t, repo, srv.URL)
	m.RegisterMixedBrief("b1", worker.Brief{Task: "fix"})

	taskID := "lumen/b1/step37"
	done := make(chan struct{})
	var dispatchErr error
	go func() {
		_, dispatchErr = m.DispatchMixedTask("lumen", "b1", "step37")
		close(done)
	}()

	select {
	case <-reqStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("worker request never started")
	}
	if err := m.CancelMixedTask(taskID); err != nil {
		t.Fatalf("CancelMixedTask: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DispatchMixedTask did not return after cancellation")
	}
	if dispatchErr == nil {
		t.Error("expected an error from a cancelled dispatch")
	}
	if !strings.Contains(dispatchErr.Error(), "context canceled") {
		t.Errorf("error = %v, want context canceled", dispatchErr)
	}
}
