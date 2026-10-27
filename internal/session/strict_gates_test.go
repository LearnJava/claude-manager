package session

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/worker"
)

// gateRun is what one strict-gates Run() observed.
type gateRun struct {
	s         *Session
	gateCalls int
	launches  []string // CLI session id of every launched process
	taskDone  int
	runEnds   []string
	errs      []string
	logs      []string
}

// runWithGates drives a real fakeclaude autonomous run (no task queue, so a
// clean exit closes the task) with runGatesFn replaced by results[i] for the
// i-th check; the last result repeats.
func runWithGates(t *testing.T, gateRounds int, gates []string, results []worker.GateResult) *gateRun {
	t.Helper()
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	var calls atomic.Int32
	orig := runGatesFn
	runGatesFn = func(_ context.Context, _ string, _ []string) worker.GateResult {
		i := int(calls.Add(1)) - 1
		if i >= len(results) {
			i = len(results) - 1
		}
		return results[i]
	}
	t.Cleanup(func() { runGatesFn = orig })

	r := &gateRun{}
	r.s = New(Params{
		ID: "it/SG", ProjectName: "it", ProjectPath: t.TempDir(), ClaudePath: claudePath,
		Gates: gates,
		Config: config.SessionConfig{
			Name: "SG", Model: "sonnet", PermissionMode: "bypassPermissions",
			AutoRestart: true, MaxTasks: 1, Prompt: "hello", GateRounds: gateRounds,
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				if ev.Init != nil {
					r.launches = append(r.launches, ev.Init.SessionID)
				}
			case EvtTaskDone:
				r.taskDone++
			case EvtRunEnd:
				r.runEnds = append(r.runEnds, ev.RunStatus)
			case EvtError:
				if ev.Err != nil {
					r.errs = append(r.errs, ev.Err.Error())
				}
			case EvtLog:
				if ev.Entry != nil {
					r.logs = append(r.logs, ev.Entry.Message)
				}
			}
		},
	})
	r.s.retryDelay = 0

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r.s.Run(ctx)
	r.gateCalls = int(calls.Load())
	return r
}

