package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// kimiStream translates Kimi Code CLI's `kimi -p --output-format stream-json`
// stdout into the shared ParsedEvent format. Per the CLI docs, a regular reply
// produces an Assistant message; a tool call produces an Assistant message
// with tool_calls, then the matching Tool message. init/result carry the
// session id (for --session resume) and the turn's outcome. Thinking content
// is not written to JSONL; tool progress goes to stderr (drainHermesStderr).
//
// The parser is deliberately tolerant of field-name variants (role vs type,
// content as string vs blocks, tool_call_id vs tool_use_id): unknown lines
// are preserved verbatim as system entries so nothing silently vanishes.
type kimiStream struct {
	seq           int
	pendingByName map[string][]string
	lastError     string
	sawResult     bool
}

func newKimiStream() *kimiStream {
	return &kimiStream{pendingByName: map[string][]string{}}
}

type kimiRawEvent struct {
	Type          string          `json:"type"`
	Role          string          `json:"role"`
	Subtype       string          `json:"subtype"`
	SessionID     string          `json:"session_id"`
	ID            string          `json:"id"`
	Model         string          `json:"model"`
	Version       string          `json:"version"`
	Text          string          `json:"text"`
	Content       json.RawMessage `json:"content"`
	ToolCalls     []kimiToolCall  `json:"tool_calls"`
	ToolCallID    string          `json:"tool_call_id"`
	ToolUseID     string          `json:"tool_use_id"`
	Name          string          `json:"name"`
	Usage         *TokenUsage     `json:"usage"`
	Tokens        *TokenUsage     `json:"tokens"`
	Error         string          `json:"error"`
	StopReason    string          `json:"stop_reason"`
	Result        string          `json:"result"`
	IsError       bool            `json:"is_error"`
	ExitCode      int             `json:"exit_code"`
	FailedAttempt int             `json:"failed_attempt"`
	NextAttempt   int             `json:"next_attempt"`
	MaxAttempts   int             `json:"max_attempts"`
	DelayMS       float64         `json:"delay_ms"`
	ErrorName     string          `json:"error_name"`
	ErrorMessage  string          `json:"error_message"`
}

type kimiToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Input     json.RawMessage `json:"input"`
	Arguments json.RawMessage `json:"arguments"`
	Function  *kimiToolFunc   `json:"function"`
}

type kimiToolFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (c kimiToolCall) toolName() string {
	if c.Name != "" {
		return c.Name
	}
	if c.Function != nil {
		return c.Function.Name
	}
	return ""
}

func (c kimiToolCall) argsText() string {
	if len(c.Input) > 0 {
		return string(c.Input)
	}
	if len(c.Arguments) > 0 {
		return string(c.Arguments)
	}
	if c.Function != nil && len(c.Function.Arguments) > 0 {
		return string(c.Function.Arguments)
	}
	return ""
}

func kimiMetaMessage(ev kimiRawEvent, raw string) string {
	switch ev.Type {
	case "system.version":
		if ev.Version != "" {
			return "Kimi Code " + ev.Version
		}
	case "turn.step.retrying":
		reason := strings.TrimSpace(ev.ErrorMessage)
		if reason == "" {
			reason = ev.ErrorName
		}
		if reason == "" {
			reason = "request failed"
		}
		attempt := ev.NextAttempt
		if attempt == 0 {
			attempt = ev.FailedAttempt + 1
		}
		msg := fmt.Sprintf("%s — retry %d", reason, attempt)
		if ev.MaxAttempts > 0 {
			msg += fmt.Sprintf("/%d", ev.MaxAttempts)
		}
		if ev.DelayMS > 0 {
			delay := time.Duration(ev.DelayMS * float64(time.Millisecond)).Round(time.Second)
			msg += " in " + delay.String()
		}
		return msg
	}
	return raw
}

