package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

func TestParseEmptyLine(t *testing.T) {
	ev := parseLineAt("", testTime)
	if ev.EventType != EventUnknown {
		t.Errorf("expected %s, got %s", EventUnknown, ev.EventType)
	}
}

func TestParseNonJSON(t *testing.T) {
	ev := parseLineAt("Starting Claude CLI...", testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
	}
	e := ev.Entries[0]
	if e.Level != "system" {
		t.Errorf("expected level system, got %s", e.Level)
	}
	if e.Source != "claude" {
		t.Errorf("expected source claude, got %s", e.Source)
	}
	if e.Message != "Starting Claude CLI..." {
		t.Errorf("unexpected message: %s", e.Message)
	}
}

// TestParseSystemInit uses the real sample from PLAN.md section 19.8.
func TestParseSystemInit(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"16e0200b-abcd-1234-efgh-000000000001","model":"claude-sonnet-4-6","tools":["Bash","Edit","Read","Grep","Glob","Write"],"mcp_servers":[{"name":"github","status":"connected"}],"permissionMode":"default","claude_code_version":"2.1.118","cwd":"D:\\RustProjects\\lumen-browser"}`

	ev := parseLineAt(line, testTime)
	if ev.EventType != EventInit {
		t.Fatalf("expected %s, got %s", EventInit, ev.EventType)
	}
	if ev.Init == nil {
		t.Fatal("Init is nil")
	}
	if ev.Init.SessionID != "16e0200b-abcd-1234-efgh-000000000001" {
		t.Errorf("unexpected session_id: %s", ev.Init.SessionID)
	}
	if ev.Init.Model != "claude-sonnet-4-6" {
		t.Errorf("unexpected model: %s", ev.Init.Model)
	}
	if ev.Init.ClaudeCodeVersion != "2.1.118" {
		t.Errorf("unexpected version: %s", ev.Init.ClaudeCodeVersion)
	}
	if ev.Init.PermissionMode != "default" {
		t.Errorf("unexpected permissionMode: %s", ev.Init.PermissionMode)
	}
	if len(ev.Init.Tools) != 6 {
		t.Errorf("expected 6 tools, got %d", len(ev.Init.Tools))
	}
	if len(ev.Init.MCPServers) != 1 || ev.Init.MCPServers[0].Name != "github" {
		t.Errorf("unexpected mcp_servers: %+v", ev.Init.MCPServers)
	}
	if len(ev.Entries) != 1 || ev.Entries[0].Level != "system" {
		t.Errorf("expected 1 system log entry, got %+v", ev.Entries)
	}
	if ev.Entries[0].Time != testTime {
		t.Errorf("unexpected time: %v", ev.Entries[0].Time)
	}
}

func TestParseSystemUnknownSubtype(t *testing.T) {
	line := `{"type":"system","subtype":"something_else"}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventUnknown {
		t.Errorf("expected %s, got %s", EventUnknown, ev.EventType)
	}
}

func TestParseAssistantText(t *testing.T) {
	line := `{"type":"assistant","message":{"model":"claude-sonnet-4-6","content":[{"type":"text","text":"I'll help you with that."}],"usage":{"input_tokens":100,"output_tokens":10,"service_tier":"standard"}}}`

	ev := parseLineAt(line, testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
	}
	e := ev.Entries[0]
	if e.Level != "text" {
		t.Errorf("expected level text, got %s", e.Level)
	}
	if e.Source != "claude" {
		t.Errorf("expected source claude, got %s", e.Source)
	}
	if e.Message != "I'll help you with that." {
		t.Errorf("unexpected message: %s", e.Message)
	}
	if ev.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if ev.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", ev.Usage.InputTokens)
	}
	if ev.Usage.OutputTokens != 10 {
		t.Errorf("expected 10 output tokens, got %d", ev.Usage.OutputTokens)
	}
	if ev.Usage.ServiceTier != "standard" {
		t.Errorf("expected standard, got %s", ev.Usage.ServiceTier)
	}
}

