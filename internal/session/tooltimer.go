package session

import (
	"sync"
	"time"

)

// toolTimer derives LogEntry.DurationMs for Claude tool calls. Claude CLI's
// stream-json carries no duration, so we time it ourselves: the moment a
// tool_use entry is parsed to the moment its tool_result/error entry is.
// (Hermes reports duration_ms on the wire and does not use this.)
type toolTimer struct {
	mu     sync.Mutex
	starts map[string]time.Time
}

// stamp records tool_use start times and fills DurationMs on the matching
// result entries in place. A result event ends the turn, so it drops any
// starts still open (a call that never got a result) instead of leaking them.
func (t *toolTimer) stamp(ev *ParsedEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range ev.Entries {
		e := &ev.Entries[i]
		if e.ToolUseID == "" {
			continue
		}
		switch e.Level {
		case "tool":
			if t.starts == nil {
				t.starts = make(map[string]time.Time)
			}
			t.starts[e.ToolUseID] = e.Time
		case "tool_result", "error":
			if start, ok := t.starts[e.ToolUseID]; ok {
				delete(t.starts, e.ToolUseID)
				if d := e.Time.Sub(start).Milliseconds(); d > 0 {
					e.DurationMs = d
				}
			}
		}
	}
	if ev.EventType == EventResult {
		clear(t.starts)
	}
}

