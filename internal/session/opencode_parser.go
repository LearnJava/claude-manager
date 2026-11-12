package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// OpenCode CLI v2 `opencode run --format json` wire format. One JSON object
// per stdout line, its own vocabulary:
//
//	{"type":"step_start","timestamp":…,"sessionID":"ses…","part":{"type":"step-start",…}}
//	{"type":"text","timestamp":…,"sessionID":"ses…","part":{"type":"text","text":"…","time":{…}}}
//	{"type":"tool_use","timestamp":…,"sessionID":"ses…","part":{"type":"tool","id":"call_…",
//	  "tool":"shell","state":{"status":"completed","input":{…},"output":"…"}}}
//	{"type":"step_finish","timestamp":…,"sessionID":"ses…","part":{"type":"step-finish",
//	  "reason":"stop|tool-calls","cost":0,"tokens":{"input":…,"output":…,"cache":{"read":…,"write":…}}}}
//	{"type":"error","timestamp":…,"sessionID":"ses…","error":{"type":"provider.no-route","message":"…"}}
//
// (recorded live from opencode v2.2.7, 2026-11-12). One process answers
// exactly one turn and exits after the final step_finish; a failed turn is
// one `error` line and exit code 1. A step_finish with reason "tool-calls"
// is an intermediate step (the agentic loop continues): it contributes its
// usage but no result event — only reason "stop" ends the turn. Text arrives
// complete (not as deltas)
// but is still buffered and flushed as one "text" log entry at the next
// non-text event, matching the Hermes parser's output shape.
//
// opencodeStream translates that into the same ParsedEvent values the other
// parsers produce, so everything downstream of handleEvent is
// runtime-agnostic. Stateful within one process: the init-once flag and the
// text buffer.
type opencodeStream struct {
	text      strings.Builder
	now       func() time.Time
	lastError string
	initSent  bool
	toolSeq   int
	activity  string
	// lastText is the most recent flushed text block, so finishEvent can
	// check the ask-user/continue markers on it after the flush.
	lastText string
	// continueSeen is a continue_session marker seen in any text this
	// process streamed, kept for the same reason hermesStream keeps one:
	// a background completion feeding one more turn makes the final text
	// lose the marker, and the run must still count as a finished slice.
	continueSeen *Question
}

func newOpenCodeStream() *opencodeStream {
	return &opencodeStream{now: time.Now}
}

type opencodePart struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
	State  struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
	} `json:"state"`
	Tokens *struct {
		Input  int `json:"input"`
		Output int `json:"output"`
		Cache  struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

type opencodeRawEvent struct {
	Type      string       `json:"type"`
	SessionID string       `json:"sessionID"`
	Part      opencodePart `json:"part"`
	Error     struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Parse consumes one stdout line and returns zero or more events, in order.
func (o *opencodeStream) Parse(line string) []ParsedEvent {
	now := o.now()
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ev opencodeRawEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
		// Not a protocol line (a stray log line): surface it verbatim.
		return append(o.flush(now), ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: "system", Source: "opencode", Message: line,
		}}})
	}

	var out []ParsedEvent
	if !o.initSent && ev.SessionID != "" {
		o.initSent = true
		out = append(out, ParsedEvent{
			EventType: EventInit,
			Init:      &InitInfo{SessionID: ev.SessionID},
			Entries: []config.LogEntry{{Time: now, Level: "system", Source: "opencode",
				Message: "Session initialized (opencode): " + ev.SessionID}},
		})
	}

	switch ev.Type {
	case "text":
		o.text.WriteString(ev.Part.Text)
		if o.activity != ActivityWriting {
			o.activity = ActivityWriting
			out = append(out, ParsedEvent{EventType: EventUnknown, Activity: &Activity{Kind: ActivityWriting}})
		}
		return out
	case "step_start":
		return out // no-op: activity is driven by the parts that follow
	case "tool_use":
		return append(out, o.toolEvent(now, ev.Part)...)
	case "step_finish":
		return append(out, o.finishEvent(now, ev.Part)...)
	case "error":
		return append(out, o.errorEvent(now, ev)...)
	}

	return append(o.flush(now), ParsedEvent{EventType: EventUnknown, Entries: []config.LogEntry{{
		Time: now, Level: "system", Source: "opencode", Message: line,
	}}})
}

// toolEvent renders one completed tool part as a tool call + tool_result
// entry pair with the same ToolUseID, mirroring the two-line shape the UI
// already renders for the other runtimes.
func (o *opencodeStream) toolEvent(now time.Time, part opencodePart) []ParsedEvent {
	o.toolSeq++
	id := part.ID
	if id == "" {
		id = fmt.Sprintf("opencode-%d", o.toolSeq)
	}
	name := part.Tool
	if name == "" {
		name = "tool"
	}
	abbrev := hermesAbbreviateInput(name, part.State.Input) // generic: salient arg or raw JSON
	o.activity = ActivityTool
	args := BuildToolArgs(part.State.Input)
	call := ParsedEvent{EventType: EventLog, Activity: &Activity{Kind: ActivityTool, Tool: name},
		Entries: []config.LogEntry{{Time: now, Level: "tool", Source: "opencode",
			Message: name + ": " + abbrev, ToolName: name, ToolInput: abbrev, ToolUseID: id,
			Diff: BuildDiff(name, part.State.Input), ToolArgs: args}}}
	result := o.toolResultEvent(now, id, part, args)
	return []ParsedEvent{call, result}
}

