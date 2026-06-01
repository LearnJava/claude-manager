package optimization

import (
	"testing"

	"claude-manager/internal/config"
)

func newLoopCfg() *config.OptimizationSettings {
	return &config.OptimizationSettings{
		LoopDetection:           true,
		LoopThreshold:           3,
		LoopAction:              LoopActionWarn,
		LoopHint:                "stop looping",
		LoopWindow:              20,
		LoopIgnoreReadAfterEdit: true,
	}
}

func TestLoopDetector_DisabledReturnsNil(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopDetection = false
	d := NewLoopDetector(cfg)
	for i := 0; i < 5; i++ {
		if got := d.Observe("Read", "x.go"); got != nil {
			t.Fatalf("disabled detector must always return nil")
		}
	}
}

func TestLoopDetector_DetectsAtThreshold(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	if got := d.Observe("Read", "src/auth.go"); got != nil {
		t.Fatalf("first call: want nil, got %+v", got)
	}
	if got := d.Observe("Read", "src/auth.go"); got != nil {
		t.Fatalf("second call: want nil, got %+v", got)
	}
	got := d.Observe("Read", "src/auth.go")
	if got == nil {
		t.Fatal("third identical call: want detection, got nil")
	}
	if got.Count != 3 || got.Tool != "Read" || got.Input != "src/auth.go" {
		t.Fatalf("unexpected detection: %+v", got)
	}
	if got.Action != LoopActionWarn || got.Hint != "stop looping" {
		t.Fatalf("action/hint mismatch: %+v", got)
	}
}

func TestLoopDetector_DifferentInputsDontCount(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	d.Observe("Read", "a.go")
	d.Observe("Read", "b.go")
	if got := d.Observe("Read", "c.go"); got != nil {
		t.Fatalf("three different paths must not be a loop, got %+v", got)
	}
}

func TestLoopDetector_DifferentToolsDontCount(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	d.Observe("Read", "x.go")
	d.Observe("Grep", "x.go")
	if got := d.Observe("Glob", "x.go"); got != nil {
		t.Fatalf("different tools must not be a loop, got %+v", got)
	}
}

func TestLoopDetector_ReadAfterEditSuppressed(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	d.Observe("Edit", "src/auth.go")
	// Read immediately after Edit must not count.
	if got := d.Observe("Read", "src/auth.go"); got != nil {
		t.Fatalf("read-after-edit must not count, got %+v", got)
	}
	// Now 2 more reads should not trigger (read was suppressed, so only 2 reads
	// total, below threshold).
	d.Observe("Read", "src/auth.go")
	if got := d.Observe("Read", "src/auth.go"); got != nil {
		t.Fatalf("only 2 actual reads recorded, want no detection, got %+v", got)
	}
	// Third read brings counted reads to 3 → detect.
	if got := d.Observe("Read", "src/auth.go"); got == nil {
		t.Fatal("want detection after 3 counted reads")
	}
}

func TestLoopDetector_ReadAfterEditOnlySuppressesImmediateNext(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	d.Observe("Edit", "src/auth.go")
	d.Observe("Read", "src/auth.go") // suppressed
	d.Observe("Bash", "go test")     // breaks the chain
	d.Observe("Read", "src/auth.go") // counted (1)
	d.Observe("Read", "src/auth.go") // counted (2)
	if got := d.Observe("Read", "src/auth.go"); got == nil {
		t.Fatal("read should be counted after the chain is broken")
	}
}

func TestLoopDetector_RingBufferEvictsOldEntries(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopWindow = 4
	d := NewLoopDetector(cfg)
	d.Observe("Read", "x.go") // window: [x]
	d.Observe("Read", "y.go") // window: [x,y]
	d.Observe("Read", "y.go") // window: [x,y,y]
	d.Observe("Read", "z.go") // window: [x,y,y,z]
	// Now insert one more y → window becomes [y,y,z,y] — three y's in 4-slot
	// buffer, so threshold (3) is met.
	got := d.Observe("Read", "y.go")
	if got == nil {
		t.Fatal("expected detection of 3 y reads in 4-slot ring")
	}
	if got.Count != 3 {
		t.Fatalf("want count=3, got %d", got.Count)
	}
}

func TestLoopDetector_Reset(t *testing.T) {
	d := NewLoopDetector(newLoopCfg())
	d.Observe("Read", "a.go")
	d.Observe("Read", "a.go")
	d.Reset()
	if got := d.Observe("Read", "a.go"); got != nil {
		t.Fatalf("after reset, single observe must not detect; got %+v", got)
	}
}

func TestLoopDetector_DefaultThreshold(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopThreshold = 0 // should fall back to 3
	d := NewLoopDetector(cfg)
	d.Observe("Read", "a.go")
	d.Observe("Read", "a.go")
	if got := d.Observe("Read", "a.go"); got == nil {
		t.Fatal("fallback threshold should be 3")
	}
}

func TestLoopDetector_DefaultActionWhenEmpty(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopAction = ""
	d := NewLoopDetector(cfg)
	d.Observe("Read", "a.go")
	d.Observe("Read", "a.go")
	got := d.Observe("Read", "a.go")
	if got == nil || got.Action != LoopActionWarn {
		t.Fatalf("empty action must fall back to warn; got %+v", got)
	}
}

func TestLoopDetector_DefaultWindowWhenZero(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopWindow = 0
	d := NewLoopDetector(cfg)
	if got := len(d.buf); got != 20 {
		t.Fatalf("default window should be 20, got %d", got)
	}
}

func TestNormalizeInput(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	cases := []struct {
		name   string
		tool   string
		inputs map[string]string
		want   string
	}{
		{"bash command", "Bash", map[string]string{"command": "ls -la"}, "ls -la"},
		{"bash truncates", "Bash", map[string]string{"command": long}, long[:80]},
		{"read path", "Read", map[string]string{"file_path": "src/x.go"}, "src/x.go"},
		{"edit path", "Edit", map[string]string{"file_path": "src/x.go"}, "src/x.go"},
		{"write path", "Write", map[string]string{"file_path": "src/x.go"}, "src/x.go"},
		{"grep pattern", "Grep", map[string]string{"pattern": "TODO"}, "TODO"},
		{"glob pattern", "Glob", map[string]string{"pattern": "**/*.go"}, "**/*.go"},
		{"agent description", "Agent", map[string]string{"description": "explore code"}, "explore code"},
		{"unknown tool falls back", "FooBar", map[string]string{"q": "v"}, "v"},
		{"missing keys", "Bash", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeInput(tc.tool, tc.inputs); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLoopDetected_HintIncluded(t *testing.T) {
	cfg := newLoopCfg()
	cfg.LoopAction = LoopActionSendHint
	d := NewLoopDetector(cfg)
	d.Observe("Read", "a.go")
	d.Observe("Read", "a.go")
	got := d.Observe("Read", "a.go")
	if got == nil {
		t.Fatal("want detection")
	}
	if got.Action != LoopActionSendHint || got.Hint != "stop looping" {
		t.Fatalf("hint not propagated: %+v", got)
	}
}

func TestLoopDetector_NilCfg(t *testing.T) {
	d := NewLoopDetector(nil)
	if d.Enabled() {
		t.Fatal("nil cfg must not be enabled")
	}
	if got := d.Observe("Read", "a.go"); got != nil {
		t.Fatalf("nil cfg must always return nil, got %+v", got)
	}
}
