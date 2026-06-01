package optimization

import (
	"testing"

	"claude-manager/internal/config"
)

func testOptCfg() *config.OptimizationSettings {
	return &config.OptimizationSettings{
		ContextWarnThreshold:    0.6,
		ContextRestartThreshold: 0.75,
		ContextRestartMode:      "resume",
		LoopDetection:           true,
		LoopThreshold:           3,
		LoopWindow:              10,
		LoopAction:              LoopActionWarn,
	}
}

func TestReporterSnapshotEmpty(t *testing.T) {
	r := NewReporter(nil, nil, nil)
	s := r.Snapshot("proj/S1")
	if s.ContextUtilization != 0 {
		t.Errorf("expected 0 utilization, got %f", s.ContextUtilization)
	}
	if s.CacheEfficiency != 0 {
		t.Errorf("expected 0 cache efficiency, got %f", s.CacheEfficiency)
	}
	if s.LastLoop != nil {
		t.Errorf("expected nil LastLoop")
	}
}

func TestReporterContextUtilization(t *testing.T) {
	cfg := testOptCfg()
	ctx := NewContextMonitor(cfg, 100_000)
	r := NewReporter(ctx, nil, nil)

	ctx.Observe(TokenUsageAdapter{InputTokens: 70_000})
	s := r.Snapshot("x")
	if s.ContextUtilization < 0.69 || s.ContextUtilization > 0.71 {
		t.Errorf("utilization: got %f, want ~0.70", s.ContextUtilization)
	}
	if s.ContextWindowSize != 100_000 {
		t.Errorf("ContextWindowSize: got %d", s.ContextWindowSize)
	}
	if s.ContextMaxTokens != 70_000 {
		t.Errorf("ContextMaxTokens: got %d", s.ContextMaxTokens)
	}
}

func TestReporterCacheStats(t *testing.T) {
	cache := NewCacheTracker(nil)
	cache.Record("proj/S1", 1000, 500, 200, 300)
	cache.Record("proj/S1", 500, 400, 100, 150)

	r := NewReporter(nil, cache, nil)
	s := r.Snapshot("proj/S1")

	if s.Turns != 2 {
		t.Errorf("Turns: got %d want 2", s.Turns)
	}
	if s.InputTokens != 1500 {
		t.Errorf("InputTokens: got %d want 1500", s.InputTokens)
	}
	if s.CacheReadTokens != 900 {
		t.Errorf("CacheReadTokens: got %d want 900", s.CacheReadTokens)
	}
	// efficiency = 900 / (1500 + 900 + 300) = 900/2700 = 1/3
	wantEff := float64(900) / float64(1500+900+300)
	if s.CacheEfficiency < wantEff-0.001 || s.CacheEfficiency > wantEff+0.001 {
		t.Errorf("CacheEfficiency: got %f want %f", s.CacheEfficiency, wantEff)
	}
}

func TestReporterUnknownSessionCacheStats(t *testing.T) {
	cache := NewCacheTracker(nil)
	r := NewReporter(nil, cache, nil)
	s := r.Snapshot("nobody/S99")
	if s.Turns != 0 || s.CacheEfficiency != 0 {
		t.Errorf("expected zero cache stats for unknown session: %+v", s)
	}
}

func TestReporterLoopRecordClear(t *testing.T) {
	r := NewReporter(nil, nil, nil)
	sessionID := "proj/S1"

	loop := &LoopDetected{Tool: "Bash", Input: "ls", Count: 3, Action: LoopActionWarn}
	r.RecordLoop(sessionID, loop)

	s := r.Snapshot(sessionID)
	if s.LastLoop == nil {
		t.Fatal("expected LastLoop != nil after RecordLoop")
	}
	if s.LastLoop.Tool != "Bash" {
		t.Errorf("LastLoop.Tool: got %q", s.LastLoop.Tool)
	}

	r.ClearLoop(sessionID)
	s2 := r.Snapshot(sessionID)
	if s2.LastLoop != nil {
		t.Errorf("expected nil LastLoop after ClearLoop")
	}
}

func TestReporterLoopIsolatedBetweenSessions(t *testing.T) {
	r := NewReporter(nil, nil, nil)
	r.RecordLoop("a/S1", &LoopDetected{Tool: "Read", Input: "x.go", Count: 3})

	s1 := r.Snapshot("a/S1")
	s2 := r.Snapshot("b/S2")
	if s1.LastLoop == nil {
		t.Error("a/S1: expected loop")
	}
	if s2.LastLoop != nil {
		t.Error("b/S2: expected no loop bleed")
	}
}

func TestReporterGlobalReport(t *testing.T) {
	cache := NewCacheTracker(nil)
	cache.Record("a/S1", 1000, 500, 200, 300)
	cache.Record("b/S2", 2000, 1000, 100, 400)

	r := NewReporter(nil, cache, nil)
	g := r.GlobalReport()

	if g.InputTokens != 3000 {
		t.Errorf("GlobalReport InputTokens: got %d want 3000", g.InputTokens)
	}
	if g.CacheReadTokens != 1500 {
		t.Errorf("GlobalReport CacheReadTokens: got %d want 1500", g.CacheReadTokens)
	}
	if g.Turns != 2 {
		t.Errorf("GlobalReport Turns: got %d want 2", g.Turns)
	}
	if g.CacheEfficiency == 0 {
		t.Error("GlobalReport CacheEfficiency should be non-zero")
	}
}

func TestReporterGlobalReportNilCache(t *testing.T) {
	r := NewReporter(nil, nil, nil)
	g := r.GlobalReport()
	if g.Turns != 0 || g.CacheEfficiency != 0 {
		t.Errorf("nil cache should return zero GlobalReport: %+v", g)
	}
}