// Parse implements Parser.Parse for one stream-json line.
func (k *kimiStream) Parse(line string) []ParsedEvent {
	var ev kimiRawEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
			Time: time.Now(), Level: "system", Source: "kimi", Message: line,
		}}}}
	}

	typ := ev.Type
	if typ == "" {
		typ = ev.Role
	}
	if ev.Role == "meta" {
		typ = "meta"
	}
	switch typ {
	case "meta":
		// The real Kimi 2.x wire has no init/result events: the conversation
		// id arrives as a trailing session.resume_hint. Known meta events get
		// readable text; unknown ones are preserved verbatim.
		if ev.Type == "session.resume_hint" {
			return []ParsedEvent{{
				EventType: EventInit,
				Init:      &InitInfo{SessionID: ev.SessionID, Model: ev.Model},
			}}
		}
		return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
			Time: time.Now(), Level: "system", Source: "kimi", Message: kimiMetaMessage(ev, line),
		}}}}

	case "system", "init":
		id := ev.SessionID
		if id == "" {
			id = ev.ID
		}
		return []ParsedEvent{{
			EventType: EventInit,
			Init:      &InitInfo{SessionID: id, Model: ev.Model},
		}}

	case "assistant":
		var entries []config.LogEntry
		var activity *Activity
		if text := kimiText(ev.Text, ev.Content); text != "" {
			entries = append(entries, config.LogEntry{
				Time: time.Now(), Level: "text", Source: "kimi", Message: text,
			})
			activity = &Activity{Kind: ActivityWriting}
		}
		for _, call := range ev.ToolCalls {
			k.seq++
			id := call.ID
			if id == "" {
				id = fmt.Sprintf("kimi-%d", k.seq)
			}
			name := call.toolName()
			k.pendingByName[name] = append(k.pendingByName[name], id)
			entries = append(entries, config.LogEntry{
				Time: time.Now(), Level: "tool", Source: "kimi",
				ToolName: name, ToolInput: call.argsText(), ToolUseID: id,
			})
			activity = &Activity{Kind: ActivityTool, Tool: name}
		}
		if len(entries) == 0 {
			return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
				Time: time.Now(), Level: "system", Source: "kimi", Message: line,
			}}}}
		}
		return []ParsedEvent{{EventType: EventLog, Entries: entries, Activity: activity}}

	case "tool", "tool_result":
		text := kimiText(ev.Text, ev.Content)
		if ev.Error != "" {
			text = ev.Error
		}
		id := ev.ToolCallID
		if id == "" {
			id = ev.ToolUseID
		}
		if id == "" {
			if ids := k.pendingByName[ev.Name]; len(ids) > 0 {
				id = ids[len(ids)-1]
				k.pendingByName[ev.Name] = ids[:len(ids)-1]
			}
		}
		level := "tool_result"
		if ev.IsError || ev.Error != "" {
			level = "error"
		}
		return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
			Time: time.Now(), Level: level, Source: "kimi",
			ToolName: ev.Name, Message: text, ToolUseID: id,
		}}, Activity: &Activity{Kind: ActivityThinking}}}

	case "result":
		k.sawResult = true
		failed := ev.Error != "" || ev.ExitCode != 0 || ev.StopReason == "error"
		k.lastError = ev.Error
		stop := "success"
		if failed {
			stop = "error"
		}
		res := &SessionResult{
			StopReason: stop,
			ResultText: ev.Result,
		}
		usage := ev.Usage
		if usage == nil {
			usage = ev.Tokens
		}
		if usage != nil {
			res.Usage = *usage
		}
		out := []ParsedEvent{}
		if usage != nil {
			out = append(out, ParsedEvent{EventType: EventLog, Usage: usage})
		}
		if failed && k.lastError != "" {
			out = append(out, ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: time.Now(), Level: "error", Source: "kimi", Message: k.lastError,
			}}})
		}
		entries := []config.LogEntry{}
		if ev.Result != "" {
			entries = append(entries, config.LogEntry{
				Time: time.Now(), Level: "result", Source: "kimi", Message: ev.Result,
			})
		}
		out = append(out, ParsedEvent{
			EventType: EventResult,
			Result:    res,
			Entries:   entries,
			Activity:  &Activity{Kind: ActivityIdle},
		})
		if q := questionFromMarker(ev.Result); q != nil {
			out[len(out)-1].Questions = []Question{*q}
		}
		return out
	}

	// Unknown or malformed shapes are preserved verbatim.
	return []ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
		Time: time.Now(), Level: "system", Source: "kimi", Message: line,
	}}}}
}

// Flush implements Parser.Flush. The real Kimi 2.x wire has no result event:
// the process simply exits after its last assistant/meta line. Synthesize the
// turn's EventResult then (unless one already arrived), so the per-turn loop
// learns the turn finished and the manager records the run — with no usage or
// cost, which the UI reports as "no data".
func (k *kimiStream) Flush() []ParsedEvent {
	if k.sawResult {
		return nil
	}
	k.sawResult = true
	stop := "success"
	if k.lastError != "" {
		stop = "error"
	}
	return []ParsedEvent{{
		EventType: EventResult,
		Result:    &SessionResult{StopReason: stop},
		Activity:  &Activity{Kind: ActivityIdle},
	}}
}

// LastError implements Parser.LastError.
func (k *kimiStream) LastError() string { return k.lastError }

// kimiText reads message text from either a plain string field or a content
// value that may be a string or an array of {type:"text",text:...} blocks.
func kimiText(text string, content json.RawMessage) string {
	if text != "" {
		return text
	}
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return strings.TrimSpace(string(content))
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
