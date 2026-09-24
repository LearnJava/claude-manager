package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// buildFakeclaudeForSession compiles cmd/fakeclaude into a temp binary —
// local twin of internal/control/e2e_test.go's buildFakeclaude, needed here
// to drive a real multi-task Run() loop (LEARN-TASKS.md LN-21 regression
// test below needs an actual process per task, not just a stubbed event).
func buildFakeclaudeForSession(t *testing.T) string {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH, skipping: " + err.Error())
	}
	binName := "fakeclaude"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	outPath := filepath.Join(t.TempDir(), binName)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command(goExe, "build", "-o", outPath, "./cmd/fakeclaude/")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	return outPath
}

// TestRun_EvtTaskDone_CarriesFinishedRunsOwnCLISessionID is the LEARN-TASKS.md
// LN-21 regression test for the bug a real corpus measurement traced
// near-zero live-ingest coverage to: Run()'s "completed" case used to rotate
// s.CLISessionID to the NEXT task's fresh id BEFORE emitting EvtTaskDone, so
// SessionManager.finishRun (which reads the id off the event, or previously
// off Session itself) always saw the wrong, not-yet-launched process's id —
// IngestRun's FindTranscript then failed on effectively every completed
// autonomous task. Fixed by rotating only after the emit and carrying the
// finished run's own id on the event (SessionEvent.CLISessionID).
//
// Drives two real fakeclaude processes back to back (AutoRestart, MaxTasks=2)
// and asserts that each EvtTaskDone's CLISessionID matches the id the CLI
// process actually launched with for THAT task (echoed back via its own
// EvtInit.Init.SessionID) — never the next task's.
func TestRun_EvtTaskDone_CarriesFinishedRunsOwnCLISessionID(t *testing.T) {
	claudePath := buildFakeclaudeForSession(t)
	dir := t.TempDir()
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	var (
		launchedIDs []string // launched CLI session ids, in order (EvtInit)
		taskDoneIDs []string // CLISessionID carried on each EvtTaskDone, in order
	)

	s := New(Params{
		ID:          "it/IT",
		ProjectName: "it",
		ProjectPath: dir,
		ClaudePath:  claudePath,
		Config: config.SessionConfig{
			Name:           "IT",
			Model:          "sonnet",
			PermissionMode: "bypassPermissions",
			AutoRestart:    true,
			MaxTasks:       2,
			Prompt:         "hello",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				if ev.Init != nil {
					launchedIDs = append(launchedIDs, ev.Init.SessionID)
				}
			case EvtTaskDone:
				taskDoneIDs = append(taskDoneIDs, ev.CLISessionID)
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)

	if len(launchedIDs) != 2 {
		t.Fatalf("launched %d processes, want 2 (got init session ids: %v)", len(launchedIDs), launchedIDs)
	}
	if len(taskDoneIDs) != 2 {
		t.Fatalf("got %d EvtTaskDone events, want 2 (got: %v)", len(taskDoneIDs), taskDoneIDs)
	}
	for i, want := range launchedIDs {
		if taskDoneIDs[i] != want {
			t.Errorf("task %d: EvtTaskDone.CLISessionID = %q, want %q (the id THAT task's own process launched with) — got the full sequence %v vs launched %v",
				i, taskDoneIDs[i], want, taskDoneIDs, launchedIDs)
		}
	}
	// The two tasks must have used different ids (the rotation itself still
	// happens, just after the emit) — otherwise this test would trivially
	// pass even with the bug present.
	if launchedIDs[0] == launchedIDs[1] {
		t.Fatalf("both tasks launched with the same CLI session id %q — rotation isn't happening, test is not exercising the bug", launchedIDs[0])
	}
}

// A clean exit is not a closed task: when the queue's top pointer is still in
// place after the run and the agent did not end on the continue-session
// marker, the run is recorded unfinished (EvtRunEnd, no EvtTaskDone, no
// tasks_done bump), and after maxUnfinishedStreak such runs in a row the loop
// stops instead of paying for the same task forever.
func TestRun_UnfinishedTaskIsNotCountedAndStreakStops(t *testing.T) {
	claudePath := buildFakeclaudeForSession(t)
	dir := t.TempDir()
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)
	// No git repository: the queue is read off disk, and nothing edits it.
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("# queue\nTASKS.md:7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var runEnds []string
	taskDone := 0
	var logs []string
	s := New(Params{
		ID:          "it/UF",
		ProjectName: "it",
		ProjectPath: dir,
		ClaudePath:  claudePath,
		Config: config.SessionConfig{
			Name:            "UF",
			Model:           "sonnet",
			PermissionMode:  "bypassPermissions",
			AutoRestart:     true,
			StopWhenNoTasks: true,
			TaskSource:      "STATUS-P1.md",
			Prompt:          "hello",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtRunEnd:
				runEnds = append(runEnds, ev.RunStatus)
			case EvtTaskDone:
				taskDone++
			case EvtLog:
				if ev.Entry != nil && ev.Entry.Source == "manager" {
					logs = append(logs, ev.Entry.Message)
				}
			}
		},
	})
	s.retryDelay = 0

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.Run(ctx)

	if taskDone != 0 || s.TasksDone() != 0 {
		t.Errorf("task_done events = %d, tasks_done = %d; want 0 — the task never left the queue", taskDone, s.TasksDone())
	}
	if len(runEnds) != maxUnfinishedStreak {
		t.Fatalf("run_end events = %v, want %d unfinished runs before the loop stops", runEnds, maxUnfinishedStreak)
	}
	for _, st := range runEnds {
		if st != RunStatusUnfinished {
			t.Errorf("run status = %q, want %q", st, RunStatusUnfinished)
		}
	}
	if s.Status() != config.StatusError {
		t.Errorf("status = %v, want error after the unfinished streak", s.Status())
	}
	found := false
	for _, l := range logs {
		if strings.Contains(l, "Task TASKS.md:7 not finished") {
			found = true
		}
	}
	if !found {
		t.Errorf("no 'not finished' line in the log: %v", logs)
	}
}
