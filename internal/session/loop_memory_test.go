package session

import (
	"strings"
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/optimization"
)

// stubLoopMemory is a LoopMemory with a canned hint.
type stubLoopMemory struct {
	hint     string
	recorded []*optimization.LoopDetected
}

func (m *stubLoopMemory) Hint(string, string) string { return m.hint }
func (m *stubLoopMemory) Record(h *optimization.LoopDetected) {
	m.recorded = append(m.recorded, h)
}

func memLoopSession(t *testing.T, action string, mem optimization.LoopMemory) (*Session, chan []byte, func() []LoopAlert) {
	t.Helper()
	s, in, alerts := loopSession(t, action)
	s.loopMemory = mem
	return s, in, alerts
}

func TestLoopGuard_PastLoopHintReachesTheAgentAndTheLoopIsRecorded(t *testing.T) {
	mem := &stubLoopMemory{hint: "What got past it last time: Edit a.go."}
	s, in, alerts := memLoopSession(t, "send_hint", mem)
	repeatBash(s, 3)
	if len(alerts()) != 1 || len(mem.recorded) != 1 || mem.recorded[0].Input != "go test ./..." {
		t.Fatalf("alerts=%d recorded=%+v", len(alerts()), mem.recorded)
	}
	line := string(<-in)
	if !strings.Contains(line, "Try a different approach.") || !strings.Contains(line, "Edit a.go.") {
		t.Fatalf("hint = %s, want the loop hint and what helped last time", line)
	}
}

func TestLoopGuard_RestartNoteCarriesThePastHint(t *testing.T) {
	s, _, _ := memLoopSession(t, "restart", &stubLoopMemory{hint: "What got past it last time: Edit a.go."})
	s.mu.Lock()
	s.cancelRun = func() {}
	s.mu.Unlock()
	repeatBash(s, 3)
	if p := s.initialPromptText(false); !strings.Contains(p, "Edit a.go.") {
		t.Fatalf("prompt = %q", p)
	}
}

func TestLoopGuard_EmptyHistoryChangesNothing(t *testing.T) {
	plain, plainIn, _ := loopSession(t, "send_hint")
	repeatBash(plain, 3)
	want := <-plainIn

	mem := &stubLoopMemory{}
	s, in, _ := memLoopSession(t, "send_hint", mem)
	repeatBash(s, 3)
	if got := <-in; string(got) != string(want) {
		t.Fatalf("with an empty history the hint changed:\n got %s\nwant %s", got, want)
	}
	if len(mem.recorded) != 1 {
		t.Error("the loop must still be recorded")
	}
}

func TestLoopGuard_FarRepeatAlertsOutsideTheWindow(t *testing.T) {
	mem := &stubLoopMemory{}
	s, _, alerts := memLoopSession(t, "warn", mem)
	s.loopDetector = optimization.NewLoopDetector(&config.OptimizationSettings{
		LoopDetection: true, LoopThreshold: 3, LoopAction: "warn", LoopWindow: 5,
		LoopMemory: true, LoopFarThreshold: 3,
	})
	n := 0
	for round := 0; round < 3; round++ {
		s.handleLine(toolUseLine("r"+string(rune('a'+round)), "Read", map[string]any{"file_path": "x.go"}), false)
		for i := 0; i < 5; i++ {
			n++
			s.handleLine(toolUseLine("f"+string(rune('a'+n)), "Grep", map[string]any{"pattern": string(rune('a' + n))}), false)
		}
	}
	got := alerts()
	if len(got) != 1 || got[0].Tool != "Read" || got[0].Count != 3 {
		t.Fatalf("alerts = %+v, want one far-repeat alert on Read x.go", got)
	}
	if len(mem.recorded) != 1 || !mem.recorded[0].Far {
		t.Errorf("recorded = %+v, want the far repeat", mem.recorded)
	}
}
