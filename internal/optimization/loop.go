package optimization

import (
	"strings"
	"sync"

	"claude-manager/internal/config"
)

// LoopAction enumerates what the manager should do when a loop is detected.
// Mirrors the loop_action TOML setting (PLAN.md 20.3.8).
const (
	LoopActionWarn     = "warn"
	LoopActionSendHint = "send_hint"
	LoopActionRestart  = "restart"
)

// LoopDetected is returned by LoopDetector.Observe when N or more identical
// tool+input combinations have appeared within the recent-tool-calls window.
type LoopDetected struct {
	Tool       string // tool name, e.g. "Read"
	Input      string // normalized input, e.g. "src/auth.go"
	Count      int    // occurrences in the current window (>= threshold)
	Action     string // one of LoopAction* constants
	Hint       string // text to send to stdin when Action == LoopActionSendHint
	WindowSize int    // size of the ring buffer at detection time
}

// toolCall is one slot in the LoopDetector ring buffer.
type toolCall struct {
	tool  string
	input string
	used  bool
}

// LoopDetector keeps a ring buffer of the most recent tool calls and reports
// when a single (tool, normalized-input) combination has occurred at least
// LoopThreshold times within the window.
//
// Normalization of the input is the caller's responsibility (use
// NormalizeInput as a default helper). Per PLAN.md 20.3.8: Bash → first 80
// chars of the command, Read/Edit/Write → file path, Grep → pattern.
//
// Safe for concurrent use within one session.
type LoopDetector struct {
	cfg *config.OptimizationSettings

	mu          sync.Mutex
	buf         []toolCall
	head        int // next write position
	lastEditKey string
}

// NewLoopDetector returns a detector sized from cfg.LoopWindow. If cfg is nil
// or detection is disabled (LoopDetection == false), Observe always returns
// nil. A non-positive window falls back to 20.
func NewLoopDetector(cfg *config.OptimizationSettings) *LoopDetector {
	window := 20
	if cfg != nil && cfg.LoopWindow > 0 {
		window = cfg.LoopWindow
	}
	return &LoopDetector{
		cfg: cfg,
		buf: make([]toolCall, window),
	}
}

// Enabled reports whether the detector will actually observe.
func (d *LoopDetector) Enabled() bool {
	return d.cfg != nil && d.cfg.LoopDetection
}

// Observe records one tool call and returns a non-nil *LoopDetected if the
// combination has appeared at least LoopThreshold times in the current window.
//
// tool is the canonical tool name (e.g. "Bash", "Read"). normalizedInput must
// already be in canonical form (use NormalizeInput).
//
// When LoopIgnoreReadAfterEdit is true, a Read immediately following an Edit
// of the same path is not counted (it's the legitimate "verify after edit"
// pattern, PLAN.md 20.3.8 "Снижение ложных срабатываний").
func (d *LoopDetector) Observe(tool, normalizedInput string) *LoopDetected {
	if !d.Enabled() {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	key := tool + ":" + normalizedInput

	// Read-after-edit suppression
	if d.cfg.LoopIgnoreReadAfterEdit &&
		tool == "Read" &&
		d.lastEditKey != "" &&
		d.lastEditKey == "Edit:"+normalizedInput {
		// Skip counting but still update lastEditKey so subsequent Reads of the
		// same file (not immediately after Edit) are counted normally.
		d.lastEditKey = ""
		return nil
	}

	// Update lastEditKey AFTER the read-after-edit check so an Edit call
	// itself does not get suppressed.
	if tool == "Edit" {
		d.lastEditKey = key
	} else {
		d.lastEditKey = ""
	}

	// Insert into ring buffer.
	d.buf[d.head] = toolCall{tool: tool, input: normalizedInput, used: true}
	d.head = (d.head + 1) % len(d.buf)

	// Count occurrences of this key in the buffer.
	count := 0
	for _, c := range d.buf {
		if c.used && c.tool == tool && c.input == normalizedInput {
			count++
		}
	}

	threshold := d.cfg.LoopThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if count < threshold {
		return nil
	}

	action := d.cfg.LoopAction
	if action == "" {
		action = LoopActionWarn
	}

	return &LoopDetected{
		Tool:       tool,
		Input:      normalizedInput,
		Count:      count,
		Action:     action,
		Hint:       d.cfg.LoopHint,
		WindowSize: len(d.buf),
	}
}

// Reset clears the ring buffer. Useful after a successful intervention
// (e.g. send_hint that worked) so we don't immediately re-trigger on the same
// historical entries.
func (d *LoopDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.buf {
		d.buf[i] = toolCall{}
	}
	d.head = 0
	d.lastEditKey = ""
}

// NormalizeInput produces a canonical representation of a tool's input for
// loop detection. Rules from PLAN.md 20.3.8:
//
//   - Bash         → first 80 chars of the command
//   - Read/Edit/Write → file path (verbatim)
//   - Grep         → pattern (verbatim)
//   - Glob         → pattern (verbatim)
//   - Agent        → first 120 chars of description
//   - default      → first 120 chars of input
//
// inputs is keyed by parameter name (e.g. "command", "file_path"). Missing
// keys collapse to an empty string so two calls with no relevant parameter
// will still be considered equal.
func NormalizeInput(tool string, inputs map[string]string) string {
	get := func(k string) string { return strings.TrimSpace(inputs[k]) }

	switch tool {
	case "Bash":
		return truncate(get("command"), 80)
	case "Read", "Edit", "Write":
		return get("file_path")
	case "Grep", "Glob":
		return get("pattern")
	case "Agent":
		return truncate(get("description"), 120)
	}
	// Best-effort fallback: concatenate sorted values, truncate.
	var b strings.Builder
	for _, v := range inputs {
		if b.Len() > 0 {
			b.WriteByte('|')
		}
		b.WriteString(v)
	}
	return truncate(b.String(), 120)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
