package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setupRepoWithFile initializes a git repo at dir with one committed file, so
// `git worktree add` has a HEAD to branch from.
func setupRepoWithFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	initGitRepo(t, dir)
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
}

// patchResponse builds a single-patch SSE reply body for the given file/find/replace.
func patchBody(file, find, replace string) string {
	return fmt.Sprintf("### PATCH 1\nFILE %s\n<<<FIND\n%s\n===REPLACE\n%s\n>>>END\n", file, find, replace)
}

// markerGateCmd returns a portable command that exits 0 iff relFile contains
// marker (a single plain word, no spaces/regex metacharacters needed).
func markerGateCmd(relFile, marker string) string {
	if isWindows() {
		return fmt.Sprintf("findstr %s %s", marker, relFile)
	}
	return fmt.Sprintf("grep -q %s %s", marker, relFile)
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}

// --- happy path: green on round 1 ---

func TestRunTaskGreenFirstRound(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, patchBody("a.txt", "start", "FIXED"), "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	client := NewClient(cfg, "key")
	client.sleep = noSleep

	worktreeDir := t.TempDir()
	orch := &RoundOrchestrator{
		Client:      client,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   3,
		WorktreeDir: worktreeDir,
	}
	task := &MixedTask{ID: "t1", Project: "proj", BriefID: "b1", WorkerName: "step37"}
	brief := Brief{ID: "b1", Task: "replace start with FIXED in a.txt"}

	if err := orch.RunTask(context.Background(), repo, task, brief); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if task.Status != TaskStatusDone {
		t.Fatalf("Status = %v, want done", task.Status)
	}
	if len(task.Rounds) != 1 || !task.Rounds[0].Passed {
		t.Fatalf("Rounds = %+v, want 1 passed round", task.Rounds)
	}
	got, err := os.ReadFile(filepath.Join(task.WorktreePath, "a.txt"))
	// git on Windows may check the worktree out with CRLF (core.autocrlf);
	// only the content matters here, not the line ending git chose.
	if err != nil || strings.TrimSpace(string(got)) != "FIXED" {
		t.Fatalf("worktree file = %q, err=%v", got, err)
	}

	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = task.WorktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "Co-Authored-By: step37") {
		t.Errorf("commit missing trailer: %s", out)
	}
}

// --- red then green across rounds ---

func TestRunTaskRedThenGreenAcrossRounds(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			sseFrame(w, patchBody("a.txt", "start", "still broken"), "stop")
		} else {
			sseFrame(w, patchBody("a.txt", "still broken", "FIXED"), "stop")
		}
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	client := NewClient(cfg, "key")
	client.sleep = noSleep

	tasks := NewTaskStore(t.TempDir())
	orch := &RoundOrchestrator{
		Client:      client,
		Tasks:       tasks,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   3,
		WorktreeDir: t.TempDir(),
	}
	task := &MixedTask{ID: "t2", Project: "proj", BriefID: "b2", WorkerName: "step37"}
	brief := Brief{ID: "b2", Task: "make a.txt contain FIXED"}

	if err := orch.RunTask(context.Background(), repo, task, brief); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if task.Status != TaskStatusDone {
		t.Fatalf("Status = %v, want done. Rounds=%+v", task.Status, task.Rounds)
	}
	if len(task.Rounds) != 2 {
		t.Fatalf("Rounds = %d, want 2", len(task.Rounds))
	}
	if task.Rounds[0].Passed {
		t.Error("round 1 should have failed the gate")
	}
	if task.Rounds[0].FeedbackSent == "" {
		t.Error("round 1 must record the feedback sent to the model")
	}
	if !task.Rounds[1].Passed {
		t.Error("round 2 should have passed")
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Errorf("worker calls = %d, want 2", call)
	}
}

// --- parse failure feeds back and retries ---

func TestRunTaskParseFailureFeedsBack(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			sseFrame(w, "sorry, I can't do that right now", "stop")
		} else {
			sseFrame(w, patchBody("a.txt", "start", "FIXED"), "stop")
		}
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	client := NewClient(cfg, "key")
	client.sleep = noSleep

	orch := &RoundOrchestrator{
		Client:      client,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   3,
		WorktreeDir: t.TempDir(),
	}
	task := &MixedTask{ID: "t3", Project: "proj", BriefID: "b3", WorkerName: "step37"}
	brief := Brief{ID: "b3", Task: "fix a.txt"}

	if err := orch.RunTask(context.Background(), repo, task, brief); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if task.Status != TaskStatusDone || len(task.Rounds) != 2 {
		t.Fatalf("Status=%v Rounds=%+v, want done/2", task.Status, task.Rounds)
	}
	if task.Rounds[0].ParseError == "" {
		t.Error("round 1 should record a parse error")
	}
}

