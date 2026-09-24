package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func buildFakehermes(t *testing.T) string {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH: " + err.Error())
	}
	name := "fakehermes"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(goExe, "build", "-o", out, "./cmd/fakehermes/")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakehermes: %v\n%s", err, b)
	}
	return out
}

type fakeHermesCall struct {
	Args  []string `json:"args"`
	Query string   `json:"query"`
}

func readFakeHermesLog(t *testing.T, path string) []fakeHermesCall {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fakehermes log: %v", err)
	}
	var calls []fakeHermesCall
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var c fakeHermesCall
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			t.Fatalf("log line %q: %v", l, err)
		}
		calls = append(calls, c)
	}
	return calls
}

func argValue(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// An autonomous hermes session: two tasks = two fresh conversations, each a
// single `hermes chat` process whose result finishes the task. The first
// turn carries the ask-user protocol preamble (Hermes has no
// --append-system-prompt), and token usage from the result reaches EvtUsage.
func TestHermesRuntime_AutonomousLoop(t *testing.T) {
	bin := buildFakehermes(t)
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FAKEHERMES_LOG", logPath)

	var mu sync.Mutex
	var inits, dones []string
	var usage []TokenUsage
	s := New(Params{
		ID: "p/H", ProjectName: "p", ProjectPath: t.TempDir(), HermesPath: bin,
		Config: config.SessionConfig{
			Name: "H", Runtime: "hermes", Model: "some/model", PermissionMode: "bypassPermissions",
			AutoRestart: true, MaxTasks: 2, Prompt: "do the task",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Type {
			case EvtInit:
				inits = append(inits, ev.Init.SessionID)
			case EvtTaskDone:
				dones = append(dones, ev.CLISessionID)
			case EvtUsage:
				usage = append(usage, *ev.Usage)
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)

	calls := readFakeHermesLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("launched %d hermes processes, want 2", len(calls))
	}
	for i, c := range calls {
		if c.Args[0] != "chat" || argValue(c.Args, "--query-file") != "-" ||
			argValue(c.Args, "--format") != "stream-json" || argValue(c.Args, "-m") != "some/model" ||
			!slices.Contains(c.Args, "--yolo") {
			t.Errorf("call %d args = %v", i, c.Args)
		}
		if slices.Contains(c.Args, "--resume") {
			t.Errorf("call %d: each task must be a fresh conversation, got --resume in %v", i, c.Args)
		}
		if !strings.Contains(c.Query, "ask-user") || !strings.HasSuffix(c.Query, "do the task") {
			t.Errorf("call %d query must be preamble + prompt, got %q", i, c.Query)
		}
	}
	if len(inits) != 2 || len(dones) != 2 || inits[0] == inits[1] {
		t.Fatalf("inits=%v dones=%v, want two distinct conversations", inits, dones)
	}
	for i := range inits {
		if dones[i] != inits[i] {
			t.Errorf("task %d done with id %q, launched as %q", i, dones[i], inits[i])
		}
	}
	if len(usage) != 2 || usage[0].CacheReadInputTokens != 100 || usage[0].InputTokens != 10 {
		t.Errorf("usage events = %+v, want one per turn from the result tokens", usage)
	}
	if st := s.Status(); st != config.StatusIdle {
		t.Errorf("final status = %s, want idle", st)
	}
}

// An interactive hermes session: every user message is a new process that
// resumes the conversation id the first turn's init reported, and a model
// switch between turns takes effect on the next turn without a restart.
func TestHermesRuntime_InteractiveResumesAndSwitchesModel(t *testing.T) {
	bin := buildFakehermes(t)
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FAKEHERMES_LOG", logPath)

	results := make(chan string, 8)
	s := New(Params{
		ID: "p/C", ProjectName: "p", ProjectPath: t.TempDir(), HermesPath: bin,
		Config: config.SessionConfig{Name: "C", Runtime: "hermes", Model: "m1", Prompt: "first"},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtResult {
				results <- ev.Result.ResultText
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	wait := func(want string) {
		t.Helper()
		select {
		case got := <-results:
			if got != want {
				t.Fatalf("result = %q, want %q", got, want)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	wait("echo: first")
	if err := s.SendMessage("second — привет"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	wait("echo: second — привет")
	s.SetModel("m2")
	if err := s.SendMessage("third"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	wait("echo: third")

	s.Stop(false)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not exit after Stop")
	}

	calls := readFakeHermesLog(t, logPath)
	if len(calls) != 3 {
		t.Fatalf("launched %d processes, want 3", len(calls))
	}
	if slices.Contains(calls[0].Args, "--resume") {
		t.Errorf("first turn must start a new conversation: %v", calls[0].Args)
	}
	conv := argValue(calls[1].Args, "--resume")
	if conv == "" || argValue(calls[2].Args, "--resume") != conv {
		t.Errorf("turns 2 and 3 must resume the same conversation: %v / %v", calls[1].Args, calls[2].Args)
	}
	if argValue(calls[1].Args, "-m") != "m1" || argValue(calls[2].Args, "-m") != "m2" {
		t.Errorf("model per turn = %q, %q; want m1, m2", argValue(calls[1].Args, "-m"), argValue(calls[2].Args, "-m"))
	}
	if strings.Contains(calls[0].Query, "ask-user") {
		t.Errorf("interactive session must not get the autonomous protocol preamble")
	}
	if calls[1].Query != "second — привет" {
		t.Errorf("second query = %q, want it verbatim", calls[1].Query)
	}
}

// A failed result carrying a 429 is a rate limit, not a generic error.
func TestHermesRuntime_RateLimitResult(t *testing.T) {
	bin := buildFakehermes(t)
	var limited bool
	s := New(Params{
		ID: "p/R", ProjectName: "p", ProjectPath: t.TempDir(), HermesPath: bin,
		Config: config.SessionConfig{Name: "R", Runtime: "hermes", Model: "m", Prompt: "FAIL_429",
			StopWhenNoTasks: false, AutoRestart: false},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtRateLimit {
				limited = true
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.runOnce(ctx, false)
	if err != errRateLimited {
		t.Fatalf("runOnce err = %v, want errRateLimited", err)
	}
	if !limited {
		t.Errorf("no EvtRateLimit emitted")
	}
}

// The S6 incident, end to end: a soft stop requested mid-task, then the
// usage limit runs out. Hermes reports only the rotated-to credential's 401,
// with the 429 visible in its agent.log alone. The session must treat it as
// a rate limit (not a generic error), NOT let the pending soft stop end it
// there, and continue the SAME conversation once the limit resets — only
// then, at the task's end, does the soft stop take effect.
func TestHermesRuntime_RateLimitBehind401_ResumesDespiteSoftStop(t *testing.T) {
	bin := buildFakehermes(t)
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	t.Setenv("FAKEHERMES_FAIL_429_THEN_401", "1")
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FAKEHERMES_LOG", logPath)

	var mu sync.Mutex
	var limited, errs, dones int
	var logs []string
	var s *Session
	s = New(Params{
		ID: "p/L", ProjectName: "p", ProjectPath: t.TempDir(), HermesPath: bin,
		RateLimitPauseSec: 1,
		Config: config.SessionConfig{Name: "L", Runtime: "hermes", Model: "m",
			Prompt: "do the task", AutoRestart: true},
		OnEvent: func(_ string, ev SessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Type {
			case EvtRateLimit:
				limited++
			case EvtError:
				errs++
			case EvtTaskDone:
				dones++
			case EvtInit:
				// The user clicks "stop after task" while the task runs.
				s.Stop(true)
			case EvtLog:
				if ev.Entry != nil && ev.Entry.Source == "manager" {
					logs = append(logs, ev.Entry.Message)
				}
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)

	calls := readFakeHermesLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("launched %d hermes processes, want 2 (the failed turn, then its resumption): %+v", len(calls), calls)
	}
	if slices.Contains(calls[0].Args, "--resume") {
		t.Errorf("first turn must start fresh: %v", calls[0].Args)
	}
	if argValue(calls[1].Args, "--resume") == "" {
		t.Errorf("the turn after the rate limit must resume the same conversation: %v", calls[1].Args)
	}
	mu.Lock()
	defer mu.Unlock()
	if limited == 0 {
		t.Errorf("the 429 behind the 401 was not treated as a rate limit (logs: %v)", logs)
	}
	if dones != 1 {
		t.Errorf("task_done events = %d, want 1 — the task finishes after the limit resets", dones)
	}
	if st := s.Status(); st != config.StatusIdle {
		t.Errorf("final status = %s, want idle (soft stop honoured at the task's end)", st)
	}
}
