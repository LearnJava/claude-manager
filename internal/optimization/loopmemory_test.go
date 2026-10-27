package optimization

import (
	"strings"
	"testing"
	"time"
)

// farSequence feeds n rounds of "X then 5 distinct fillers" to a window-5
// detector, so X is never in the window twice; it returns the hits on X.
func farSequence(d *LoopDetector, rounds int) []*LoopDetected {
	var hits []*LoopDetected
	filler := 0
	for r := 0; r < rounds; r++ {
		if h := d.Observe("Read", "x.go"); h != nil {
			hits = append(hits, h)
		}
		for i := 0; i < 5; i++ {
			filler++
			d.Observe("Grep", string(rune('a'+filler%26))+string(rune('A'+filler/26)))
		}
	}
	return hits
}

func TestFarRepeatOutsideTheWindowIsDetected(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopWindow = 5
	cfg.LoopMemory = true
	cfg.LoopFarThreshold = 4
	cfg.LoopAction = LoopActionRestart
	hits := farSequence(NewLoopDetector(cfg), 8)
	if len(hits) != 2 {
		t.Fatalf("8 reads with threshold 4 = %d hits, want 2 (the tally starts over after a hit)", len(hits))
	}
	h := hits[0]
	if !h.Far || h.Count != 4 || h.Tool != "Read" || h.Input != "x.go" {
		t.Errorf("hit = %+v", h)
	}
	if h.Action != LoopActionWarn {
		t.Errorf("a far repeat must not restart, action = %q", h.Action)
	}
}

func TestFarRepeatOffChangesNothing(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopWindow = 5
	cfg.LoopFarThreshold = 2 // set, but loop_memory is off
	d := NewLoopDetector(cfg)
	if hits := farSequence(d, 12); len(hits) != 0 {
		t.Fatalf("loop_memory off: got %d far hits", len(hits))
	}
	if d.tally != nil {
		t.Error("loop_memory off must not even keep a tally")
	}
}

func TestFarRepeatDefaultThresholdAndNewRun(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopWindow = 5
	cfg.LoopMemory = true
	d := NewLoopDetector(cfg)
	if hits := farSequence(d, DefaultLoopFarThreshold-1); len(hits) != 0 {
		t.Fatal("below the default threshold nothing fires")
	}
	d.NewRun()
	if hits := farSequence(d, DefaultLoopFarThreshold-1); len(hits) != 0 {
		t.Fatal("a new run starts the tally over")
	}
	if hits := farSequence(d, 1); len(hits) != 1 {
		t.Fatal("the default threshold must fire")
	}
}

func TestInWindowLoopStillWins(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopMemory = true
	cfg.LoopAction = LoopActionRestart
	d := NewLoopDetector(cfg)
	var hit *LoopDetected
	for i := 0; i < 3; i++ {
		hit = d.Observe("Bash", "go test")
	}
	if hit == nil || hit.Far || hit.Action != LoopActionRestart {
		t.Fatalf("hit = %+v, want the ordinary in-window loop", hit)
	}
}

func TestPastLoopHint(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	if got := PastLoopHint("  ", now, now); got != "" {
		t.Errorf("no way out known = %q, want empty", got)
	}
	got := PastLoopHint("Bash go build ./...", now.Add(-3*time.Hour), now)
	for _, want := range []string{"3 h ago", "Bash go build ./...", "stuck"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint %q lacks %q", got, want)
		}
	}
	if got := PastLoopHint("x", now.Add(-72*time.Hour), now); !strings.Contains(got, "3 days ago") {
		t.Errorf("hint = %q", got)
	}
}
