package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// AccessLevel is the cm-mcp access tier a tool belongs to (docs/mcp.md,
// "Уровни доступа"). The tier is declared here; filtering by it is MCP-05.
type AccessLevel string

const (
	LevelRead    AccessLevel = "read"
	LevelOperate AccessLevel = "operate"
	LevelAdmin   AccessLevel = "admin"
)

// ToolAnnotations mirrors the MCP tool annotations. control must not import
// the MCP SDK (the GUI binary links this package), so cm-mcp converts it.
type ToolAnnotations struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

// Tool is one MCP tool, declared exactly once: schemas come from the Go
// input/output structs, the handler is a translation into a control-plane
// call plus a shaping of its answer.
type Tool struct {
	Name        string
	Title       string
	Description string
	Level       AccessLevel
	Annotations ToolAnnotations
	// Covers lists the exported *App methods this tool reaches; the parity
	// test (parity_test.go) checks every App method against these.
	Covers       []string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage

	translate func(args json.RawMessage) (endpoint string, body any, err error)
	shape     func(args json.RawMessage, raw any) (out any, summary string, err error)
}

// spec is the typed declaration define turns into a Tool.
type spec[In, Out any] struct {
	Name, Title, Description string
	Level                    AccessLevel
	Annotations              ToolAnnotations
	Covers                   []string
	// Translate maps decoded arguments to a control-plane call.
	Translate func(in In) (endpoint string, body any, err error)
	// Result builds the structured output from the control-plane result.
	Result func(in In, raw any) Out
	// Summary is the short text shown next to structuredContent (not a JSON dump).
	Summary func(in In, out Out) string
}

func define[In, Out any](s spec[In, Out]) Tool {
	return Tool{
		Name: s.Name, Title: s.Title, Description: s.Description,
		Level: s.Level, Annotations: s.Annotations, Covers: s.Covers,
		InputSchema:  mustSchema[In](),
		OutputSchema: mustSchema[Out](),
		translate: func(args json.RawMessage) (string, any, error) {
			in, err := decodeArgs[In](args)
			if err != nil {
				return "", nil, err
			}
			return s.Translate(in)
		},
		shape: func(args json.RawMessage, raw any) (any, string, error) {
			in, err := decodeArgs[In](args)
			if err != nil {
				return nil, "", err
			}
			out := s.Result(in, raw)
			return out, s.Summary(in, out), nil
		},
	}
}

func decodeArgs[In any](args json.RawMessage) (In, error) {
	var in In
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return in, fmt.Errorf("bad arguments: %w", err)
	}
	return in, nil
}

func mustSchema[T any]() json.RawMessage {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp tool schema: %v", err))
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("mcp tool schema: %v", err))
	}
	return b
}

// MCPTools is the ordered registry of all tools exposed via cm-mcp.
// Registration: claude mcp add cm -- cm-mcp
var MCPTools = buildMCPTools()