// --- exhausts rounds -> needs_human ---

func TestRunTaskExhaustsRoundsToNeedsHuman(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, patchBody("a.txt", "start", "still broken"), "stop")
		writeDone(w)
	}))
	defer srv.Close()

	cfg := testWorkerConfig(srv.URL)
	client := NewClient(cfg, "key")
	client.sleep = noSleep

	orch := &RoundOrchestrator{
		Client:      client,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   2,
		WorktreeDir: t.TempDir(),
	}
	task := &MixedTask{ID: "t4", Project: "proj", BriefID: "b4", WorkerName: "step37"}
	brief := Brief{ID: "b4", Task: "fix a.txt"}

	if err := orch.RunTask(context.Background(), repo, task, brief); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if task.Status != TaskStatusNeedsHuman {
		t.Fatalf("Status = %v, want needs_human", task.Status)
	}
	if len(task.Rounds) != 2 {
		t.Fatalf("Rounds = %d, want 2 (MaxRounds)", len(task.Rounds))
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Errorf("worker calls = %d, want 2", call)
	}

	// no commit was made: HEAD must still be the initial commit
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = task.WorktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n") + 1; lines != 1 {
		t.Errorf("worktree has %d commits, want 1 (no commit on failure): %s", lines, out)
	}
}

// --- resume from persisted rounds ---

func TestRunTaskResumesFromPersistedRounds(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	// First orchestrator: only round 1 (gate fails), simulating a crash right
	// after round 1 was recorded.
	var call1 int32
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&call1, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, patchBody("a.txt", "start", "still broken"), "stop")
		writeDone(w)
	}))
	defer srv1.Close()

	cfg1 := testWorkerConfig(srv1.URL)
	client1 := NewClient(cfg1, "key")
	client1.sleep = noSleep

	tasks := NewTaskStore(t.TempDir())
	worktreeDir := t.TempDir()

	orch1 := &RoundOrchestrator{
		Client: client1, Tasks: tasks,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   5, // won't exhaust — we stop it artificially after round 1
		WorktreeDir: worktreeDir,
	}
	task := &MixedTask{ID: "t5", Project: "proj", BriefID: "b5", WorkerName: "step37"}
	brief := Brief{ID: "b5", Task: "fix a.txt"}

	// Manually drive one round via a MaxRounds=1 orchestrator to simulate the
	// process stopping after exactly one round without reaching a terminal
	// commit or needs_human state we'd otherwise have to unwind.
	orch1.MaxRounds = 1
	task.MaxRounds = 1
	if err := orch1.RunTask(context.Background(), repo, task, brief); err != nil {
		t.Fatalf("RunTask (round 1): %v", err)
	}
	if task.Status != TaskStatusNeedsHuman || len(task.Rounds) != 1 {
		t.Fatalf("after round 1: Status=%v Rounds=%d, want needs_human/1", task.Status, len(task.Rounds))
	}
	branchAfterRound1 := task.Branch
	worktreeAfterRound1 := task.WorktreePath

	// Reload as a fresh process would (from disk), raise MaxRounds, and
	// resume with a server that now fixes the file.
	reloaded, err := tasks.Load(task.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("Load: (%v, %v)", reloaded, err)
	}
	reloaded.Status = TaskStatusRunning
	reloaded.MaxRounds = 3

	var call2 int32
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&call2, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, patchBody("a.txt", "still broken", "FIXED"), "stop")
		writeDone(w)
	}))
	defer srv2.Close()
	cfg2 := testWorkerConfig(srv2.URL)
	client2 := NewClient(cfg2, "key")
	client2.sleep = noSleep

	orch2 := &RoundOrchestrator{
		Client: client2, Tasks: tasks,
		Gates:       []string{markerGateCmd("a.txt", "FIXED")},
		MaxRounds:   3,
		WorktreeDir: worktreeDir,
	}
	if err := orch2.RunTask(context.Background(), repo, reloaded, brief); err != nil {
		t.Fatalf("RunTask (resume): %v", err)
	}
	if reloaded.Status != TaskStatusDone {
		t.Fatalf("Status = %v, want done", reloaded.Status)
	}
	if len(reloaded.Rounds) != 2 {
		t.Fatalf("Rounds = %d, want 2 total (1 resumed + 1 new)", len(reloaded.Rounds))
	}
	if reloaded.Branch != branchAfterRound1 || reloaded.WorktreePath != worktreeAfterRound1 {
		t.Error("resume must reuse the existing worktree/branch, not create a new one")
	}
	if atomic.LoadInt32(&call2) != 1 {
		t.Errorf("resumed run made %d worker calls, want 1 (round 1 must not be resent)", call2)
	}
}