func (r *gateRun) logged(sub string) bool {
	for _, l := range r.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

var (
	greenGates = worker.GateResult{Passed: true, Commands: []worker.GateCommandResult{{Command: "make test"}}}
	redGates   = worker.GateResult{Commands: []worker.GateCommandResult{
		{Command: "make test", Output: "TestSum: got 3, want 4\n", ExitCode: 1}}}
)

// Gates configured but gate_rounds unset: nothing is run, the dialog is the
// one it always was.
func TestStrictGates_OffByDefault(t *testing.T) {
	r := runWithGates(t, 0, []string{"make test"}, []worker.GateResult{redGates})
	if r.gateCalls != 0 {
		t.Errorf("gates ran %d times with gate_rounds = 0", r.gateCalls)
	}
	if len(r.launches) != 1 || r.taskDone != 1 {
		t.Errorf("launches = %d, task_done = %d; want 1 and 1", len(r.launches), r.taskDone)
	}
	if r.logged("Ворота") {
		t.Errorf("gate lines in the log with the feature off: %v", r.logs)
	}
}

// gate_rounds without gates is also off.
func TestStrictGates_NeedsGates(t *testing.T) {
	r := runWithGates(t, 2, nil, []worker.GateResult{redGates})
	if r.gateCalls != 0 || len(r.launches) != 1 || r.taskDone != 1 {
		t.Errorf("gate calls = %d, launches = %d, task_done = %d; want 0, 1, 1", r.gateCalls, len(r.launches), r.taskDone)
	}
}

func TestStrictGates_GreenFirstTime(t *testing.T) {
	r := runWithGates(t, 2, []string{"make test"}, []worker.GateResult{greenGates})
	if r.gateCalls != 1 || len(r.launches) != 1 || r.taskDone != 1 {
		t.Errorf("gate calls = %d, launches = %d, task_done = %d; want 1, 1, 1", r.gateCalls, len(r.launches), r.taskDone)
	}
}

// Red, then green: the failure goes back as one more turn, and the task is counted once, after the second check.
func TestStrictGates_RedThenGreen(t *testing.T) {
	r := runWithGates(t, 2, []string{"make test"}, []worker.GateResult{redGates, greenGates})
	if r.gateCalls != 2 {
		t.Errorf("gate calls = %d, want 2", r.gateCalls)
	}
	if len(r.launches) != 2 {
		t.Fatalf("launches = %v, want 2 processes (task + one rework round)", r.launches)
	}
	if r.taskDone != 1 || r.s.TasksDone() != 1 {
		t.Errorf("task_done = %d, tasks_done = %d; want 1", r.taskDone, r.s.TasksDone())
	}
	if !r.logged("Reworking after the gate report") {
		t.Errorf("the rework turn never reached the agent (its scenario did not match the feedback prompt): %q", r.logs)
	}
	if len(r.errs) != 0 {
		t.Errorf("unexpected errors: %v", r.errs)
	}
}

// Always red: stops after exactly gate_rounds rework rounds with an explicit
// reason, the task is not counted and the failing output is in the log.
func TestStrictGates_StopsAtRoundLimit(t *testing.T) {
	r := runWithGates(t, 2, []string{"make test"}, []worker.GateResult{redGates})
	if r.gateCalls != 3 {
		t.Errorf("gate calls = %d, want 3 (initial + 2 rework rounds)", r.gateCalls)
	}
	if len(r.launches) != 3 {
		t.Errorf("launches = %d, want 3", len(r.launches))
	}
	if r.taskDone != 0 || r.s.TasksDone() != 0 {
		t.Errorf("task_done = %d, tasks_done = %d; a task with red gates must not count", r.taskDone, r.s.TasksDone())
	}
	if r.s.Status() != config.StatusError {
		t.Errorf("status = %v, want error", r.s.Status())
	}
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "2 rework rounds") {
		t.Errorf("errors = %v, want one naming the 2 rework rounds", r.errs)
	}
	if len(r.runEnds) != 1 || r.runEnds[0] != RunStatusUnfinished {
		t.Errorf("run ends = %v, want one %q", r.runEnds, RunStatusUnfinished)
	}
	if !r.logged("TestSum: got 3, want 4") {
		t.Errorf("the failing gate output is not in the session log: %v", r.logs)
	}
}

// The prompt of a rework round is the gates' own Feedback() text, verbatim,
// and it is consumed once, ahead of the crash-recovery prompt a resumed run
// would otherwise get.
func TestStrictGates_FeedbackIsThePrompt(t *testing.T) {
	s := New(Params{
		ID: "it/FB", ProjectName: "it", ProjectPath: t.TempDir(), ClaudePath: "claude",
		Gates:  []string{"make test"},
		Config: config.SessionConfig{Name: "FB", AutoRestart: true, GateRounds: 3, CrashRecoveryPrompt: "recover"},
	})
	s.CLISessionID = "abc"
	round := 0
	runGatesFn = func(context.Context, string, []string) worker.GateResult { return redGates }
	t.Cleanup(func() { runGatesFn = worker.RunGates })

	if v := s.checkStrictGates(context.Background(), &round); v != gatesRework || round != 1 {
		t.Fatalf("verdict = %v, round = %d; want rework, 1", v, round)
	}
	if s.resumeSessionID != "abc" {
		t.Errorf("resumeSessionID = %q, want the finished run's id", s.resumeSessionID)
	}
	got := s.initialPromptBody(false)
	if !strings.Contains(got, redGates.Feedback()) {
		t.Errorf("prompt does not carry Feedback() verbatim:\n%s", got)
	}
	if !strings.Contains(got, "rework round 1 of 3") {
		t.Errorf("prompt lacks the round counter:\n%s", got)
	}
	if again := s.initialPromptBody(false); again != "recover" {
		t.Errorf("feedback was not consumed: next prompt = %q", again)
	}
}