func TestParseAssistantWhitespaceTextSkipped(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"   "}]}}`
	ev := parseLineAt(line, testTime)
	if len(ev.Entries) != 0 {
		t.Errorf("expected whitespace-only text to be skipped, got %d entries", len(ev.Entries))
	}
}

func TestParseAssistantToolUse(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		input     string
		wantInput string
	}{
		{"bash", "Bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"read", "Read", `{"file_path":"/src/main.go","offset":10}`, "/src/main.go"},
		{"edit", "Edit", `{"file_path":"/src/main.go","old_string":"x","new_string":"y"}`, "/src/main.go"},
		{"write", "Write", `{"file_path":"/src/new.go","content":"package main"}`, "/src/new.go"},
		{"grep", "Grep", `{"pattern":"func.*Error","path":"./internal"}`, "func.*Error"},
		{"glob", "Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"agent", "Agent", `{"description":"Explore the codebase","prompt":"Find all tests"}`, "Explore the codebase"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"` +
				tc.toolName + `","input":` + tc.input + `}]}}`
			ev := parseLineAt(line, testTime)
			if ev.EventType != EventLog {
				t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
			}
			if len(ev.Entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
			}
			e := ev.Entries[0]
			if e.Level != "tool" {
				t.Errorf("expected level tool, got %s", e.Level)
			}
			if e.ToolName != tc.toolName {
				t.Errorf("expected ToolName %s, got %s", tc.toolName, e.ToolName)
			}
			if e.ToolInput != tc.wantInput {
				t.Errorf("expected ToolInput %q, got %q", tc.wantInput, e.ToolInput)
			}
			wantMsg := tc.toolName + ": " + tc.wantInput
			if e.Message != wantMsg {
				t.Errorf("expected Message %q, got %q", wantMsg, e.Message)
			}
		})
	}
}

func TestParseAssistantThinking(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think about this..."},{"type":"text","text":"Here is the answer."}]}}`

	ev := parseLineAt(line, testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if len(ev.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ev.Entries))
	}
	if ev.Entries[0].Level != "thinking" {
		t.Errorf("expected level thinking, got %s", ev.Entries[0].Level)
	}
	if ev.Entries[0].Message != "Let me think about this..." {
		t.Errorf("unexpected thinking: %s", ev.Entries[0].Message)
	}
	if ev.Entries[1].Level != "text" {
		t.Errorf("expected level text, got %s", ev.Entries[1].Level)
	}
	if ev.Entries[1].Message != "Here is the answer." {
		t.Errorf("unexpected text: %s", ev.Entries[1].Message)
	}
}

func TestParseAssistantMultipleContentBlocks(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"I'll read the file."},` +
		`{"type":"tool_use","name":"Read","input":{"file_path":"/src/main.go"}},` +
		`{"type":"text","text":"Done."}` +
		`]}}`

	ev := parseLineAt(line, testTime)
	if len(ev.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(ev.Entries))
	}
	if ev.Entries[0].Level != "text" {
		t.Errorf("entry 0: expected level text, got %s", ev.Entries[0].Level)
	}
	if ev.Entries[1].Level != "tool" {
		t.Errorf("entry 1: expected level tool, got %s", ev.Entries[1].Level)
	}
	if ev.Entries[2].Level != "text" {
		t.Errorf("entry 2: expected level text, got %s", ev.Entries[2].Level)
	}
}

// TestParseAssistantWithUsage uses the real sample from PLAN.md section 18.1.
func TestParseAssistantWithUsage(t *testing.T) {
	line := `{
  "type": "assistant",
  "message": {
    "model": "claude-sonnet-4-6",
    "usage": {
      "input_tokens": 15230,
      "cache_creation_input_tokens": 16634,
      "cache_read_input_tokens": 8200,
      "output_tokens": 342,
      "service_tier": "standard"
    }
  }
}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if ev.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if ev.Usage.InputTokens != 15230 {
		t.Errorf("expected 15230, got %d", ev.Usage.InputTokens)
	}
	if ev.Usage.CacheCreationInputTokens != 16634 {
		t.Errorf("expected 16634, got %d", ev.Usage.CacheCreationInputTokens)
	}
	if ev.Usage.CacheReadInputTokens != 8200 {
		t.Errorf("expected 8200, got %d", ev.Usage.CacheReadInputTokens)
	}
	if ev.Usage.OutputTokens != 342 {
		t.Errorf("expected 342, got %d", ev.Usage.OutputTokens)
	}
	if ev.Usage.ServiceTier != "standard" {
		t.Errorf("expected standard, got %s", ev.Usage.ServiceTier)
	}
}

