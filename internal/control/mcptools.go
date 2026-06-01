package control

import (
	"encoding/json"
	"fmt"
)

// ToolDef is one MCP tool definition exposed via cm-mcp.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPTools is the ordered list of all tools exposed via cm-mcp.
// Registration: claude mcp add cm -- cm-mcp
var MCPTools = buildMCPTools()

func buildMCPTools() []ToolDef {
	return []ToolDef{
		// ── Actions ──────────────────────────────────────────────────────────
		{
			Name:        "start_session",
			Description: "Start a session by project and name.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string","description":"Project name"},"session":{"type":"string","description":"Session name"}},"required":["project","session"]}`),
		},
		{
			Name:        "stop_session",
			Description: "Stop a session. Set after_task=true to finish the current task first (soft stop).",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"session":{"type":"string"},"after_task":{"type":"boolean","description":"Finish current task before stopping (soft stop)"}},"required":["project","session"]}`),
		},
		{
			Name:        "restart_session",
			Description: "Restart a session. Set resume=true to resume from the saved CLI session ID instead of restarting fresh.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"session":{"type":"string"},"resume":{"type":"boolean","description":"Resume from saved CLI session ID"}},"required":["project","session"]}`),
		},
		{
			Name:        "send_message",
			Description: "Send a user message to an active bidirectional session.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"session":{"type":"string"},"text":{"type":"string","description":"Message text to send"}},"required":["project","session","text"]}`),
		},
		{
			Name:        "approve_permission",
			Description: "Approve a pending permission request for a session.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID in project/session format"},"request_id":{"type":"string","description":"Permission request ID"},"scope":{"type":"string","enum":["once","session","always"],"description":"Approval scope (default: allow)"}},"required":["session_id","request_id"]}`),
		},
		{
			Name:        "deny_permission",
			Description: "Deny a pending permission request for a session.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID in project/session format"},"request_id":{"type":"string","description":"Permission request ID"},"scope":{"type":"string","enum":["once","session","always"]}},"required":["session_id","request_id"]}`),
		},
		{
			Name:        "set_global_settings",
			Description: "Update global application settings (AppConfig partial update).",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"config":{"type":"object","description":"Partial AppConfig fields to update"}},"required":["config"]}`),
		},
		{
			Name:        "run_preflight",
			Description: "Run a preflight analysis session to generate a task plan for a project.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"task":{"type":"string","description":"Task description to analyse"}},"required":["project","task"]}`),
		},
		{
			Name:        "execute_plan",
			Description: "Execute a pre-generated task plan by plan ID.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}`),
		},
		// ── Queries ───────────────────────────────────────────────────────────
		{
			Name:        "get_sessions",
			Description: "Get all sessions with their current status and metrics.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_session_logs",
			Description: "Get log entries for a session.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"session":{"type":"string"},"tail":{"type":"integer","description":"Number of most recent entries (default 100)"}},"required":["project","session"]}`),
		},
		{
			Name:        "get_pending_permissions",
			Description: "Get all pending permission requests across all sessions.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_metrics",
			Description: "Get cost and token metrics for a project over a time period.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string","description":"Project name (required for project cost)"},"period":{"type":"integer","description":"Number of days to aggregate (default 7)"}}}`),
		},
		// ── Blocking primitives ───────────────────────────────────────────────
		{
			Name:        "wait_for_status",
			Description: "Block until a session reaches the specified status, or until timeout. Reliable alternative to polling: use after start_session, send_message, or approve_permission.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"project":{"type":"string"},"session":{"type":"string"},"status":{"type":"string","description":"Target status (e.g. working, idle, WaitingPermission, RateLimited, Error)"},"timeout_ms":{"type":"integer","description":"Timeout in milliseconds (default 5000)"}},"required":["project","session","status"]}`),
		},
		{
			Name:        "wait_for_event",
			Description: "Block until a matching event is emitted by the control-plane, or until timeout. Checks the ring buffer first so past events are not missed.",
			InputSchema: mustSchemaJSON(`{"type":"object","properties":{"event":{"type":"string","description":"Event name (e.g. session:status, session:log, session:permission)"},"match":{"type":"object","description":"Key-value pairs the event data must contain (all must match)"},"timeout_ms":{"type":"integer","description":"Timeout in milliseconds (default 5000)"}},"required":["event"]}`),
		},
	}
}

