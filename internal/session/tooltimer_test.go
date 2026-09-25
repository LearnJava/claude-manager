package session

import (
	"testing"
	"time"

	"claude-manager/internal/config"
)

func TestToolTimerStampsDurations(t *testing.T) {
	var tt toolTimer
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms int, level, id string) ParsedEvent {
		return ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: t0.Add(time.Duration(ms) * time.Millisecond), Level: level, ToolUseID: id,
		}}}
	}
	tt.stamp(&ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{
		{Time: t0, Level: "tool", ToolUseID: "a"}, {Time: t0, Level: "tool", ToolUseID: "b"},
	}})
	rb, ra := at(200, "tool_result", "b"), at(1500, "error", "a")
	tt.stamp(&rb)
	tt.stamp(&ra)
	if got := rb.Entries[0].DurationMs; got != 200 {
		t.Errorf("b duration = %d, want 200", got)
	}
	if got := ra.Entries[0].DurationMs; got != 1500 {
		t.Errorf("a duration = %d, want 1500", got)
	}
	// Open starts are dropped on a turn result.
	tt.stamp(&ParsedEvent{Entries: []config.LogEntry{{Time: t0, Level: "tool", ToolUseID: "c"}}})
	tt.stamp(&ParsedEvent{EventType: EventResult})
	if len(tt.starts) != 0 {
		t.Errorf("starts not cleared: %v", tt.starts)
	}
}