// TranslateTool converts a tools/call invocation into a control-plane HTTP
// request. Returns the endpoint ("/rpc" or "/wait") and a JSON-serialisable
// request body. Returns an error for unknown tools or bad arguments.
func TranslateTool(name string, args json.RawMessage) (endpoint string, body any, err error) {
	t := findTool(name)
	if t == nil {
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t.translate(args)
}

// ShapeResult turns the control-plane result of tool name into the
// structuredContent object (valid against the tool's OutputSchema) and a
// short text summary for content.
func ShapeResult(name string, args json.RawMessage, raw any) (out any, summary string, err error) {
	t := findTool(name)
	if t == nil {
		return nil, "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.shape(args, raw)
}

func findTool(name string) *Tool {
	for i := range MCPTools {
		if MCPTools[i].Name == name {
			return &MCPTools[i]
		}
	}
	return nil
}

// ── Arguments ─────────────────────────────────────────────────────────────────

// sessionRef addresses a session. session_id is the one form; project+session
// is the legacy pair, kept as an alias until MCP-17.
type sessionRef struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session ID in project/session form, e.g. lumen/P1"`
	Project   string `json:"project,omitempty" jsonschema:"Deprecated alias: project name, used together with session instead of session_id"`
	Session   string `json:"session,omitempty" jsonschema:"Deprecated alias: session name, used together with project instead of session_id"`
}

// id resolves the reference to "project/session".
func (r sessionRef) id() (string, error) {
	pair := r.Project != "" || r.Session != ""
	switch {
	case r.SessionID != "" && pair:
		return "", fmt.Errorf("pass either session_id or project+session, not both")
	case r.SessionID != "":
		return r.SessionID, nil
	case r.Project != "" && r.Session != "":
		return r.Project + "/" + r.Session, nil
	}
	return "", fmt.Errorf(`session_id is required ("project/session", e.g. "lumen/P1")`)
}

// parts resolves the reference to its project and session names.
func (r sessionRef) parts() (project, session string, err error) {
	id, err := r.id()
	if err != nil {
		return "", "", err
	}
	project, session, ok := strings.Cut(id, "/")
	if !ok || project == "" || session == "" {
		return "", "", fmt.Errorf(`session_id %q must look like "project/session"`, id)
	}
	return project, session, nil
}

type sessionIn struct{ sessionRef }

type stopIn struct {
	sessionRef
	AfterTask bool `json:"after_task,omitempty" jsonschema:"Finish the current task before stopping (soft stop)"`
}

type restartIn struct {
	sessionRef
	Resume bool `json:"resume,omitempty" jsonschema:"Resume from the saved CLI session ID instead of restarting fresh"`
}

type sendIn struct {
	sessionRef
	Text string `json:"text" jsonschema:"Message text to send"`
}

type permissionIn struct {
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
	RequestID string `json:"request_id" jsonschema:"Permission request ID from get_pending_permissions"`
	Scope     string `json:"scope,omitempty" jsonschema:"Approval scope: once, session or always (default: allow)"`
}

type settingsIn struct {
	Config map[string]any `json:"config" jsonschema:"Partial AppConfig fields to update"`
}

type preflightIn struct {
	Project string `json:"project" jsonschema:"Project name"`
	Task    string `json:"task" jsonschema:"Task description to analyse"`
}

type planIn struct {
	PlanID string `json:"plan_id" jsonschema:"Plan ID returned by run_preflight"`
}

type briefIn struct {
	ID           string `json:"id" jsonschema:"Stable brief ID, e.g. the subtask name"`
	Task         string `json:"task" jsonschema:"Self-contained task specification sent to the worker"`
	SystemPrompt string `json:"system_prompt,omitempty" jsonschema:"Optional override of the FIND/REPLACE protocol prompt"`
}

type dispatchIn struct {
	Project string `json:"project" jsonschema:"Project name"`
	BriefID string `json:"brief_id" jsonschema:"ID of a brief registered via register_mixed_brief"`
	Worker  string `json:"worker" jsonschema:"Worker name from the [[worker]] config"`
}

type projectIn struct {
	Project string `json:"project" jsonschema:"Project name"`
}

type workerWaitIn struct {
	TaskID    string `json:"task_id" jsonschema:"Task ID (project/brief/worker) as returned by dispatch_mixed_task"`
	Status    string `json:"status,omitempty" jsonschema:"Terminal status to wait for: done or needs_human; omit to match either"`
	TimeoutMs int64  `json:"timeout_ms,omitempty" jsonschema:"Timeout in milliseconds (default 60000)"`
}

type logsIn struct {
	sessionRef
	Limit int64 `json:"limit,omitempty" jsonschema:"Number of most recent entries (default 100)"`
	Tail  int64 `json:"tail,omitempty" jsonschema:"Deprecated alias of limit"`
}

type noIn struct{}

type metricsIn struct {
	Project string `json:"project,omitempty" jsonschema:"Project name; omit for the account rate-limit status"`
	Period  int64  `json:"period,omitempty" jsonschema:"Number of days to aggregate (default 7)"`
}

type waitStatusIn struct {
	sessionRef
	Status    string `json:"status" jsonschema:"Target status, e.g. working, idle, WaitingPermission, RateLimited, Error"`
	TimeoutMs int64  `json:"timeout_ms,omitempty" jsonschema:"Timeout in milliseconds (default 5000)"`
}

type waitEventIn struct {
	Event     string         `json:"event" jsonschema:"Event name, e.g. session:status, session:log, session:permission"`
	Match     map[string]any `json:"match,omitempty" jsonschema:"Key-value pairs the event data must contain (all must match)"`
	TimeoutMs int64          `json:"timeout_ms,omitempty" jsonschema:"Timeout in milliseconds (default 5000)"`
}

// ── Results ───────────────────────────────────────────────────────────────────

// sessionAck is the result of a session lifecycle action.
type sessionAck struct {
	OK        bool   `json:"ok" jsonschema:"True when the action was accepted"`
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
}

type permissionAck struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Decision  string `json:"decision" jsonschema:"The decision sent: allow, once, session, always or deny"`
}

