package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// Hermes CLI stream-json (HERMES-TASKS.md HR-04).
//
// `hermes chat --query-file - --format stream-json` writes one JSON object
// per stdout line, a different vocabulary from Claude CLI's:
//
//	{"type":"system","subtype":"init","model":"…","session_id":"20260924_111123_993e42"}
//	{"type":"text","text":"hel"}                         — a streaming delta
//	{"type":"tool_use","name":"terminal","tool_call_id":"…","input":{"command":"…"}}
//	{"type":"tool_result","name":"terminal","output":"…","duration_ms":1180,"is_error":false}
//	{"type":"result","session_id":"…","exit_code":0,"text":"…","error":"…",
//	 "tokens":{"input":8,"output":63,"total":…,"cache_read":…,"cache_write":…},"duration_ms":7173}
//
// (hermes_cli/stream_json.py, StreamJsonEmitter; real recordings are in
// testdata/hermes-stream/.) One process answers exactly one turn and exits
// after `result`; there are no permission_request or rate_limit events.
//
// hermesStream translates that into the same ParsedEvent values ParseLine
// produces, so everything downstream of handleEvent is runtime-agnostic.
// It is stateful because Hermes streams text as tiny deltas: they are
// buffered and flushed as one "text" log entry at the next non-text event,
// instead of one log row per token.
type hermesStream struct {
	text strings.Builder
	now  func() time.Time
	// lastError is the `error` text of the last failed result line, so the
	// runtime can classify it (rate limit, auth) without reparsing.
	lastError string
	// Hermes's wire format carries no tool_use_id, so we synthesize one: a
	// monotonic counter assigned to each tool_use, tracked per tool name so a
	// tool_result finds the most recent unclosed call with that name.
	toolSeq       int
	pendingByName map[string][]string
}

func newHermesStream() *hermesStream {
	return &hermesStream{now: time.Now, pendingByName: make(map[string][]string)}
}

type hermesRawEvent struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	Model      string          `json:"model"`
	SessionID  string          `json:"session_id"`
	Text       string          `json:"text"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	Output     string          `json:"output"`
	IsError    bool            `json:"is_error"`
	DurationMs int64           `json:"duration_ms"`
	ExitCode   int             `json:"exit_code"`
	Error      string          `json:"error"`
	Tokens     *struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
	} `json:"tokens"`
}

// Parse consumes one stdout line and returns zero or more events, in order.
func (h *hermesStream) Parse(line string) []ParsedEvent {
	now := h.now()
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ev hermesRawEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
		// Not a protocol line (a stray print from a plugin, a traceback):
		// surface it verbatim rather than dropping it.
		return append(h.flush(now), ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: "system", Source: "hermes", Message: line,
		}}})
	}

	if ev.Type == "text" {
		h.text.WriteString(ev.Text)
		return nil
	}
	out := h.flush(now)

	switch ev.Type {
	case "system":
		if ev.Subtype != "init" {
			return out
		}
		msg := "Session initialized (hermes)"
		if ev.Model != "" {
			msg += ": " + ev.Model
		}
		return append(out, ParsedEvent{
			EventType: EventInit,
			Init:      &InitInfo{SessionID: ev.SessionID, Model: ev.Model},
			Entries:   []config.LogEntry{{Time: now, Level: "system", Source: "hermes", Message: msg}},
		})

	case "tool_use":
		abbrev := hermesAbbreviateInput(ev.Name, ev.Input)
		h.toolSeq++
		id := fmt.Sprintf("hermes-%d", h.toolSeq)
		h.pendingByName[ev.Name] = append(h.pendingByName[ev.Name], id)
		return append(out, ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: "tool", Source: "hermes",
			Message: ev.Name + ": " + abbrev, ToolName: ev.Name, ToolInput: abbrev, ToolUseID: id,
		}}})

	case "tool_result":
		pe := ParsedEvent{EventType: EventLog}
		// todo_list's result is the authoritative full list after the call
		// (its input may be a partial merge, or empty for a plain read), so
		// the TaskPanel is fed from the output, not the input.
		if ev.Name == "todo_list" {
			if todos, ok := parseTodoInput(json.RawMessage(ev.Output)); ok {
				pe.Todos = todos
			}
		}
		msg := ev.Output
		if strings.TrimSpace(msg) == "" {
			msg = "(no output)"
		}
		level := "tool_result"
		if ev.IsError {
			level = "error"
		}
		// Pop the most recent unclosed tool_use with this name — Hermes has
		// no tool_use_id of its own to match on.
		var id string
		if q := h.pendingByName[ev.Name]; len(q) > 0 {
			id = q[len(q)-1]
			h.pendingByName[ev.Name] = q[:len(q)-1]
		}
		pe.Entries = []config.LogEntry{{Time: now, Level: level, Source: "hermes", Message: msg, ToolUseID: id}}
		return append(out, pe)

	case "result":
		res := &SessionResult{
			DurationMs: ev.DurationMs,
			ResultText: ev.Text,
		}
		if ev.Tokens != nil {
			res.Usage = TokenUsage{
				InputTokens:              ev.Tokens.Input,
				OutputTokens:             ev.Tokens.Output,
				CacheReadInputTokens:     ev.Tokens.CacheRead,
				CacheCreationInputTokens: ev.Tokens.CacheWrite,
			}
			// Hermes reports token usage only once per turn, here. Emit it
			// as the usage event Claude CLI would have sent per assistant
			// message, so the context bar and token totals still move.
			usage := res.Usage
			out = append(out, ParsedEvent{EventType: EventLog, Usage: &usage})
		}
		if ev.ExitCode != 0 || ev.Error != "" {
			res.StopReason = "error"
			msg := ev.Error
			if msg == "" {
				msg = "hermes turn failed"
			}
			h.lastError = msg
			out = append(out, ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: "error", Source: "hermes", Message: msg,
			}}})
		}
		msg := ev.Text
		if msg == "" {
			msg = "Turn completed"
		}
		return append(out, ParsedEvent{
			EventType: EventResult,
			Result:    res,
			Entries:   []config.LogEntry{{Time: now, Level: "result", Source: "hermes", Message: msg}},
		})
	}

	return append(out, ParsedEvent{EventType: EventUnknown, Entries: []config.LogEntry{{
		Time: now, Level: "system", Source: "hermes", Message: line,
	}}})
}

// Flush returns any buffered text as a log event (end of stream).
func (h *hermesStream) Flush() []ParsedEvent { return h.flush(h.now()) }

func (h *hermesStream) flush(now time.Time) []ParsedEvent {
	if h.text.Len() == 0 {
		return nil
	}
	text := h.text.String()
	h.text.Reset()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
		Time: now, Level: "text", Source: "hermes", Message: strings.TrimSpace(text),
	}}}}
}

// hermesAbbreviateInput is AbbreviateInput for Hermes' tool names: it keeps
// the salient argument (the shell command, the path, the pattern) and falls
// back to the raw JSON.
func hermesAbbreviateInput(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return string(input)
	}
	keys := map[string][]string{
		"terminal":      {"command"},
		"read_file":     {"path"},
		"write_file":    {"path"},
		"patch":         {"path"},
		"search_files":  {"pattern"},
		"web_search":    {"query"},
		"web_extract":   {"urls"},
		"skill_view":    {"name"},
		"delegate_task": {"goal"},
	}[tool]
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case []any:
			parts := make([]string, 0, len(v))
			for _, x := range v {
				if s, ok := x.(string); ok {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, " ")
			}
		}
	}
	return string(input)
}
