package optimization

import (
	"testing"

	"claude-manager/internal/config"
)

func newCtxCfg() *config.OptimizationSettings {
	return &config.OptimizationSettings{
		ContextWarnThreshold:    0.60,
		ContextRestartThreshold: 0.75,
		ContextRestartMode:      "resume",
	}
}

func TestContextMonitor_NoEventBelowWarn(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	ev := m.Observe(TokenUsageAdapter{InputTokens: 30_000})
	if ev.Type != ContextEventNone {
		t.Fatalf("want none, got %s", ev.Type)
	}
	if ev.Utilization < 0.29 || ev.Utilization > 0.31 {
		t.Fatalf("utilization mismatch: %v", ev.Utilization)
	}
}

func TestContextMonitor_WarnOnceAtThreshold(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	// First crossing the warn line
	ev1 := m.Observe(TokenUsageAdapter{InputTokens: 65_000})
	if ev1.Type != ContextEventWarn {
		t.Fatalf("turn 1: want warn, got %s", ev1.Type)
	}
	// Second observation still in warn zone — must NOT fire again
	ev2 := m.Observe(TokenUsageAdapter{InputTokens: 70_000})
	if ev2.Type != ContextEventNone {
		t.Fatalf("turn 2: want none (already warned), got %s", ev2.Type)
	}
}

func TestContextMonitor_RestartAtRestartThreshold(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	ev := m.Observe(TokenUsageAdapter{InputTokens: 80_000})
	if ev.Type != ContextEventRestart {
		t.Fatalf("want restart, got %s", ev.Type)
	}
	if ev.RestartMode != "resume" {
		t.Fatalf("want mode=resume, got %s", ev.RestartMode)
	}
	// Second observation must NOT fire restart again
	ev2 := m.Observe(TokenUsageAdapter{InputTokens: 90_000})
	if ev2.Type == ContextEventRestart {
		t.Fatalf("want none after restart already fired, got %s", ev2.Type)
	}
}

func TestContextMonitor_RestartModeOffSuppresses(t *testing.T) {
	cfg := newCtxCfg()
	cfg.ContextRestartMode = "off"
	m := NewContextMonitor(cfg, 100_000)
	ev := m.Observe(TokenUsageAdapter{InputTokens: 90_000})
	if ev.Type == ContextEventRestart {
		t.Fatalf("mode=off must suppress restart, got %s", ev.Type)
	}
	if ev.Type != ContextEventWarn {
		t.Fatalf("want warn when restart suppressed, got %s", ev.Type)
	}
}

func TestContextMonitor_NoContextWindow(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 0)
	ev := m.Observe(TokenUsageAdapter{InputTokens: 10_000})
	if ev.Type != ContextEventNone {
		t.Fatalf("want none when window is 0, got %s", ev.Type)
	}
	m.SetContextWindow(100_000)
	ev2 := m.Observe(TokenUsageAdapter{InputTokens: 80_000})
	if ev2.Type != ContextEventRestart {
		t.Fatalf("want restart after SetContextWindow, got %s", ev2.Type)
	}
}

func TestContextMonitor_Reset(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	_ = m.Observe(TokenUsageAdapter{InputTokens: 80_000})
	m.Reset()
	ev := m.Observe(TokenUsageAdapter{InputTokens: 80_000})
	if ev.Type != ContextEventRestart {
		t.Fatalf("want restart after reset, got %s", ev.Type)
	}
}

func TestContextMonitor_NilUsageOrCfgIsSafe(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	if ev := m.Observe(nil); ev.Type != ContextEventNone {
		t.Fatalf("nil usage: want none, got %s", ev.Type)
	}
	m2 := NewContextMonitor(nil, 100_000)
	if ev := m2.Observe(TokenUsageAdapter{InputTokens: 90_000}); ev.Type != ContextEventNone {
		t.Fatalf("nil cfg: want none, got %s", ev.Type)
	}
}

func TestContextMonitor_UtilizationTracksMax(t *testing.T) {
	m := NewContextMonitor(newCtxCfg(), 100_000)
	_ = m.Observe(TokenUsageAdapter{InputTokens: 50_000})
	_ = m.Observe(TokenUsageAdapter{InputTokens: 30_000})
	if u := m.Utilization(); u < 0.49 || u > 0.51 {
		t.Fatalf("utilization should track max; got %v", u)
	}
}

func TestTokenUsageAdapter_Total(t *testing.T) {
	u := TokenUsageAdapter{
		InputTokens:              100,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     5_000,
	}
	if got := u.Total(); got != 5_120 {
		t.Fatalf("want 5120, got %d", got)
	}
}

func TestContextEventTypeString(t *testing.T) {
	cases := map[ContextEventType]string{
		ContextEventNone:    "none",
		ContextEventWarn:    "warn",
		ContextEventRestart: "restart",
		ContextEventType(99): "unknown",
	}
	for t1, want := range cases {
		if got := t1.String(); got != want {
			t.Fatalf("String for %d: want %s got %s", t1, want, got)
		}
	}
}

func TestContextMonitor_RestartTakesPrecedenceOverWarn(t *testing.T) {
	// Jump straight into restart zone on the first observation:
	// the monitor must emit Restart (not Warn first then Restart).
	m := NewContextMonitor(newCtxCfg(), 100_000)
	ev := m.Observe(TokenUsageAdapter{InputTokens: 76_000})
	if ev.Type != ContextEventRestart {
		t.Fatalf("want restart, got %s", ev.Type)
	}
}