type okOut struct {
	OK bool `json:"ok"`
}

type planOut struct {
	Plan any `json:"plan" jsonschema:"The generated task plan, including its plan_id"`
}

type planAck struct {
	OK     bool   `json:"ok"`
	PlanID string `json:"plan_id"`
}

type briefOut struct {
	BriefID string `json:"brief_id" jsonschema:"ID of the registered brief"`
}

type mixedTaskOut struct {
	Task any `json:"task" jsonschema:"Mixed-programming task state: rounds, patches, gate results, status"`
}

type mixedTasksOut struct {
	Tasks []any `json:"tasks" jsonschema:"Persisted mixed-programming tasks of the project"`
}

type sessionsOut struct {
	Sessions []any `json:"sessions" jsonschema:"All sessions with status and metrics"`
}

type logsOut struct {
	SessionID string `json:"session_id"`
	Entries   []any  `json:"entries" jsonschema:"Log entries, oldest first"`
}

type permissionsOut struct {
	Permissions []any `json:"permissions" jsonschema:"Pending permission requests across all sessions"`
}

type metricsOut struct {
	Project   string   `json:"project,omitempty"`
	Days      int64    `json:"days,omitempty"`
	CostUSD   *float64 `json:"cost_usd,omitempty" jsonschema:"Project cost over the period in USD"`
	RateLimit any      `json:"rate_limit,omitempty" jsonschema:"Account rate-limit status, when no project was given"`
}