func (o *opencodeStream) toolResultEvent(now time.Time, id string, part opencodePart, args map[string]string) ParsedEvent {
	// todo parity: opencode's todowrite output is the authoritative list
	// after the call, same role as Hermes' todo_list.
	if name := part.Tool; name == "todowrite" || name == "todo_write" {
		if todos, ok := parseTodoInput(json.RawMessage(part.State.Output)); ok {
			return ParsedEvent{EventType: EventLog, Activity: &Activity{Kind: ActivityThinking},
				Todos: todos, Entries: []config.LogEntry{{Time: now, Level: "tool_result",
					Source: "opencode", Message: part.State.Output, ToolUseID: id, ToolArgs: args}}}
		}
	}
	o.activity = ActivityThinking
	msg := part.State.Output
	if strings.TrimSpace(msg) == "" {
		msg = "(no output)"
	}
	return ParsedEvent{EventType: EventLog, Activity: &Activity{Kind: ActivityThinking},
		Entries: []config.LogEntry{{Time: now, Level: "tool_result", Source: "opencode",
			Message: msg, ToolUseID: id, ToolArgs: args}}}
}

func (o *opencodeStream) finishEvent(now time.Time, part opencodePart) []ParsedEvent {
	// An intermediate step (reason "tool-calls": the model made tool calls and
	// the agentic loop continues) is progress, not a finished turn. Emit its
	// usage but no result — otherwise every tool round-trip renders as
	// "Turn completed" and the feed's done badge, burying the real end of the
	// turn under dozens of false ones (observed live: ~80 "Turn completed"
	// entries for one task).
	if part.Reason == "tool-calls" {
		out := o.flush(now)
		if part.Tokens != nil {
			out = append(out, ParsedEvent{EventType: EventLog, Usage: &TokenUsage{
				InputTokens:              part.Tokens.Input,
				OutputTokens:             part.Tokens.Output,
				CacheReadInputTokens:     part.Tokens.Cache.Read,
				CacheCreationInputTokens: part.Tokens.Cache.Write,
			}})
		}
		return out
	}
	out := o.flush(now)
	o.activity = ActivityIdle
	if part.Tokens != nil {
		usage := TokenUsage{
			InputTokens:              part.Tokens.Input,
			OutputTokens:             part.Tokens.Output,
			CacheReadInputTokens:     part.Tokens.Cache.Read,
			CacheCreationInputTokens: part.Tokens.Cache.Write,
		}
		out = append(out, ParsedEvent{EventType: EventLog, Usage: &usage})
	}
	pe := ParsedEvent{
		EventType: EventResult,
		Result:    &SessionResult{StopReason: "stop", ResultText: o.lastText},
		Activity:  &Activity{Kind: ActivityIdle},
		Entries:   []config.LogEntry{{Time: now, Level: "result", Source: "opencode", Message: "Turn completed"}},
	}
	// The turn's text was flushed right above; the ask-user/continue markers
	// it carried ride on the result event, the same way Hermes' result does.
	if q := questionFromMarker(o.lastText); q != nil {
		pe.Questions = []Question{*q}
	} else if o.continueSeen != nil {
		pe.Questions = []Question{*o.continueSeen}
	}
	return append(out, pe)
}

func (o *opencodeStream) errorEvent(now time.Time, ev opencodeRawEvent) []ParsedEvent {
	o.activity = ActivityIdle
	msg := ev.Error.Message
	if msg == "" {
		msg = "opencode turn failed"
	}
	o.lastError = msg
	out := o.flush(now)
	return append(out,
		ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: "error", Source: "opencode", Message: msg,
		}}},
		ParsedEvent{EventType: EventResult, Result: &SessionResult{StopReason: "error"},
			Activity: &Activity{Kind: ActivityIdle}},
	)
}

func (o *opencodeStream) LastError() string { return o.lastError }

// Flush returns any buffered text as a log event (end of stream).
func (o *opencodeStream) Flush() []ParsedEvent { return o.flush(o.now()) }

func (o *opencodeStream) flush(now time.Time) []ParsedEvent {
	if o.text.Len() == 0 {
		return nil
	}
	text := o.text.String()
	o.text.Reset()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	o.lastText = strings.TrimSpace(text)
	if q := questionFromMarker(o.lastText); q != nil && q.Kind == KindContinueSession {
		o.continueSeen = q
	}
	return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
		Time: now, Level: "text", Source: "opencode", Message: o.lastText,
	}}}}
}
