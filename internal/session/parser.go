package session

import (
	"encoding/json"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// ---- Types from PLAN.md section 18.2 ----

// TokenUsage holds per-turn token counts from an assistant message.
type TokenUsage struct {
	InputTokens              int    `json:"input_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	ServiceTier              string `json:"service_tier"`
}

// ModelUsage holds per-model cost and token breakdown from a result event.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
}

// SessionResult holds the final metrics from a result event.
type SessionResult struct {
	TotalCostUSD  float64               `json:"total_cost_usd"`
	DurationMs    int64                 `json:"duration_ms"`
	DurationApiMs int64                 `json:"duration_api_ms"`
	NumTurns      int                   `json:"num_turns"`
	Usage         TokenUsage            `json:"usage"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage"`
	StopReason    string                `json:"stop_reason"`
	ResultText    string                `json:"result"`
}

// RateLimitInfo holds rate limit status from a rate_limit_event.
type RateLimitInfo struct {
	Status             string  `json:"status"`
	ResetsAt           int64   `json:"resetsAt"`
	RateLimitType      string  `json:"rateLimitType"`
	Utilization        float64 `json:"utilization"`
	IsUsingOverage     bool    `json:"isUsingOverage"`
	SurpassedThreshold float64 `json:"surpassedThreshold"`
}

// MCPServer describes a connected MCP server from a system/init event.
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// InitInfo holds session metadata from a system/init event.
type InitInfo struct {
	SessionID         string      `json:"session_id"`
	Model             string      `json:"model"`
	Tools             []string    `json:"tools"`
	MCPServers        []MCPServer `json:"mcp_servers"`
	PermissionMode    string      `json:"permissionMode"`
	ClaudeCodeVersion string      `json:"claude_code_version"`
	CWD               string      `json:"cwd"`
}

// PermissionRequest describes a permission request emitted by Claude CLI.
type PermissionRequest struct {
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
	Command     string `json:"command"`
	FilePath    string `json:"file_path"`
	RiskLevel   string `json:"risk_level"`
}

// ---- ParsedEvent ----

const (
	EventLog        = "log"
	EventResult     = "result"
	EventRateLimit  = "rate_limit"
	EventInit       = "init"
	EventPermission = "permission_request"
	EventUnknown    = "unknown"
)

// ParsedEvent is the result of parsing one line of Claude CLI stream-json output.
type ParsedEvent struct {
	EventType  string
	Entries    []config.LogEntry  // populated for EventLog and EventResult
	Result     *SessionResult     // non-nil for EventResult
	RateLimit  *RateLimitInfo     // non-nil for EventRateLimit
	Init       *InitInfo          // non-nil for EventInit
	Permission *PermissionRequest // non-nil for EventPermission
	Usage      *TokenUsage        // per-turn usage from assistant messages
}

// ---- Internal raw JSON structures ----

type rawStreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init fields
	SessionID         string      `json:"session_id"`
	Model             string      `json:"model"`
	Tools             []string    `json:"tools"`
	MCPServers        []MCPServer `json:"mcp_servers"`
	PermissionMode    string      `json:"permissionMode"`
	ClaudeCodeVersion string      `json:"claude_code_version"`
	CWD               string      `json:"cwd"`

	// assistant fields
	Message *rawAssistantMessage `json:"message"`

	// result event fields
	TotalCostUSD  float64               `json:"total_cost_usd"`
	DurationMs    int64                 `json:"duration_ms"`
	DurationApiMs int64                 `json:"duration_api_ms"`
	NumTurns      int                   `json:"num_turns"`
	Usage         *TokenUsage           `json:"usage"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage"`
	StopReason    string                `json:"stop_reason"`
	ResultText    string                `json:"result"`

	// rate_limit_event fields
	RateLimitInfo *RateLimitInfo `json:"rate_limit_info"`

	// permission_request fields
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
	Command     string `json:"command"`
	FilePath    string `json:"file_path"`
	RiskLevel   string `json:"risk_level"`
}

type rawAssistantMessage struct {
	Model   string       `json:"model"`
	Usage   *TokenUsage  `json:"usage"`
	Content []rawContent `json:"content"`
}

type rawContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Thinking string          `json:"thinking"`
}

// ---- Public API ----

// ParseLine parses a single line of Claude CLI stream-json output.
func ParseLine(line string) ParsedEvent {
	return parseLineAt(line, time.Now())
}

// parseLineAt is ParseLine with an injectable timestamp for testing.
func parseLineAt(line string, now time.Time) ParsedEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return ParsedEvent{EventType: EventUnknown}
	}

	var ev rawStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return ParsedEvent{
			EventType: EventLog,
			Entries: []config.LogEntry{{
				Time:    now,
				Level:   "system",
				Source:  "claude",
				Message: line,
			}},
		}
	}

	switch ev.Type {
	case "system":
		return handleSystem(ev, now)
	case "assistant":
		return handleAssistant(ev, now)
	case "result":
		return handleResult(ev, now)
	case "rate_limit_event":
		return handleRateLimit(ev)
	case "permission_request":
		return handlePermission(ev)
	case "stream_event":
		// Partial-message deltas emitted by --include-partial-messages. The full
		// "assistant" message follows, so these are redundant; drop them silently
		// instead of flooding the log with raw JSON.
		return ParsedEvent{EventType: EventUnknown}
	default:
		return ParsedEvent{
			EventType: EventUnknown,
			Entries: []config.LogEntry{{
				Time:    now,
				Level:   "system",
				Source:  "claude",
				Message: line,
			}},
		}
	}
}