type waitOut struct {
	Event string `json:"event" jsonschema:"Name of the event that matched"`
	Data  any    `json:"data" jsonschema:"Event payload"`
	TS    string `json:"ts,omitempty" jsonschema:"Emission time, RFC 3339"`
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func orDefault(v, dflt int64) int64 {
	if v == 0 {
		return dflt
	}
	return v
}

func list(raw any) []any {
	if l, ok := raw.([]any); ok && l != nil {
		return l
	}
	return []any{}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// sessionTool declares a lifecycle tool taking a session reference and
// answering with sessionAck; verb is the past-tense word for the summary.
func sessionTool[In any](s spec[In, sessionAck], ref func(In) sessionRef, verb string) Tool {
	s.Result = func(in In, _ any) sessionAck {
		id, _ := ref(in).id()
		return sessionAck{OK: true, SessionID: id}
	}
	s.Summary = func(_ In, out sessionAck) string { return fmt.Sprintf("Session %s %s.", out.SessionID, verb) }
	return define(s)
}

func waitResult(_ noIn, raw any) waitOut {
	m, _ := raw.(map[string]any)
	out := waitOut{Data: m["data"]}
	out.Event, _ = m["event"].(string)
	out.TS, _ = m["ts"].(string)
	return out
}

func waitSummary(out waitOut) string {
	return fmt.Sprintf("Event %s received.", out.Event)
}

func buildMCPTools() []Tool {
	op := ToolAnnotations{Idempotent: true, OpenWorld: true}
	add := ToolAnnotations{}
	ro := ToolAnnotations{ReadOnly: true}

	return []Tool{
		// ── Actions ──────────────────────────────────────────────────────────
		sessionTool(spec[sessionIn, sessionAck]{
			Name: "start_session", Title: "Start session",
			Description: "Start a session by session_id (or the legacy project+session pair).",
			Level:       LevelOperate, Annotations: op, Covers: []string{"StartSession"},
			Translate: func(in sessionIn) (string, any, error) {
				p, s, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("StartSession", map[string]string{"project": p, "session": s}), nil
			},
		}, func(in sessionIn) sessionRef { return in.sessionRef }, "started"),
		sessionTool(spec[stopIn, sessionAck]{
			Name: "stop_session", Title: "Stop session",
			Description: "Stop a session. Set after_task=true to finish the current task first (soft stop).",
			Level:       LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"StopSession"},
			Translate: func(in stopIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("StopSession", map[string]any{"id": id, "soft": in.AfterTask}), nil
			},
		}, func(in stopIn) sessionRef { return in.sessionRef }, "stop requested"),
		sessionTool(spec[sessionIn, sessionAck]{
			Name: "interrupt_session", Title: "Interrupt session",
			Description: "Interrupt the session's current action (the turn in flight: a running tool call or generation) without stopping the session; it then waits for the next send_message.",
			Level:       LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"InterruptSession"},
			Translate: func(in sessionIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("InterruptSession", map[string]string{"id": id}), nil
			},
		}, func(in sessionIn) sessionRef { return in.sessionRef }, "interrupted"),
		sessionTool(spec[restartIn, sessionAck]{
			Name: "restart_session", Title: "Restart session",
			Description: "Restart a session. Set resume=true to resume from the saved CLI session ID instead of restarting fresh.",
			Level:       LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RestartSession", "ResumeSession"},
			Translate: func(in restartIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				method := "RestartSession"
				if in.Resume {
					method = "ResumeSession"
				}
				return "/rpc", rpcCallBody(method, map[string]string{"id": id}), nil
			},
		}, func(in restartIn) sessionRef { return in.sessionRef }, "restarted"),
		sessionTool(spec[sendIn, sessionAck]{
			Name: "send_message", Title: "Send message",
			Description: "Send a user message to an active bidirectional session.",
			Level:       LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"SendMessage"},
			Translate: func(in sendIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("SendMessage", map[string]string{"id": id, "message": in.Text}), nil
			},
		}, func(in sendIn) sessionRef { return in.sessionRef }, "message sent"),
		define(spec[permissionIn, permissionAck]{
			Name: "approve_permission", Title: "Approve permission",
			Description: "Approve a pending permission request for a session (on the human's behalf).",
			Level:       LevelAdmin, Annotations: add, Covers: []string{"RespondPermission"},
			Translate: func(in permissionIn) (string, any, error) {
				decision := in.Scope
				if decision == "" {
					decision = "allow"
				}
				return "/rpc", rpcCallBody("RespondPermission", map[string]string{
					"id": in.SessionID, "request_id": in.RequestID, "decision": decision,
				}), nil
			},
			Result: func(in permissionIn, _ any) permissionAck {
				decision := in.Scope
				if decision == "" {
					decision = "allow"
				}
				return permissionAck{OK: true, SessionID: in.SessionID, RequestID: in.RequestID, Decision: decision}
			},
			Summary: func(_ permissionIn, out permissionAck) string {
				return fmt.Sprintf("Permission %s approved (%s) for %s.", out.RequestID, out.Decision, out.SessionID)
			},
		}),
		define(spec[permissionIn, permissionAck]{
			Name: "deny_permission", Title: "Deny permission",
			Description: "Deny a pending permission request for a session.",
			Level:       LevelOperate, Annotations: add, Covers: []string{"RespondPermission"},
			Translate: func(in permissionIn) (string, any, error) {
				return "/rpc", rpcCallBody("RespondPermission", map[string]string{
					"id": in.SessionID, "request_id": in.RequestID, "decision": "deny",
				}), nil
			},
			Result: func(in permissionIn, _ any) permissionAck {
				return permissionAck{OK: true, SessionID: in.SessionID, RequestID: in.RequestID, Decision: "deny"}
			},
			Summary: func(_ permissionIn, out permissionAck) string {
				return fmt.Sprintf("Permission %s denied for %s.", out.RequestID, out.SessionID)
			},
		}),
		define(spec[settingsIn, okOut]{
			Name: "set_global_settings", Title: "Update global settings",
			Description: "Update global application settings (AppConfig partial update).",
			Level:       LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"UpdateConfig"},
			Translate: func(in settingsIn) (string, any, error) {
				cfg := in.Config
				if cfg == nil {
					cfg = map[string]any{}
				}
				return "/rpc", rpcCallBody("UpdateConfig", map[string]any{"config": cfg}), nil
			},
			Result:  func(settingsIn, any) okOut { return okOut{OK: true} },
			Summary: func(settingsIn, okOut) string { return "Global settings updated." },
		}),
		define(spec[preflightIn, planOut]{
			Name: "run_preflight", Title: "Run preflight analysis",
			Description: "Run a preflight analysis session to generate a task plan for a project.",
			Level:       LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RunPreflight"},
			Translate: func(in preflightIn) (string, any, error) {
				return "/rpc", rpcCallBody("RunPreflight", map[string]string{"project": in.Project, "task": in.Task}), nil
			},
			Result:  func(_ preflightIn, raw any) planOut { return planOut{Plan: raw} },
			Summary: func(in preflightIn, _ planOut) string { return fmt.Sprintf("Plan generated for %s.", in.Project) },
		}),
		define(spec[planIn, planAck]{
			Name: "execute_plan", Title: "Execute plan",
			Description: "Execute a pre-generated task plan by plan ID.",
			Level:       LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"ExecutePlan"},
			Translate: func(in planIn) (string, any, error) {
				return "/rpc", rpcCallBody("ExecutePlan", map[string]string{"plan_id": in.PlanID}), nil
			},
			Result:  func(in planIn, _ any) planAck { return planAck{OK: true, PlanID: in.PlanID} },
			Summary: func(in planIn, _ planAck) string { return fmt.Sprintf("Plan %s started.", in.PlanID) },
		}),
		// ── Mixed programming (MIXED-TASKS.md MP-07) ─────────────────────────
		define(spec[briefIn, briefOut]{
			Name: "register_mixed_brief", Title: "Register mixed brief",
			Description: "Register a mixed-programming brief so it can be dispatched to a worker model. The brief must be self-contained: task description in English, verbatim code excerpts, accepted decisions, target files.",
			Level:       LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"RegisterMixedBrief"},
			Translate: func(in briefIn) (string, any, error) {
				return "/rpc", rpcCallBody("RegisterMixedBrief", map[string]string{
					"id": in.ID, "task": in.Task, "system_prompt": in.SystemPrompt,
				}), nil
			},
			Result: func(in briefIn, raw any) briefOut {
				if id, ok := raw.(string); ok && id != "" {
					return briefOut{BriefID: id}
				}
				return briefOut{BriefID: in.ID}
			},
			Summary: func(_ briefIn, out briefOut) string { return fmt.Sprintf("Brief %s registered.", out.BriefID) },
		}),
		define(spec[dispatchIn, mixedTaskOut]{
			Name: "dispatch_mixed_task", Title: "Dispatch mixed task",
			Description: "Run the mixed-programming round loop (brief -> patches -> gates -> feedback) for a registered brief against a configured worker model. Blocks until the task is done or needs_human; use wait_for_worker_status/get_mixed_rounds to observe progress from another call.",
			Level:       LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"DispatchMixedTask"},
			Translate: func(in dispatchIn) (string, any, error) {
				return "/rpc", rpcCallBody("DispatchMixedTask", map[string]string{
					"project": in.Project, "brief_id": in.BriefID, "worker": in.Worker,
				}), nil
			},
			Result: func(_ dispatchIn, raw any) mixedTaskOut { return mixedTaskOut{Task: raw} },
			Summary: func(in dispatchIn, _ mixedTaskOut) string {
				return fmt.Sprintf("Brief %s dispatched to worker %s.", in.BriefID, in.Worker)
			},
		}),
		define(spec[projectIn, mixedTasksOut]{
			Name: "get_mixed_rounds", Title: "Get mixed-programming rounds",
			Description: "Get persisted mixed-programming task state (rounds, patches, gate results, status) for a project.",
			Level:       LevelRead, Annotations: ro, Covers: []string{"GetMixedRounds"},
			Translate: func(in projectIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetMixedRounds", map[string]string{"project": in.Project}), nil
			},
			Result: func(_ projectIn, raw any) mixedTasksOut { return mixedTasksOut{Tasks: list(raw)} },
			Summary: func(in projectIn, out mixedTasksOut) string {
				return fmt.Sprintf("%s in %s.", plural(len(out.Tasks), "mixed task", "mixed tasks"), in.Project)
			},
		}),
		define(spec[workerWaitIn, waitOut]{
			Name: "wait_for_worker_status", Title: "Wait for worker status",
			Description: "Block until a mixed-programming task reaches a terminal status (done or needs_human), or until timeout. Checks past events first, so it can be called after dispatch_mixed_task returned.",
			Level:       LevelRead, Annotations: ro, Covers: nil,
			Translate: func(in workerWaitIn) (string, any, error) {
				match := map[string]any{"task_id": in.TaskID}
				if in.Status != "" {
					match["status"] = in.Status
				}
				return "/wait", waitCallBody("worker:done", match, orDefault(in.TimeoutMs, 60000)), nil
			},
			Result:  func(_ workerWaitIn, raw any) waitOut { return waitResult(noIn{}, raw) },
			Summary: func(_ workerWaitIn, out waitOut) string { return waitSummary(out) },
		}),
		// ── Queries ───────────────────────────────────────────────────────────
		define(spec[noIn, sessionsOut]{
			Name: "get_sessions", Title: "List sessions",
			Description: "Get all sessions with their current status and metrics.",
			Level:       LevelRead, Annotations: ro, Covers: []string{"GetAllSessions"},
			Translate: func(noIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetAllSessions", map[string]any{}), nil
			},
			Result: func(_ noIn, raw any) sessionsOut { return sessionsOut{Sessions: list(raw)} },
			Summary: func(_ noIn, out sessionsOut) string {
				return plural(len(out.Sessions), "session", "sessions") + "."
			},
		}),
		define(spec[logsIn, logsOut]{
			Name: "get_session_logs", Title: "Get session log",
			Description: "Get log entries for a session.",
			Level:       LevelRead, Annotations: ro, Covers: []string{"GetSessionLog"},
			Translate: func(in logsIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				limit := orDefault(in.Limit, orDefault(in.Tail, 100))
				return "/rpc", rpcCallBody("GetSessionLog", map[string]any{"id": id, "offset": 0, "limit": limit}), nil
			},
			Result: func(in logsIn, raw any) logsOut {
				id, _ := in.id()
				return logsOut{SessionID: id, Entries: list(raw)}
			},
			Summary: func(_ logsIn, out logsOut) string {
				return fmt.Sprintf("%s from %s.", plural(len(out.Entries), "log entry", "log entries"), out.SessionID)
			},
		}),
		define(spec[noIn, permissionsOut]{
			Name: "get_pending_permissions", Title: "List pending permissions",
			Description: "Get all pending permission requests across all sessions.",
			Level:       LevelRead, Annotations: ro, Covers: []string{"GetPendingPermissions"},
			Translate: func(noIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetPendingPermissions", map[string]any{}), nil
			},
			Result: func(_ noIn, raw any) permissionsOut { return permissionsOut{Permissions: list(raw)} },
			Summary: func(_ noIn, out permissionsOut) string {
				return plural(len(out.Permissions), "pending permission", "pending permissions") + "."
			},
		}),
		define(spec[metricsIn, metricsOut]{
			Name: "get_metrics", Title: "Get cost and rate-limit metrics",
			Description: "Get cost and token metrics for a project over a time period; without a project, the account rate-limit status.",
			Level:       LevelRead, Annotations: ro, Covers: []string{"GetProjectCost", "GetRateLimitStatus"},
			Translate: func(in metricsIn) (string, any, error) {
				if in.Project != "" {
					return "/rpc", rpcCallBody("GetProjectCost", map[string]any{
						"project": in.Project, "days": orDefault(in.Period, 7),
					}), nil
				}
				return "/rpc", rpcCallBody("GetRateLimitStatus", map[string]any{}), nil
			},
			Result: func(in metricsIn, raw any) metricsOut {
				if in.Project == "" {
					return metricsOut{RateLimit: raw}
				}
				out := metricsOut{Project: in.Project, Days: orDefault(in.Period, 7)}
				if c, ok := raw.(float64); ok {
					out.CostUSD = &c
				}
				return out
			},
			Summary: func(_ metricsIn, out metricsOut) string {
				if out.CostUSD == nil {
					return "Rate-limit status."
				}
				return fmt.Sprintf("%s: $%.2f over %d days.", out.Project, *out.CostUSD, out.Days)
			},
		}),
		// ── Blocking primitives ───────────────────────────────────────────────
		define(spec[waitStatusIn, waitOut]{
			Name: "wait_for_status", Title: "Wait for session status",
			Description: "Block until a session reaches the specified status, or until timeout. Reliable alternative to polling: use after start_session, send_message, or approve_permission.",
			Level:       LevelRead, Annotations: ro,
			Translate: func(in waitStatusIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/wait", waitCallBody("session:status", map[string]any{"id": id, "status": in.Status}, orDefault(in.TimeoutMs, 5000)), nil
			},
			Result:  func(_ waitStatusIn, raw any) waitOut { return waitResult(noIn{}, raw) },
			Summary: func(_ waitStatusIn, out waitOut) string { return waitSummary(out) },
		}),
		define(spec[waitEventIn, waitOut]{
			Name: "wait_for_event", Title: "Wait for event",
			Description: "Block until a matching event is emitted by the control-plane, or until timeout. Checks the ring buffer first so past events are not missed.",
			Level:       LevelRead, Annotations: ro,
			Translate: func(in waitEventIn) (string, any, error) {
				return "/wait", waitCallBody(in.Event, in.Match, orDefault(in.TimeoutMs, 5000)), nil
			},
			Result:  func(_ waitEventIn, raw any) waitOut { return waitResult(noIn{}, raw) },
			Summary: func(_ waitEventIn, out waitOut) string { return waitSummary(out) },
		}),
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