// --- pure helpers ---

func TestBuildMessagesReconstruction(t *testing.T) {
	brief := Brief{Task: "do the thing"}
	rounds := []RoundRecord{
		{RawOutput: "patch v1", FeedbackSent: "gate failed: X"},
		{RawOutput: "patch v2"}, // final round, no feedback sent
	}
	msgs := buildMessages(brief, rounds)
	wantRoles := []string{"system", "user", "assistant", "user", "assistant"}
	if len(msgs) != len(wantRoles) {
		t.Fatalf("len = %d, want %d: %+v", len(msgs), len(wantRoles), msgs)
	}
	for i, role := range wantRoles {
		if msgs[i].Role != role {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, role)
		}
	}
	if msgs[1].Content != "do the thing" {
		t.Errorf("brief task not in message 1: %+v", msgs[1])
	}
	if msgs[4].Content != "patch v2" {
		t.Errorf("last round output missing: %+v", msgs[4])
	}
}

func TestWorktreeBranchNameSanitizesAndFormats(t *testing.T) {
	now := time.Date(2026, 7, 3, 14, 5, 9, 0, time.UTC)
	got := worktreeBranchName(now, "lumen/task 1", "step 3.7!")
	want := "mp-lumen-task-1-step-3.7-140509"
	if got != want {
		t.Errorf("branch = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, " /!") {
		t.Errorf("branch contains unsafe characters: %q", got)
	}
}

func TestRejectionFeedbackIncludesReasons(t *testing.T) {
	fb := rejectionFeedback([]RejectedPatch{
		{Patch: Patch{Index: 1, File: "a.go"}, Reason: "PATCH 1 (a.go): FIND not found verbatim"},
	})
	if !strings.Contains(fb, "FIND not found verbatim") {
		t.Errorf("feedback missing reason: %s", fb)
	}
}

// --- TaskStore ---

func TestTaskStoreSaveLoadRoundtrip(t *testing.T) {
	s := NewTaskStore(t.TempDir())
	task := &MixedTask{ID: "proj/b1/step37", Project: "proj", Status: TaskStatusRunning, Rounds: []RoundRecord{{Number: 1}}}
	if err := s.Save(task); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.Load(task.ID)
	if err != nil || loaded == nil {
		t.Fatalf("Load: (%v, %v)", loaded, err)
	}
	if loaded.Project != "proj" || len(loaded.Rounds) != 1 {
		t.Errorf("loaded = %+v", loaded)
	}
}

func TestTaskStoreListForProject(t *testing.T) {
	s := NewTaskStore(t.TempDir())
	s.Save(&MixedTask{ID: "a", Project: "proj-x"})
	s.Save(&MixedTask{ID: "b", Project: "proj-x"})
	s.Save(&MixedTask{ID: "c", Project: "proj-y"})

	got, err := s.ListForProject("proj-x")
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(got), got)
	}
}

func TestTaskStoreListForProjectEmptyDir(t *testing.T) {
	s := NewTaskStore(filepath.Join(t.TempDir(), "does-not-exist"))
	got, err := s.ListForProject("anything")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

// --- concurrency sanity: two independent tasks don't interfere ---

func TestRunTaskConcurrentTasksIndependentWorktrees(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	setupRepoWithFile(t, repo, "a.txt", "start\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, patchBody("a.txt", "start", "FIXED"), "stop")
		writeDone(w)
	}))
	defer srv.Close()

	worktreeDir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := testWorkerConfig(srv.URL)
			cfg.KeyEnv = fmt.Sprintf("CONCURRENT_KEY_%d", i)
			client := NewClient(cfg, "key")
			client.sleep = noSleep
			orch := &RoundOrchestrator{
				Client: client, Gates: []string{markerGateCmd("a.txt", "FIXED")},
				MaxRounds: 3, WorktreeDir: worktreeDir,
				now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC) },
			}
			task := &MixedTask{ID: fmt.Sprintf("t-%d", i), Project: "proj", BriefID: fmt.Sprintf("b-%d", i), WorkerName: "step37"}
			errs[i] = orch.RunTask(context.Background(), repo, task, Brief{ID: task.BriefID, Task: "fix"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("task %d: %v", i, err)
		}
	}
}
