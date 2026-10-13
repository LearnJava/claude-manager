package session

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"claude-manager/internal/config"
)

// toolUseLine is an assistant stream-json line with one tool_use block.
func toolUseLine(id, tool string, input map[string]any) string {
	data, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "tool_use", "id": id, "name": tool, "input": input,
			}},
		},
	})
	return string(data)
}

func loopCfg(action string) *config.OptimizationSettings {
	return &config.OptimizationSettings{
		LoopDetection:           true,
		LoopThreshold:           3,
		LoopAction:              action,
		LoopHint:                "Try a different approach.",
		LoopWindow:              20,
		LoopIgnoreReadAfterEdit: true,
	}
}

// loopSession returns a session with a live stdin channel and the EvtLoop
// payloads it emitted.
func loopSession(t *testing.T, action string) (*Session, chan []byte, func() []LoopAlert) {
	t.Helper()
	var mu sync.Mutex
	var alerts []LoopAlert
	s := New(Params{
		ID:           "p/s",
		Config:       config.SessionConfig{Name: "s", Prompt: "Do the task."},
		Optimization: loopCfg(action),
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLoop && ev.Loop != nil {
				mu.Lock()
				alerts = append(alerts, *ev.Loop)
				mu.Unlock()
			}
		},
	})
	in := make(chan []byte, 8)
	s.mu.Lock()
	s.inputCh = in
	s.mu.Unlock()
	return s, in, func() []LoopAlert {
		mu.Lock()
		defer mu.Unlock()
		return append([]LoopAlert(nil), alerts...)
	}
}

func repeatBash(s *Session, n int) {
	for i := 0; i < n; i++ {
		s.handleLine(toolUseLine("t"+string(rune('a'+i)), "Bash", map[string]any{"command": "go test ./..."}), false)
	}
}

func TestLoopGuard_WarnAlertsOnThirdRepeat(t *testing.T) {
	s, in, alerts := loopSession(t, "warn")
	repeatBash(s, 2)
	if len(alerts()) != 0 {
		t.Fatal("two repeats must not alert")
	}
	repeatBash(s, 1)
	got := alerts()
	if len(got) != 1 || got[0].Tool != "Bash" || got[0].Input != "go test ./..." || got[0].Count != 3 || got[0].Action != "warn" {
		t.Fatalf("alerts = %+v, want one Bash warn with count 3", got)
	}
	if len(in) != 0 || s.loopRestartHit.Load() {
		t.Fatal("warn must not touch stdin or restart")
	}
	// The window is cleared after a detection: the next repeat starts over.
	repeatBash(s, 2)
	if len(alerts()) != 1 {
		t.Fatal("the same history must not fire again right away")
	}
}

func TestLoopGuard_DisabledIsNoOp(t *testing.T) {
	cfg := loopCfg("restart")
	cfg.LoopDetection = false
	alerts := 0
	s := New(Params{
		Config:       config.SessionConfig{Name: "s"},
		Optimization: cfg,
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLoop {
				alerts++
			}
		},
	})
	repeatBash(s, 5)
	if alerts != 0 || s.loopRestartHit.Load() {
		t.Fatal("loop_detection = false must do nothing")
	}
}

func TestLoopGuard_ReadingPartsOfAFileIsNotALoop(t *testing.T) {
	s, _, alerts := loopSession(t, "warn")
	for i, off := range []int{1, 200, 400, 600} {
		s.handleLine(toolUseLine("r"+string(rune('a'+i)), "Read",
			map[string]any{"file_path": "big.go", "offset": off, "limit": 200}), false)
	}
	for i, old := range []string{"foo", "bar", "baz"} {
		s.handleLine(toolUseLine("e"+string(rune('a'+i)), "Edit",
			map[string]any{"file_path": "big.go", "old_string": old, "new_string": "x"}), false)
	}
	if got := alerts(); len(got) != 0 {
		t.Fatalf("alerts = %+v, want none for ranged reads and different edits", got)
	}
}

func TestLoopGuard_SendHintWritesToStdin(t *testing.T) {
	s, in, alerts := loopSession(t, "send_hint")
	repeatBash(s, 3)
	if len(alerts()) != 1 {
		t.Fatal("expected an alert")
	}
	select {
	case line := <-in:
		if !strings.Contains(string(line), "Try a different approach.") || !strings.Contains(string(line), "go test ./...") {
			t.Fatalf("hint = %s", line)
		}
	default:
		t.Fatal("expected the hint on stdin")
	}
}

func TestLoopGuard_RestartStopsRunAndNotesTheLoop(t *testing.T) {
	s, _, alerts := loopSession(t, "restart")
	cancelled := false
	s.mu.Lock()
	s.cancelRun = func() { cancelled = true }
	s.mu.Unlock()

	repeatBash(s, 3)
	if len(alerts()) != 1 || !cancelled || !s.loopRestartHit.Load() {
		t.Fatalf("restart: alerts=%d cancelled=%v hit=%v", len(alerts()), cancelled, s.loopRestartHit.Load())
	}
	if err := s.classifySentinelError(); err != errContextRestart {
		t.Fatalf("classifySentinelError = %v, want errContextRestart", err)
	}
	prompt := s.initialPromptText(false)
	if !strings.Contains(prompt, "go test ./...") || !strings.HasSuffix(prompt, "Do the task.") {
		t.Fatalf("prompt = %q, want the loop note before the session prompt", prompt)
	}
	if again := s.initialPromptText(false); again != "Do the task." {
		t.Fatalf("the note must be used once, got %q", again)
	}
	s.resetRunAtomics()
	if s.loopRestartHit.Load() {
		t.Fatal("a new run starts with the flag clear")
	}

	// The run the restart started only warns on a new loop.
	cancelled = false
	repeatBash(s, 3)
	got := alerts()
	if len(got) != 2 || got[1].Action != "warn" || cancelled || s.loopRestartHit.Load() {
		t.Fatalf("second loop: alerts=%+v cancelled=%v, want a warning only", got, cancelled)
	}
	// The run after that may restart again.
	s.resetRunAtomics()
	repeatBash(s, 3)
	if !cancelled {
		t.Fatal("a later run must be able to restart again")
	}
}
