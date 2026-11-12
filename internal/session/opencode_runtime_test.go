package session

import (
	"context"
	"encoding/json"
	"errors"
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

func buildFakeopencode(t *testing.T) string {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH: " + err.Error())
	}
	name := "fakeopencode"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(goExe, "build", "-o", out, "./cmd/fakeopencode/")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeopencode: %v\n%s", err, b)
	}
	return out
}

func readFakeOpenCodeLog(t *testing.T, path string) []fakeHermesCall {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fakeopencode log: %v", err)
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

// An autonomous opencode session: two tasks = two fresh conversations, each
// a single `opencode run` process whose step_finish ends the task. The
// first turn carries the ask-user protocol preamble (opencode has no
// --append-system-prompt), token usage from step_finish reaches EvtUsage,
// and every launch is --archived/--auto with the effort as a #variant.
func TestOpenCodeRuntime_AutonomousLoop(t *testing.T) {
	bin := buildFakeopencode(t)
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FAKEOPENCODE_LOG", logPath)

	var mu sync.Mutex
	var inits, dones []string
	var usage []TokenUsage
	s := New(Params{
		ID: "p/OC", ProjectName: "p", ProjectPath: t.TempDir(), OpenCodePath: bin,
		RetryDelay: 1, RateLimitPauseSec: 1,
		Config: config.SessionConfig{
			Name: "OC", Runtime: config.RuntimeOpenCode, Model: "zai-coding-plan/glm-5.3",
			Effort: "high", PermissionMode: "bypassPermissions",
			AutoRestart: true, MaxTasks: 2, Prompt: "hello there",
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

	calls := readFakeOpenCodeLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("launched %d opencode processes, want 2", len(calls))
	}
	for i, c := range calls {
		if !slices.Contains(c.Args, "--auto") || !slices.Contains(c.Args, "--archived") {
			t.Errorf("call %d args = %v, want --auto and --archived", i, c.Args)
		}
		if argValue(c.Args, "--format") != "json" {
			t.Errorf("call %d --format = %q, want json", i, argValue(c.Args, "--format"))
		}
		if model := argValue(c.Args, "--model"); model != "zai-coding-plan/glm-5.3#high" {
			t.Errorf("call %d --model = %q, want zai-coding-plan/glm-5.3#high", i, model)
		}
		// Autonomous tasks are fresh conversations: no --session on either.
		if argValue(c.Args, "--session") != "" {
			t.Errorf("call %d has --session %q, want none", i, argValue(c.Args, "--session"))
		}
		// The first turn of a fresh conversation carries the autonomous
		// preamble (both tasks open fresh conversations here).
		if !strings.Contains(c.Query, "ask-user") || !strings.HasSuffix(c.Query, "hello there") {
			t.Errorf("call %d query must be preamble + prompt, got %q", i, c.Query)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inits) != 2 || len(dones) != 2 || inits[0] == inits[1] {
		t.Fatalf("inits=%v dones=%v, want two distinct conversations", inits, dones)
	}
	for i := range inits {
		if dones[i] != inits[i] {
			t.Errorf("task %d done with id %q, launched as %q", i, dones[i], inits[i])
		}
		if !strings.HasPrefix(inits[i], "ses") {
			t.Errorf("minted session id %q does not start with \"ses\"", inits[i])
		}
	}
	if len(usage) != 2 || usage[0].CacheReadInputTokens != 100 || usage[0].InputTokens != 10 {
		t.Errorf("usage events = %+v, want one per turn from the step_finish tokens", usage)
	}
	if st := s.Status(); st != config.StatusIdle {
		t.Errorf("final status = %s, want idle", st)
	}
}

// An interactive opencode session: turn 1 mints a "ses…" conversation id,
// turn 2 (the user's follow-up from inputCh) resumes the SAME id via
// --session instead of starting over, and a model switch between turns
// takes effect on the next turn without a restart.
func TestOpenCodeRuntime_InteractiveResumesAndSwitchesModel(t *testing.T) {
	bin := buildFakeopencode(t)
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FAKEOPENCODE_LOG", logPath)

	results := make(chan string, 8)
	s := New(Params{
		ID: "p/OC2", ProjectName: "p", ProjectPath: t.TempDir(), OpenCodePath: bin,
		Config: config.SessionConfig{
			Name: "OC2", Runtime: config.RuntimeOpenCode, Model: "zai-coding-plan/glm-5.3", Prompt: "first",
		},
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
	s.SetModel("zai-coding-plan/glm-5.3-flash")
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

	calls := readFakeOpenCodeLog(t, logPath)
	if len(calls) != 3 {
		t.Fatalf("launched %d processes, want 3", len(calls))
	}
	if slices.Contains(calls[0].Args, "--session") {
		t.Errorf("first turn must start a new conversation: %v", calls[0].Args)
	}
	conv := argValue(calls[1].Args, "--session")
	if conv == "" || !strings.HasPrefix(conv, "ses") || argValue(calls[2].Args, "--session") != conv {
		t.Errorf("turns 2 and 3 must resume the same \"ses\" conversation: %v / %v", calls[1].Args, calls[2].Args)
	}
	if argValue(calls[1].Args, "--model") != "zai-coding-plan/glm-5.3" ||
		argValue(calls[2].Args, "--model") != "zai-coding-plan/glm-5.3-flash" {
		t.Errorf("model per turn = %q, %q", argValue(calls[1].Args, "--model"), argValue(calls[2].Args, "--model"))
	}
	if strings.Contains(calls[0].Query, "ask-user") {
		t.Errorf("interactive session must not get the autonomous protocol preamble")
	}
	if calls[1].Query != "second — привет" {
		t.Errorf("second query = %q, want it verbatim", calls[1].Query)
	}
}

// A failed turn (the CLI's error line + exit 1) is classified by the shared
// failure machinery: a 429 message becomes the rate-limit sentinel (the Run
// loop then waits and resumes), not a silent success.
func TestOpenCodeRuntime_FailedTurn(t *testing.T) {
	bin := buildFakeopencode(t)

	s := New(Params{
		ID: "p/OC3", ProjectName: "p", ProjectPath: t.TempDir(), OpenCodePath: bin,
		RetryDelay: 1, RateLimitPauseSec: 1,
		Config: config.SessionConfig{
			Name: "OC3", Runtime: config.RuntimeOpenCode, Model: "bogus/nope",
			Prompt: "FAIL_429",
		},
	})
	err := s.runOnceOpenCode(context.Background(), false)
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("runOnceOpenCode error = %v, want the rate-limit sentinel", err)
	}
}