// TestParseResult uses the real sample from PLAN.md section 18.1.
func TestParseResult(t *testing.T) {
	line := `{
  "type": "result",
  "total_cost_usd": 0.0624,
  "duration_ms": 7416,
  "duration_api_ms": 6947,
  "num_turns": 1,
  "usage": {
    "input_tokens": 15230,
    "cache_creation_input_tokens": 16634,
    "cache_read_input_tokens": 8200,
    "output_tokens": 342,
    "server_tool_use": {
      "web_search_requests": 0,
      "web_fetch_requests": 0
    },
    "cache_creation": {
      "ephemeral_1h_input_tokens": 16634,
      "ephemeral_5m_input_tokens": 0
    }
  },
  "modelUsage": {
    "claude-sonnet-4-6": {
      "inputTokens": 15230,
      "outputTokens": 342,
      "cacheReadInputTokens": 8200,
      "cacheCreationInputTokens": 16634,
      "costUSD": 0.0624,
      "contextWindow": 200000,
      "maxOutputTokens": 32000
    }
  }
}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventResult {
		t.Fatalf("expected %s, got %s", EventResult, ev.EventType)
	}
	if ev.Result == nil {
		t.Fatal("Result is nil")
	}

	r := ev.Result
	if r.TotalCostUSD != 0.0624 {
		t.Errorf("expected 0.0624, got %f", r.TotalCostUSD)
	}
	if r.DurationMs != 7416 {
		t.Errorf("expected 7416, got %d", r.DurationMs)
	}
	if r.DurationApiMs != 6947 {
		t.Errorf("expected 6947, got %d", r.DurationApiMs)
	}
	if r.NumTurns != 1 {
		t.Errorf("expected 1, got %d", r.NumTurns)
	}
	if r.Usage.InputTokens != 15230 {
		t.Errorf("expected 15230, got %d", r.Usage.InputTokens)
	}
	if r.Usage.CacheCreationInputTokens != 16634 {
		t.Errorf("expected 16634, got %d", r.Usage.CacheCreationInputTokens)
	}
	if r.Usage.OutputTokens != 342 {
		t.Errorf("expected 342, got %d", r.Usage.OutputTokens)
	}

	m, ok := r.ModelUsage["claude-sonnet-4-6"]
	if !ok {
		t.Fatal("modelUsage missing claude-sonnet-4-6")
	}
	if m.InputTokens != 15230 {
		t.Errorf("expected 15230, got %d", m.InputTokens)
	}
	if m.OutputTokens != 342 {
		t.Errorf("expected 342, got %d", m.OutputTokens)
	}
	if m.CostUSD != 0.0624 {
		t.Errorf("expected 0.0624, got %f", m.CostUSD)
	}
	if m.ContextWindow != 200000 {
		t.Errorf("expected 200000, got %d", m.ContextWindow)
	}
	if m.MaxOutputTokens != 32000 {
		t.Errorf("expected 32000, got %d", m.MaxOutputTokens)
	}

	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(ev.Entries))
	}
	if ev.Entries[0].Level != "result" {
		t.Errorf("expected level result, got %s", ev.Entries[0].Level)
	}
}

func TestParseResultWithText(t *testing.T) {
	line := `{"type":"result","result":"Task completed successfully","total_cost_usd":0.01,"num_turns":2}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventResult {
		t.Fatalf("expected %s, got %s", EventResult, ev.EventType)
	}
	if ev.Result.ResultText != "Task completed successfully" {
		t.Errorf("unexpected ResultText: %s", ev.Result.ResultText)
	}
	if ev.Entries[0].Message != "Task completed successfully" {
		t.Errorf("unexpected log message: %s", ev.Entries[0].Message)
	}
}

func TestParseResultNoText(t *testing.T) {
	line := `{"type":"result","total_cost_usd":0.01}`
	ev := parseLineAt(line, testTime)
	if ev.Entries[0].Message != "Session completed" {
		t.Errorf("expected fallback message, got %s", ev.Entries[0].Message)
	}
}