// ---- Event handlers ----

func handleSystem(ev rawStreamEvent, now time.Time) ParsedEvent {
	if ev.Subtype != "init" {
		return ParsedEvent{EventType: EventUnknown}
	}
	info := &InitInfo{
		SessionID:         ev.SessionID,
		Model:             ev.Model,
		Tools:             ev.Tools,
		MCPServers:        ev.MCPServers,
		PermissionMode:    ev.PermissionMode,
		ClaudeCodeVersion: ev.ClaudeCodeVersion,
		CWD:               ev.CWD,
	}
	msg := "Session initialized"
	if info.Model != "" {
		msg += ": " + info.Model
	}
	if info.ClaudeCodeVersion != "" {
		msg += " v" + info.ClaudeCodeVersion
	}
	return ParsedEvent{
		EventType: EventInit,
		Init:      info,
		Entries: []config.LogEntry{{
			Time:    now,
			Level:   "system",
			Source:  "claude",
			Message: msg,
		}},
	}
}

func handleAssistant(ev rawStreamEvent, now time.Time) ParsedEvent {
	if ev.Message == nil {
		return ParsedEvent{EventType: EventUnknown}
	}
	var entries []config.LogEntry
	for _, c := range ev.Message.Content {
		switch c.Type {
		case "text":
			if strings.TrimSpace(c.Text) != "" {
				entries = append(entries, config.LogEntry{
					Time:    now,
					Level:   "text",
					Source:  "claude",
					Message: c.Text,
				})
			}
		case "tool_use":
			abbrev := abbreviateInput(c.Name, c.Input)
			entries = append(entries, config.LogEntry{
				Time:      now,
				Level:     "tool",
				Source:    "claude",
				Message:   c.Name + ": " + abbrev,
				ToolName:  c.Name,
				ToolInput: abbrev,
			})
		case "thinking":
			if strings.TrimSpace(c.Thinking) != "" {
				entries = append(entries, config.LogEntry{
					Time:    now,
					Level:   "thinking",
					Source:  "claude",
					Message: c.Thinking,
				})
			}
		}
	}
	return ParsedEvent{
		EventType: EventLog,
		Entries:   entries,
		Usage:     ev.Message.Usage,
	}
}

func handleResult(ev rawStreamEvent, now time.Time) ParsedEvent {
	usage := TokenUsage{}
	if ev.Usage != nil {
		usage = *ev.Usage
	}
	result := &SessionResult{
		TotalCostUSD:  ev.TotalCostUSD,
		DurationMs:    ev.DurationMs,
		DurationApiMs: ev.DurationApiMs,
		NumTurns:      ev.NumTurns,
		Usage:         usage,
		ModelUsage:    ev.ModelUsage,
		StopReason:    ev.StopReason,
		ResultText:    ev.ResultText,
	}
	msg := ev.ResultText
	if msg == "" {
		msg = "Session completed"
	}
	return ParsedEvent{
		EventType: EventResult,
		Result:    result,
		Entries: []config.LogEntry{{
			Time:    now,
			Level:   "result",
			Source:  "claude",
			Message: msg,
		}},
	}
}

func handleRateLimit(ev rawStreamEvent) ParsedEvent {
	if ev.RateLimitInfo == nil {
		return ParsedEvent{EventType: EventUnknown}
	}
	return ParsedEvent{
		EventType: EventRateLimit,
		RateLimit: ev.RateLimitInfo,
	}
}

func handlePermission(ev rawStreamEvent) ParsedEvent {
	return ParsedEvent{
		EventType: EventPermission,
		Permission: &PermissionRequest{
			ID:          ev.ID,
			Tool:        ev.Tool,
			Description: ev.Description,
			Command:     ev.Command,
			FilePath:    ev.FilePath,
			RiskLevel:   ev.RiskLevel,
		},
	}
}

// ---- Helpers ----

// abbreviateInput returns a short display string for a tool_use input.
// Rules from PLAN.md section 6.3.
func abbreviateInput(toolName string, inputJSON json.RawMessage) string {
	if len(inputJSON) == 0 {
		return ""
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(inputJSON, &m); err != nil {
		return truncate(string(inputJSON), 120)
	}

	getString := func(key string) (string, bool) {
		v, ok := m[key]
		if !ok {
			return "", false
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", false
		}
		return s, true
	}

	switch toolName {
	case "Bash":
		if cmd, ok := getString("command"); ok {
			return truncate(cmd, 120)
		}
	case "Read", "Edit", "Write":
		if path, ok := getString("file_path"); ok {
			return path
		}
	case "Glob", "Grep":
		if pat, ok := getString("pattern"); ok {
			return pat
		}
	case "Agent":
		if desc, ok := getString("description"); ok {
			return truncate(desc, 120)
		}
	}

	return truncate(string(inputJSON), 120)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