// TranslateTool converts a tools/call invocation into a control-plane HTTP
// request. Returns the endpoint ("/rpc" or "/wait") and a JSON-serialisable
// request body. Returns an error for unknown tools or bad arguments.
func TranslateTool(name string, args json.RawMessage) (endpoint string, body any, err error) {
	var a map[string]json.RawMessage
	if err := json.Unmarshal(args, &a); err != nil {
		return "", nil, fmt.Errorf("bad arguments: %w", err)
	}

	str := func(key string) string {
		v, ok := a[key]
		if !ok {
			return ""
		}
		var s string
		_ = json.Unmarshal(v, &s)
		return s
	}
	boolean := func(key string) bool {
		v, ok := a[key]
		if !ok {
			return false
		}
		var b bool
		_ = json.Unmarshal(v, &b)
		return b
	}
	integer := func(key string, dflt int64) int64 {
		v, ok := a[key]
		if !ok {
			return dflt
		}
		var n int64
		_ = json.Unmarshal(v, &n)
		if n == 0 {
			return dflt
		}
		return n
	}
	sessionID := func() string {
		p, s := str("project"), str("session")
		if p != "" && s != "" {
			return p + "/" + s
		}
		return ""
	}

	switch name {
	// ── Actions ──────────────────────────────────────────────────────────────

	case "start_session":
		return "/rpc", rpcCallBody("StartSession", map[string]string{
			"project": str("project"),
			"session": str("session"),
		}), nil

	case "stop_session":
		return "/rpc", rpcCallBody("StopSession", map[string]any{
			"id":   sessionID(),
			"soft": boolean("after_task"),
		}), nil

	case "restart_session":
		if boolean("resume") {
			return "/rpc", rpcCallBody("ResumeSession", map[string]string{
				"id": sessionID(),
			}), nil
		}
		return "/rpc", rpcCallBody("RestartSession", map[string]string{
			"id": sessionID(),
		}), nil

	case "send_message":
		return "/rpc", rpcCallBody("SendMessage", map[string]string{
			"id":      sessionID(),
			"message": str("text"),
		}), nil

	case "approve_permission":
		scope := str("scope")
		if scope == "" {
			scope = "allow"
		}
		return "/rpc", rpcCallBody("RespondPermission", map[string]string{
			"id":         str("session_id"),
			"request_id": str("request_id"),
			"decision":   scope,
		}), nil

	case "deny_permission":
		return "/rpc", rpcCallBody("RespondPermission", map[string]string{
			"id":         str("session_id"),
			"request_id": str("request_id"),
			"decision":   "deny",
		}), nil

	case "set_global_settings":
		rawCfg, ok := a["config"]
		if !ok {
			rawCfg = json.RawMessage(`{}`)
		}
		var cfg any
		_ = json.Unmarshal(rawCfg, &cfg)
		return "/rpc", rpcCallBody("UpdateConfig", map[string]any{"config": cfg}), nil

	case "run_preflight":
		return "/rpc", rpcCallBody("RunPreflight", map[string]string{
			"project": str("project"),
			"task":    str("task"),
		}), nil

	case "execute_plan":
		return "/rpc", rpcCallBody("ExecutePlan", map[string]string{
			"plan_id": str("plan_id"),
		}), nil

	// ── Queries ───────────────────────────────────────────────────────────────

	case "get_sessions":
		return "/rpc", rpcCallBody("GetAllSessions", map[string]any{}), nil

	case "get_session_logs":
		tail := integer("tail", 100)
		return "/rpc", rpcCallBody("GetSessionLog", map[string]any{
			"id":     sessionID(),
			"offset": 0,
			"limit":  tail,
		}), nil

	case "get_pending_permissions":
		return "/rpc", rpcCallBody("GetPendingPermissions", map[string]any{}), nil

	case "get_metrics":
		project := str("project")
		period := int(integer("period", 7))
		if project != "" {
			return "/rpc", rpcCallBody("GetProjectCost", map[string]any{
				"project": project,
				"days":    period,
			}), nil
		}
		return "/rpc", rpcCallBody("GetRateLimitStatus", map[string]any{}), nil

	// ── Blocking primitives ───────────────────────────────────────────────────

	case "wait_for_status":
		return "/wait", waitCallBody("session:status", map[string]any{
			"id":     sessionID(),
			"status": str("status"),
		}, integer("timeout_ms", 5000)), nil

	case "wait_for_event":
		event := str("event")
		timeoutMs := integer("timeout_ms", 5000)
		var match map[string]any
		if mv, ok := a["match"]; ok {
			_ = json.Unmarshal(mv, &match)
		}
		return "/wait", waitCallBody(event, match, timeoutMs), nil

	default:
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// rpcCallBody builds a JSON-RPC 2.0 request body for POST /rpc.
func rpcCallBody(method string, params any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
}

// waitCallBody builds a request body for POST /wait.
func waitCallBody(event string, match any, timeoutMs int64) map[string]any {
	return map[string]any{
		"event":      event,
		"match":      match,
		"timeout_ms": timeoutMs,
	}
}

func mustSchemaJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}