// TestParseRateLimitEvent uses the real sample from PLAN.md section 18.1.
func TestParseRateLimitEvent(t *testing.T) {
	line := `{
  "type": "rate_limit_event",
  "rate_limit_info": {
    "status": "allowed_warning",
    "resetsAt": 1779584400,
    "rateLimitType": "seven_day",
    "utilization": 0.88,
    "isUsingOverage": false,
    "surpassedThreshold": 0.75
  }
}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventRateLimit {
		t.Fatalf("expected %s, got %s", EventRateLimit, ev.EventType)
	}
	if ev.RateLimit == nil {
		t.Fatal("RateLimit is nil")
	}
	rl := ev.RateLimit
	if rl.Status != "allowed_warning" {
		t.Errorf("expected allowed_warning, got %s", rl.Status)
	}
	if rl.ResetsAt != 1779584400 {
		t.Errorf("expected 1779584400, got %d", rl.ResetsAt)
	}
	if rl.RateLimitType != "seven_day" {
		t.Errorf("expected seven_day, got %s", rl.RateLimitType)
	}
	if rl.Utilization != 0.88 {
		t.Errorf("expected 0.88, got %f", rl.Utilization)
	}
	if rl.IsUsingOverage {
		t.Error("expected IsUsingOverage=false")
	}
	if rl.SurpassedThreshold != 0.75 {
		t.Errorf("expected 0.75, got %f", rl.SurpassedThreshold)
	}
}

func TestParseRateLimitMissingInfo(t *testing.T) {
	line := `{"type":"rate_limit_event"}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventUnknown {
		t.Errorf("expected %s for missing rate_limit_info, got %s", EventUnknown, ev.EventType)
	}
}

func TestParsePermissionRequest(t *testing.T) {
	line := `{"type":"permission_request","id":"perm-001","tool":"Bash","command":"rm -rf /tmp/test","description":"Delete test directory","risk_level":"high"}`

	ev := parseLineAt(line, testTime)
	if ev.EventType != EventPermission {
		t.Fatalf("expected %s, got %s", EventPermission, ev.EventType)
	}
	if ev.Permission == nil {
		t.Fatal("Permission is nil")
	}
	p := ev.Permission
	if p.ID != "perm-001" {
		t.Errorf("expected perm-001, got %s", p.ID)
	}
	if p.Tool != "Bash" {
		t.Errorf("expected Bash, got %s", p.Tool)
	}
	if p.Command != "rm -rf /tmp/test" {
		t.Errorf("unexpected command: %s", p.Command)
	}
	if p.Description != "Delete test directory" {
		t.Errorf("unexpected description: %s", p.Description)
	}
	if p.RiskLevel != "high" {
		t.Errorf("expected high, got %s", p.RiskLevel)
	}
}

func TestParsePermissionRequestFilePath(t *testing.T) {
	line := `{"type":"permission_request","id":"perm-002","tool":"Write","file_path":"/etc/hosts","risk_level":"medium"}`

	ev := parseLineAt(line, testTime)
	if ev.EventType != EventPermission {
		t.Fatalf("expected %s, got %s", EventPermission, ev.EventType)
	}
	if ev.Permission.FilePath != "/etc/hosts" {
		t.Errorf("expected /etc/hosts, got %s", ev.Permission.FilePath)
	}
	if ev.Permission.Tool != "Write" {
		t.Errorf("expected Write, got %s", ev.Permission.Tool)
	}
}

// TestParseUserReplayMessage: a replayed user turn (string content) becomes a
// single concise "user" entry carrying the text, not the raw JSON envelope.
func TestParseUserReplayMessage(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":"Read STATUS-P4.md and implement the property."},"session_id":"abc","isReplay":true}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
	}
	e := ev.Entries[0]
	if e.Level != "user" {
		t.Errorf("expected level user, got %s", e.Level)
	}
	if e.Message != "Read STATUS-P4.md and implement the property." {
		t.Errorf("unexpected message: %s", e.Message)
	}
}

// TestParseUserToolResult: a tool_result echo (string content) becomes a
// "tool_result" entry with just the output, dropping the JSON wrapper.
func TestParseUserToolResult(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"line1\nline2","is_error":false}]}}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventLog {
		t.Fatalf("expected %s, got %s", EventLog, ev.EventType)
	}
	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
	}
	e := ev.Entries[0]
	if e.Level != "tool_result" {
		t.Errorf("expected level tool_result, got %s", e.Level)
	}
	if e.Message != "line1\nline2" {
		t.Errorf("unexpected message: %q", e.Message)
	}
}

