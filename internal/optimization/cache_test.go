package optimization

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func TestCacheStats_Efficiency(t *testing.T) {
	s := CacheStats{InputTokens: 100, CacheReadTokens: 800, CacheCreationTokens: 100}
	got := s.Efficiency()
	if got < 0.79 || got > 0.81 {
		t.Fatalf("want ~0.80, got %v", got)
	}
}

func TestCacheStats_EfficiencyEmpty(t *testing.T) {
	if got := (CacheStats{}).Efficiency(); got != 0 {
		t.Fatalf("empty stats: want 0, got %v", got)
	}
}

func TestCacheTracker_RecordPerSessionAndGlobal(t *testing.T) {
	tr := NewCacheTracker(nil)
	tr.Record("lumen/P1", 100, 900, 0, 50)
	tr.Record("lumen/P1", 50, 950, 0, 25)
	tr.Record("lumen/P2", 200, 0, 800, 100) // cache miss-y session

	p1, ok := tr.SessionStats("lumen/P1")
	if !ok {
		t.Fatal("expected P1 to be recorded")
	}
	if p1.Turns != 2 {
		t.Fatalf("P1 turns: want 2, got %d", p1.Turns)
	}
	if p1.CacheReadTokens != 1850 {
		t.Fatalf("P1 cache reads: want 1850, got %d", p1.CacheReadTokens)
	}

	if eff := tr.SessionEfficiency("lumen/P2"); eff != 0 {
		t.Fatalf("P2 has zero cache reads: want 0 efficiency, got %v", eff)
	}

	g := tr.Global()
	if g.Turns != 3 {
		t.Fatalf("global turns: want 3, got %d", g.Turns)
	}
	wantGlobal := float64(1850) / float64(350+1850+800)
	if got := tr.GlobalEfficiency(); !approxEq(got, wantGlobal) {
		t.Fatalf("global eff: want %v, got %v", wantGlobal, got)
	}
}

func TestCacheTracker_UnknownSession(t *testing.T) {
	tr := NewCacheTracker(nil)
	if _, ok := tr.SessionStats("nope"); ok {
		t.Fatal("want ok=false for unknown session")
	}
	if eff := tr.SessionEfficiency("nope"); eff != 0 {
		t.Fatalf("want 0 for unknown session, got %v", eff)
	}
}

func TestCacheTracker_ResetSessions(t *testing.T) {
	tr := NewCacheTracker(nil)
	tr.Record("s", 10, 90, 0, 5)
	tr.ResetSessions()
	if _, ok := tr.SessionStats("s"); ok {
		t.Fatal("sessions should be cleared")
	}
	if g := tr.Global(); g.Turns != 1 {
		t.Fatalf("global should be preserved across ResetSessions, got %+v", g)
	}
}

func TestCacheTracker_StartProjectOptimized_NoDelay(t *testing.T) {
	tr := NewCacheTracker(&config.OptimizationSettings{SessionStartDelay: 0})
	var calls int32
	starts := []func() error{
		func() error { atomic.AddInt32(&calls, 1); return nil },
		func() error { atomic.AddInt32(&calls, 1); return nil },
		func() error { atomic.AddInt32(&calls, 1); return nil },
	}
	start := time.Now()
	if err := tr.StartProjectOptimized(context.Background(), starts); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("zero delay should be near-instant, took %v", elapsed)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("want 3 starts, got %d", calls)
	}
}

func TestCacheTracker_StartProjectOptimized_DelayBetween(t *testing.T) {
	// Use 1 second delay so the test runs in ~2s for 3 sessions.
	tr := NewCacheTracker(&config.OptimizationSettings{SessionStartDelay: 1})
	var times []time.Time
	mkStart := func() func() error {
		return func() error {
			times = append(times, time.Now())
			return nil
		}
	}
	starts := []func() error{mkStart(), mkStart(), mkStart()}
	if err := tr.StartProjectOptimized(context.Background(), starts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(times) != 3 {
		t.Fatalf("want 3 invocations, got %d", len(times))
	}
	gap1 := times[1].Sub(times[0])
	gap2 := times[2].Sub(times[1])
	if gap1 < 900*time.Millisecond || gap2 < 900*time.Millisecond {
		t.Fatalf("expected ~1s gaps, got %v and %v", gap1, gap2)
	}
}

func TestCacheTracker_StartProjectOptimized_CancelStops(t *testing.T) {
	tr := NewCacheTracker(&config.OptimizationSettings{SessionStartDelay: 60})
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	starts := []func() error{
		func() error { atomic.AddInt32(&calls, 1); return nil },
		func() error { atomic.AddInt32(&calls, 1); return nil },
		func() error { atomic.AddInt32(&calls, 1); return nil },
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := tr.StartProjectOptimized(ctx, starts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("only first start should run before cancel, got %d", calls)
	}
}

func TestCacheTracker_StartProjectOptimized_PropagatesError(t *testing.T) {
	tr := NewCacheTracker(&config.OptimizationSettings{SessionStartDelay: 0})
	boom := errors.New("boom")
	var calls int32
	starts := []func() error{
		func() error { atomic.AddInt32(&calls, 1); return nil },
		func() error { atomic.AddInt32(&calls, 1); return boom },
		func() error { atomic.AddInt32(&calls, 1); return nil },
	}
	err := tr.StartProjectOptimized(context.Background(), starts)
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("should stop after error: want 2 calls, got %d", calls)
	}
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}