// TestParseUserToolResultError: is_error marks the entry as an error level.
func TestParseUserToolResultError(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t","type":"tool_result","content":"boom","is_error":true}]}}`
	ev := parseLineAt(line, testTime)
	if len(ev.Entries) != 1 || ev.Entries[0].Level != "error" {
		t.Fatalf("expected 1 error entry, got %+v", ev.Entries)
	}
	if ev.Entries[0].Message != "boom" {
		t.Errorf("unexpected message: %s", ev.Entries[0].Message)
	}
}

// TestParseUserToolResultBlockContent: tool_result content given as an array of
// {type,text} blocks is flattened to its text.
func TestParseUserToolResultBlockContent(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}]}}`
	ev := parseLineAt(line, testTime)
	if len(ev.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ev.Entries))
	}
	if ev.Entries[0].Message != "hello world" {
		t.Errorf("unexpected message: %q", ev.Entries[0].Message)
	}
}

// TestParseUserToolResultEmpty: empty output is labelled rather than blank.
func TestParseUserToolResultEmpty(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"","is_error":false}]}}`
	ev := parseLineAt(line, testTime)
	if len(ev.Entries) != 1 || ev.Entries[0].Message != "(no output)" {
		t.Fatalf("expected (no output) entry, got %+v", ev.Entries)
	}
}

// TestParseUserWhitespaceMessageSkipped: a blank replayed turn is dropped.
func TestParseUserWhitespaceMessageSkipped(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":"   "}}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventUnknown || len(ev.Entries) != 0 {
		t.Errorf("expected whitespace user message dropped, got %+v", ev)
	}
}

func TestParseUnknownEventType(t *testing.T) {
	line := `{"type":"future_event","data":"something"}`
	ev := parseLineAt(line, testTime)
	if ev.EventType != EventUnknown {
		t.Errorf("expected %s, got %s", EventUnknown, ev.EventType)
	}
	// Unknown JSON events still produce a system log entry
	if len(ev.Entries) != 1 || ev.Entries[0].Level != "system" {
		t.Errorf("expected system log entry for unknown event, got %+v", ev.Entries)
	}
}

func TestAbbreviateInput(t *testing.T) {
	longStr := strings.Repeat("x", 130)

	tests := []struct {
		name      string
		toolName  string
		input     json.RawMessage
		want      string
	}{
		{
			"bash short",
			"Bash",
			json.RawMessage(`{"command":"go test ./..."}`),
			"go test ./...",
		},
		{
			"bash truncated",
			"Bash",
			json.RawMessage(`{"command":"` + longStr + `"}`),
			longStr[:120] + "...",
		},
		{
			"read",
			"Read",
			json.RawMessage(`{"file_path":"/src/main.go","offset":10}`),
			"/src/main.go",
		},
		{
			"edit",
			"Edit",
			json.RawMessage(`{"file_path":"/src/handler.go","old_string":"foo","new_string":"bar"}`),
			"/src/handler.go",
		},
		{
			"write",
			"Write",
			json.RawMessage(`{"file_path":"/src/new.go","content":"package main"}`),
			"/src/new.go",
		},
		{
			"grep",
			"Grep",
			json.RawMessage(`{"pattern":"func.*Error","path":"./internal"}`),
			"func.*Error",
		},
		{
			"glob",
			"Glob",
			json.RawMessage(`{"pattern":"**/*.go"}`),
			"**/*.go",
		},
		{
			"agent",
			"Agent",
			json.RawMessage(`{"description":"Explore the codebase","prompt":"Find all tests"}`),
			"Explore the codebase",
		},
		{
			"agent truncated",
			"Agent",
			json.RawMessage(`{"description":"` + longStr + `"}`),
			longStr[:120] + "...",
		},
		{
			"unknown tool",
			"MyCustomTool",
			json.RawMessage(`{"key":"value"}`),
			`{"key":"value"}`,
		},
		{
			"empty input",
			"Bash",
			json.RawMessage(nil),
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := abbreviateInput(tc.toolName, tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("unexpected: %s", got)
	}
	if got := truncate("hello world foo", 5); got != "hello..." {
		t.Errorf("unexpected: %s", got)
	}
	if got := truncate("exact", 5); got != "exact" {
		t.Errorf("unexpected: %s", got)
	}
}
