package control

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// ── Arguments ─────────────────────────────────────────────────────────────────

// sessionRef addresses a session by its "project/session" ID.
type sessionRef struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session ID in project/session form, e.g. lumen/P1; list them with get_sessions"`
}

// id resolves the reference to "project/session".
func (r sessionRef) id() (string, error) {
	if r.SessionID != "" {
		return r.SessionID, nil
	}
	return "", fmt.Errorf(`session_id is required ("project/session", e.g. "lumen/P1"); list sessions with get_sessions`)
}

// parts resolves the reference to its project and session names.
func (r sessionRef) parts() (project, session string, err error) {
	id, err := r.id()
	if err != nil {
		return "", "", err
	}
	project, session, ok := strings.Cut(id, "/")
	if !ok || project == "" || session == "" {
		return "", "", fmt.Errorf(`session_id %q must look like "project/session", e.g. "lumen/P1"; list sessions with get_sessions`, id)
	}
	return project, session, nil
}

// Response size limits (docs/mcp.md, "Ошибки и размер ответа"): a list tool
// returns at most DefaultPageSize items unless asked for more, never more than
// MaxPageSize, and never more than MaxResponseBytes of structured content.
const (
	DefaultPageSize  = 100
	MaxPageSize      = 500
	MaxResponseBytes = 64 * 1024
)

// pageIn is the paging part of every list tool's input.
type pageIn struct {
	Limit  int64  `json:"limit,omitempty" jsonschema:"Maximum number of items to return, 1-500 (default 100)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Position to continue from: the next_cursor of the previous response, e.g. 100; omit for the first page"`
}

// window validates the paging arguments and returns the start offset and the
// page size.
func (p pageIn) window() (offset, limit int, err error) {
	if p.Cursor != "" {
		offset, err = strconv.Atoi(p.Cursor)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("cursor %q is not valid: pass the next_cursor value from the previous response unchanged (a non-negative integer), or omit it for the first page", p.Cursor)
		}
	}
	limit = DefaultPageSize
	if p.Limit != 0 {
		if p.Limit < 1 || p.Limit > MaxPageSize {
			return 0, 0, fmt.Errorf("limit %d is out of range: use 1-%d and read further pages with cursor/next_cursor", p.Limit, MaxPageSize)
		}
		limit = int(p.Limit)
	}
	return offset, limit, nil
}

// pageOut is the paging part of every list tool's output.
type pageOut struct {
	NextCursor string `json:"next_cursor,omitempty" jsonschema:"Pass as cursor to get the next page; absent when this is the last page"`
	Truncated  bool   `json:"truncated" jsonschema:"True when more items exist than were returned (page limit or response size limit)"`
}

// pageOf cuts items[from:] to at most limit items and MaxResponseBytes, keeping
// at least one item. next is the index in items of the first item not returned.
func pageOf(items []any, from, limit int) (page []any, next int, more bool) {
	if from > len(items) {
		from = len(items)
	}
	end, size := from, 0
	for end < len(items) && end-from < limit {
		b, _ := json.Marshal(items[end])
		if end > from && size+len(b) > MaxResponseBytes {
			break
		}
		size += len(b)
		end++
	}
	return items[from:end], end, end < len(items)
}

// pageInfo turns pageOf's position into the output fields; base is the
// absolute index of items[0].
func pageInfo(base, next int, more bool) pageOut {
	if !more {
		return pageOut{}
	}
	return pageOut{NextCursor: strconv.Itoa(base + next), Truncated: true}
}

// moreHint is the sentence appended to a list summary when output was cut.
func moreHint(p pageOut) string {
	if !p.Truncated {
		return ""
	}
	return fmt.Sprintf(" More available: call again with cursor=%q.", p.NextCursor)
}

type sessionIn struct{ sessionRef }

type stopIn struct {
	sessionRef
	AfterTask bool `json:"after_task,omitempty" jsonschema:"If true, finish the current task before stopping (soft stop); default false stops at once"`
}

type restartIn struct {
	sessionRef
	Resume bool `json:"resume,omitempty" jsonschema:"If true, resume the saved CLI conversation instead of starting fresh; default false"`
}

type imageIn struct {
	Path       string `json:"path,omitempty" jsonschema:"Absolute path of a png, jpg, gif or webp file on the machine running Claude Manager, e.g. C:/shots/bug.png; use this or data_base64"`
	DataBase64 string `json:"data_base64,omitempty" jsonschema:"Base64 of the image bytes, standard alphabet, no data: prefix; use this or path"`
	MediaType  string `json:"media_type,omitempty" jsonschema:"image/png, image/jpeg, image/gif or image/webp; guessed from the file extension for path, required for data_base64"`
}

type sendIn struct {
	sessionRef
	Text   string    `json:"text" jsonschema:"Message text to send to the session, e.g. Continue with the next task"`
	Images []imageIn `json:"images,omitempty" jsonschema:"Images to attach, at most 4, each at most 5 MiB"`
}

type startIn struct {
	sessionRef
	Model   string `json:"model,omitempty" jsonschema:"Model for this and later starts, e.g. opus or sonnet; omit to keep the configured one"`
	Effort  string `json:"effort,omitempty" jsonschema:"Reasoning effort, e.g. low, medium, high, xhigh or max; omit to keep the configured one"`
	Runtime string `json:"runtime,omitempty" jsonschema:"Agent CLI to drive the session: claude or hermes; persisted in the session config; omit to keep the configured one"`
}

type sessionSlashIn struct {
	sessionRef
	pageIn
}

type confirmIn struct {
	Confirm bool `json:"confirm" jsonschema:"Must be true: acknowledges that sessions of every project are stopped"`
}

type projectIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_sessions, e.g. lumen"`
}

type modelIn struct {
	sessionRef
	Model string `json:"model" jsonschema:"Model to switch to, e.g. opus or sonnet"`
}

type runtimeIn struct {
	sessionRef
	Runtime string `json:"runtime" jsonschema:"claude or hermes"`
}

type permissionIn struct {
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form, e.g. lumen/P1"`
	RequestID string `json:"request_id" jsonschema:"Permission request ID, taken from get_pending_permissions"`
	Scope     string `json:"scope,omitempty" jsonschema:"How long the approval lasts: once, session or always (default once)"`
}

type denyIn struct {
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form, e.g. lumen/P1"`
	RequestID string `json:"request_id" jsonschema:"Permission request ID, taken from get_pending_permissions"`
}

type preflightIn struct {
	Project string `json:"project" jsonschema:"Project name, e.g. lumen"`
	Task    string `json:"task" jsonschema:"Task description to analyse, e.g. Add retry to the HTTP client"`
}

type planIn struct {
	PlanID string `json:"plan_id" jsonschema:"Plan ID returned by run_preflight, e.g. 7"`
}

type briefIn struct {
	ID           string `json:"id" jsonschema:"Stable brief ID, e.g. the subtask name: parse-header"`
	Task         string `json:"task" jsonschema:"Self-contained task specification sent to the worker model"`
	SystemPrompt string `json:"system_prompt,omitempty" jsonschema:"Optional replacement for the default FIND/REPLACE patch protocol prompt"`
}

type dispatchIn struct {
	Project string `json:"project" jsonschema:"Project name, e.g. lumen"`
	BriefID string `json:"brief_id" jsonschema:"ID of a brief registered with register_mixed_brief"`
	Worker  string `json:"worker" jsonschema:"Worker name as configured in the project, e.g. step-flash"`
}

type projectPageIn struct {
	Project string `json:"project" jsonschema:"Project name, e.g. lumen"`
	pageIn
}

type workerWaitIn struct {
	TaskID    string `json:"task_id" jsonschema:"Task ID in project/brief/worker form, as returned by dispatch_mixed_task, e.g. lumen/parse-header/step-flash"`
	Status    string `json:"status,omitempty" jsonschema:"Terminal status to wait for: done or needs_human; omit to accept either"`
	TimeoutMs int64  `json:"timeout_ms,omitempty" jsonschema:"How long to wait, in milliseconds (default 60000, at most 600000)"`
}

type logsIn struct {
	sessionRef
	pageIn
}

type noIn struct{}

type waitStatusIn struct {
	sessionRef
	Status    string `json:"status" jsonschema:"Target status to wait for"`
	TimeoutMs int64  `json:"timeout_ms,omitempty" jsonschema:"How long to wait, in milliseconds (default 5000, at most 600000)"`
}

type waitAnyIn struct {
	SessionIDs []string `json:"session_ids" jsonschema:"Sessions to watch, each in project/session form, e.g. [\"lumen/P1\",\"lumen/P2\"]"`
	TimeoutMs  int64    `json:"timeout_ms,omitempty" jsonschema:"How long to wait, in milliseconds (default 60000, at most 600000)"`
}

type waitEventIn struct {
	Event     string         `json:"event" jsonschema:"Event name, e.g. session:status, session:log, session:permission"`
	Match     map[string]any `json:"match,omitempty" jsonschema:"Fields the event payload must contain, all of them, e.g. {\"id\": \"lumen/P1\"}"`
	TimeoutMs int64          `json:"timeout_ms,omitempty" jsonschema:"How long to wait, in milliseconds (default 5000, at most 600000)"`
}

// sessionStatuses are the values of a session's status as the app reports it.
var sessionStatuses = []any{
	"idle", "starting", "analyzing", "working", "waiting_permission",
	"waiting_for_user", "rate_limited", "retrying", "stopping", "error",
}

// permissionScopes maps the scope argument to the decision the app understands.
var permissionScopes = map[string]string{"once": "allow", "session": "allow_session", "always": "allow_always"}

func scopeOrOnce(s string) string {
	if s == "" {
		return "once"
	}
	return s
}

// ── Schema refinements ────────────────────────────────────────────────────────

type refine = func(props map[string]*jsonschema.Schema)

func fptr(f float64) *float64 { return &f }

// tunePage declares the paging defaults and bounds.
func tunePage(props map[string]*jsonschema.Schema) {
	l := props["limit"]
	l.Default, l.Minimum, l.Maximum = json.RawMessage(strconv.Itoa(DefaultPageSize)), fptr(1), fptr(MaxPageSize)
}

func tuneTimeout(def int) refine {
	return func(props map[string]*jsonschema.Schema) {
		t := props["timeout_ms"]
		t.Default, t.Minimum, t.Maximum = json.RawMessage(strconv.Itoa(def)), fptr(1), fptr(float64(MaxWaitTimeout.Milliseconds()))
	}
}

func tuneAll(fns ...refine) refine {
	return func(props map[string]*jsonschema.Schema) {
		for _, f := range fns {
			f(props)
		}
	}
}

// ── Results ───────────────────────────────────────────────────────────────────

// sessionAck is the result of a session lifecycle action.
type sessionAck struct {
	OK        bool   `json:"ok" jsonschema:"True when the action was accepted"`
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
}

type permissionAck struct {
	OK        bool   `json:"ok" jsonschema:"True when the decision was delivered"`
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
	RequestID string `json:"request_id" jsonschema:"The permission request that was answered"`
	Decision  string `json:"decision" jsonschema:"The decision sent: once, session, always or deny"`
}

type projectAck struct {
	OK      bool   `json:"ok" jsonschema:"True when the action was accepted"`
	Project string `json:"project" jsonschema:"The project acted on"`
}

type sessionDetailOut struct {
	Session            any   `json:"session" jsonschema:"Status, model, effort, runtime, current task, task counters, todos, branch, tokens and cost"`
	SavedState         any   `json:"saved_state" jsonschema:"Crash-recovery state used by restart_session resume=true (CLI session id, started_at, task); null when none"`
	PendingPermissions []any `json:"pending_permissions" jsonschema:"Permission requests waiting for approve_permission or deny_permission"`
	PendingQuestion    any   `json:"pending_question" jsonschema:"Question the session asked the human, waiting for an answer; null when none"`
}

type slashOut struct {
	Commands []any `json:"commands" jsonschema:"Slash commands and skills the session offers, each with name, description and kind"`
	pageOut
}

type okOut struct {
	OK bool `json:"ok" jsonschema:"True when the change was applied"`
}

type planOut struct {
	Plan any `json:"plan" jsonschema:"The generated task plan, including its plan_id"`
}

type planAck struct {
	OK     bool   `json:"ok" jsonschema:"True when execution started"`
	PlanID string `json:"plan_id" jsonschema:"The plan that was started"`
}

type briefOut struct {
	BriefID string `json:"brief_id" jsonschema:"ID of the registered brief"`
}

type mixedTaskOut struct {
	Task any `json:"task" jsonschema:"Mixed-programming task state: rounds, patches, gate results, status"`
}

type mixedTasksOut struct {
	Tasks []any `json:"tasks" jsonschema:"Persisted mixed-programming tasks of the project"`
	pageOut
}

type sessionsOut struct {
	Sessions []any `json:"sessions" jsonschema:"Sessions with status and metrics"`
	pageOut
}

type logsOut struct {
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
	Entries   []any  `json:"entries" jsonschema:"Log entries, oldest first, starting at the cursor"`
	pageOut
}

type permissionsOut struct {
	Permissions []any `json:"permissions" jsonschema:"Pending permission requests across all sessions"`
	pageOut
}

type rateLimitOut struct {
	RateLimit any `json:"rate_limit" jsonschema:"Account rate-limit status"`
}

type waitOut struct {
	Reached       bool              `json:"reached" jsonschema:"True when the awaited event arrived; false when the wait timed out (not an error)"`
	Event         string            `json:"event,omitempty" jsonschema:"Name of the event that matched (when reached)"`
	Data          any               `json:"data,omitempty" jsonschema:"Event payload (when reached)"`
	TS            string            `json:"ts,omitempty" jsonschema:"Emission time, RFC 3339 (when reached)"`
	TimeoutMs     int64             `json:"timeout_ms,omitempty" jsonschema:"How long the wait lasted (when not reached)"`
	CurrentStatus string            `json:"current_status,omitempty" jsonschema:"Status of the awaited session right now (when not reached)"`
	Statuses      map[string]string `json:"statuses,omitempty" jsonschema:"Status of every awaited session right now (when not reached)"`
	Hint          string            `json:"hint,omitempty" jsonschema:"What to do next (when not reached)"`
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

// listPage cuts a fully fetched list to the page asked for; the window was
// validated by the tool's Translate.
func listPage(p pageIn, raw any) ([]any, pageOut) {
	offset, limit, _ := p.window()
	page, next, more := pageOf(list(raw), offset, limit)
	return page, pageInfo(0, next, more)
}

// projectTool declares a tool taking a project name and answering with projectAck.
func projectTool(name, title, method string, level AccessLevel, ann ToolAnnotations, doc Doc, verb string) Tool {
	return define(spec[projectIn, projectAck]{
		Name: name, Title: title, Doc: doc, Level: level, Annotations: ann, Covers: []string{method},
		Translate: func(in projectIn) (string, any, error) {
			if in.Project == "" {
				return "", nil, fmt.Errorf(`project is required, e.g. "lumen"; list projects with get_sessions`)
			}
			return "/rpc", rpcCallBody(method, map[string]string{"project": in.Project}), nil
		},
		Result:  func(in projectIn, _ any) projectAck { return projectAck{OK: true, Project: in.Project} },
		Summary: func(in projectIn, _ projectAck) string { return "Project " + in.Project + " " + verb + "." },
	})
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
	if reached, ok := m["reached"].(bool); ok && !reached {
		out.Data = nil
		if t, ok := m["timeout_ms"].(float64); ok {
			out.TimeoutMs = int64(t)
		}
		out.CurrentStatus, _ = m["current_status"].(string)
		if st, ok := m["statuses"].(map[string]any); ok {
			out.Statuses = map[string]string{}
			for id, v := range st {
				out.Statuses[id], _ = v.(string)
			}
		}
		out.Hint = "Not reached yet, and nothing failed: call the same tool again to keep waiting"
		if out.CurrentStatus != "" {
			out.Hint += fmt.Sprintf(" (the session is %s; read get_session_logs to see what it is doing)", out.CurrentStatus)
		}
		out.Hint += "."
		return out
	}
	out.Reached = true
	return out
}

func waitSummary(out waitOut) string {
	if !out.Reached {
		return fmt.Sprintf("Not reached within %d ms. %s", out.TimeoutMs, out.Hint)
	}
	return fmt.Sprintf("Event %s received.", out.Event)
}

// ── Tools ─────────────────────────────────────────────────────────────────────

func buildMCPTools() []Tool {
	return append(append(append(append(append(buildCoreTools(), interactionTools()...), projectsTools()...), logsTools()...), planningTools()...), skillsTools()...)
}

func buildCoreTools() []Tool {
	op := ToolAnnotations{Idempotent: true, OpenWorld: true}
	add := ToolAnnotations{}
	ro := ToolAnnotations{ReadOnly: true}

	return []Tool{
		// ── Actions ──────────────────────────────────────────────────────────
		sessionTool(spec[startIn, sessionAck]{
			Name: "start_session", Title: "Start session",
			Doc: Doc{
				What:    "Start a configured session, optionally with a model, effort or runtime override.",
				UseWhen: "a session is idle, stopped or errored and should begin work.",
				NotFor:  "a session that is already running (use send_message or set_session_model) or continuing an old conversation (use restart_session with resume=true).",
				Returns: "ok and the session_id.",
				Effects: "launches the CLI process, which may spend tokens; model and effort are remembered as the session's defaults and runtime is saved to its config.",
				Example: `{"session_id":"lumen/P1","model":"opus","effort":"high"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				props["runtime"].Enum = []any{"claude", "hermes"}
			},
			Level: LevelOperate, Annotations: op, Covers: []string{"StartSession", "StartSessionWithModel"},
			Translate: func(in startIn) (string, any, error) {
				p, s, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				if in.Model == "" && in.Effort == "" && in.Runtime == "" {
					return "/rpc", rpcCallBody("StartSession", map[string]string{"project": p, "session": s}), nil
				}
				return "/rpc", rpcCallBody("StartSessionWithModel", map[string]string{
					"project": p, "session": s, "model": in.Model, "effort": in.Effort, "runtime": in.Runtime,
				}), nil
			},
		}, func(in startIn) sessionRef { return in.sessionRef }, "started"),
		sessionTool(spec[stopIn, sessionAck]{
			Name: "stop_session", Title: "Stop session",
			Doc: Doc{
				What:    "Stop a running session.",
				UseWhen: "a session must end, now or after it finishes its current task.",
				NotFor:  "aborting only the current step while keeping the session alive (use interrupt_session).",
				Returns: "ok and the session_id; the stop is requested, confirm it with wait_for_status status=idle.",
				Effects: "terminates the CLI process (at once, or after the task when after_task=true); work in the current turn is lost.",
				Example: `{"session_id":"lumen/P1","after_task":true}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"StopSession"},
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
			Doc: Doc{
				What:    "Interrupt the session's current turn (a running tool call or generation) without stopping the session.",
				UseWhen: "the session is going down a wrong path and you want to redirect it with send_message.",
				NotFor:  "ending the session (use stop_session) or answering a permission request (use approve_permission or deny_permission).",
				Returns: "ok and the session_id; the session then waits for the next message.",
				Effects: "cancels the work in flight; partial results of that turn are discarded.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"InterruptSession"},
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
			Doc: Doc{
				What:    "Restart a session, fresh or resuming its saved conversation.",
				UseWhen: "a session is stuck, errored, or needs a clean process; use resume=true to keep the conversation.",
				NotFor:  "a session that was never started (use start_session).",
				Returns: "ok and the session_id.",
				Effects: "stops the current CLI process and launches a new one, which may spend tokens.",
				Example: `{"session_id":"lumen/P1","resume":true}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RestartSession", "ResumeSession"},
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
			Doc: Doc{
				What:    "Send a user message, optionally with images, to a running session.",
				UseWhen: "you want to give a session new instructions or answer its question.",
				NotFor:  "a session that is not running (use start_session first) or permission requests (use approve_permission).",
				Returns: "ok and the session_id; read the reply with get_session_logs or wait_for_status.",
				Effects: "the session processes the message as its next turn, which spends tokens.",
				Example: `{"session_id":"lumen/P1","text":"Continue with the next task"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"SendMessage", "SendMessageWithImages"},
			Translate: func(in sendIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				if len(in.Images) == 0 {
					return "/rpc", rpcCallBody("SendMessage", map[string]string{"id": id, "message": in.Text}), nil
				}
				return "/rpc", rpcCallBody("SendMessageWithImages", map[string]any{"id": id, "message": in.Text, "images": in.Images}), nil
			},
		}, func(in sendIn) sessionRef { return in.sessionRef }, "message sent"),
		sessionTool(spec[modelIn, sessionAck]{
			Name: "set_session_model", Title: "Switch session model",
			Doc: Doc{
				What:    "Switch a session's model, live if it is running.",
				UseWhen: "a task needs a stronger or cheaper model than the session currently uses.",
				NotFor:  "starting a session (use start_session with model) or changing the CLI (use set_session_runtime).",
				Returns: "ok and the session_id.",
				Effects: "a running session is restarted on the new model with its conversation resumed; the choice is remembered as the session's default.",
				Example: `{"session_id":"lumen/P1","model":"sonnet"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"SetSessionModel"},
			Translate: func(in modelIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				if in.Model == "" {
					return "", nil, fmt.Errorf(`model is required, e.g. "opus" or "sonnet"`)
				}
				return "/rpc", rpcCallBody("SetSessionModel", map[string]string{"id": id, "model": in.Model}), nil
			},
		}, func(in modelIn) sessionRef { return in.sessionRef }, "model switched"),
		sessionTool(spec[runtimeIn, sessionAck]{
			Name: "set_session_runtime", Title: "Set session runtime",
			Doc: Doc{
				What:    "Choose which agent CLI (claude or hermes) a session drives.",
				UseWhen: "a stopped session should run on another CLI the next time it starts.",
				NotFor:  "a running session (stop_session first) or changing the model (use set_session_model).",
				Returns: "ok and the session_id.",
				Effects: "rewrites the session's runtime in the saved configuration; refused while the session runs.",
				Example: `{"session_id":"lumen/P1","runtime":"hermes"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				props["runtime"].Enum = []any{"claude", "hermes"}
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"SetSessionRuntime"},
			Translate: func(in runtimeIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				if in.Runtime == "" {
					return "", nil, fmt.Errorf("runtime is required: claude or hermes")
				}
				return "/rpc", rpcCallBody("SetSessionRuntime", map[string]string{"id": id, "runtime": in.Runtime}), nil
			},
		}, func(in runtimeIn) sessionRef { return in.sessionRef }, "runtime set"),
		sessionTool(spec[sessionIn, sessionAck]{
			Name: "clear_session_state", Title: "Clear saved session state",
			Doc: Doc{
				What:    "Delete a session's saved crash-recovery state so its next start opens a fresh conversation.",
				UseWhen: "a stopped session keeps resuming a conversation that went wrong.",
				NotFor:  "a running session (stop_session first) or restarting one (use restart_session).",
				Returns: "ok and the session_id.",
				Effects: "the saved CLI conversation id is forgotten; the old conversation can no longer be resumed.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Destructive: true, Idempotent: true}, Covers: []string{"ClearSessionState"},
			Translate: func(in sessionIn) (string, any, error) {
				p, s, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("ClearSessionState", map[string]string{"project": p, "session": s}), nil
			},
		}, func(in sessionIn) sessionRef { return in.sessionRef }, "state cleared"),
		projectTool("start_project", "Start project sessions", "StartProject", LevelOperate, op,
			Doc{
				What:    "Start every configured session of a project.",
				UseWhen: "a whole project should begin work at once.",
				NotFor:  "starting one session (use start_session).",
				Returns: "ok and the project.",
				Effects: "launches one CLI process per session, which may spend tokens.",
				Example: `{"project":"lumen"}`,
			}, "started"),
		projectTool("stop_project", "Stop project sessions", "StopProject", LevelAdmin,
			ToolAnnotations{Destructive: true, Idempotent: true},
			Doc{
				What:    "Stop every running session of a project.",
				UseWhen: "a whole project must halt at once.",
				NotFor:  "stopping one session (use stop_session) or every project (use stop_all_sessions).",
				Returns: "ok and the project.",
				Effects: "terminates all CLI processes of the project; work in their current turns is lost.",
				Example: `{"project":"lumen"}`,
			}, "stopped"),
		define(spec[confirmIn, okOut]{
			Name: "stop_all_sessions", Title: "Stop all sessions",
			Doc: Doc{
				What:    "Stop every running session of every project.",
				UseWhen: "everything must halt, e.g. a runaway cost or a rate limit.",
				NotFor:  "stopping one session or one project (use stop_session or stop_project).",
				Returns: "ok.",
				Effects: "terminates all CLI processes; work in their current turns is lost.",
				Example: `{"confirm":true}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Destructive: true, Idempotent: true}, Covers: []string{"StopAll"},
			Translate: func(in confirmIn) (string, any, error) {
				if !in.Confirm {
					return "", nil, fmt.Errorf("stop_all_sessions stops work in every project: call it again with confirm=true if that is intended, or use stop_project / stop_session for a narrower stop")
				}
				return "/rpc", rpcCallBody("StopAll", nil), nil
			},
			Result:  func(confirmIn, any) okOut { return okOut{OK: true} },
			Summary: func(confirmIn, okOut) string { return "All sessions stopped." },
		}),
		projectTool("start_ad_hoc_chat", "Start ad-hoc chat session", "StartAdHocChatSession", LevelOperate, op,
			Doc{
				What:    "Open an unconfigured Chat session in a project, with no task source and no first prompt.",
				UseWhen: "you want a free-form session to instruct with send_message.",
				NotFor:  "running a configured session (use start_session).",
				Returns: "ok and the project; the session_id is project/Chat.",
				Effects: "adds or reuses a Chat session in the saved configuration and launches it, idle until the first message.",
				Example: `{"project":"lumen"}`,
			}, "chat session started"),
		define(spec[permissionIn, permissionAck]{
			Name: "approve_permission", Title: "Approve permission",
			Doc: Doc{
				What:    "Approve a pending permission request on the human's behalf.",
				UseWhen: "get_pending_permissions lists a request you are authorised to allow.",
				NotFor:  "refusing a request (use deny_permission).",
				Returns: "ok, the ids and the decision sent.",
				Effects: "the session runs the tool it asked for; scope session or always also adds a standing allow rule.",
				Example: `{"session_id":"lumen/P1","request_id":"req-1","scope":"once"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				s := props["scope"]
				s.Enum, s.Default = []any{"once", "session", "always"}, json.RawMessage(`"once"`)
			},
			Level: LevelAdmin, Annotations: add, Covers: []string{"RespondPermission"},
			Translate: func(in permissionIn) (string, any, error) {
				decision, ok := permissionScopes[scopeOrOnce(in.Scope)]
				if !ok {
					return "", nil, fmt.Errorf("scope %q is not valid: use once, session or always (default once)", in.Scope)
				}
				return "/rpc", rpcCallBody("RespondPermission", map[string]string{
					"id": in.SessionID, "request_id": in.RequestID, "decision": decision,
				}), nil
			},
			Result: func(in permissionIn, _ any) permissionAck {
				return permissionAck{OK: true, SessionID: in.SessionID, RequestID: in.RequestID, Decision: scopeOrOnce(in.Scope)}
			},
			Summary: func(_ permissionIn, out permissionAck) string {
				return fmt.Sprintf("Permission %s approved (%s) for %s.", out.RequestID, out.Decision, out.SessionID)
			},
		}),
		define(spec[denyIn, permissionAck]{
			Name: "deny_permission", Title: "Deny permission",
			Doc: Doc{
				What:    "Deny a pending permission request.",
				UseWhen: "get_pending_permissions lists a request that must not run.",
				NotFor:  "approving a request (use approve_permission).",
				Returns: "ok, the ids and decision deny.",
				Effects: "the session is told the tool was refused and continues without it.",
				Example: `{"session_id":"lumen/P1","request_id":"req-1"}`,
			},
			Level: LevelOperate, Annotations: add, Covers: []string{"RespondPermission"},
			Translate: func(in denyIn) (string, any, error) {
				return "/rpc", rpcCallBody("RespondPermission", map[string]string{
					"id": in.SessionID, "request_id": in.RequestID, "decision": "deny",
				}), nil
			},
			Result: func(in denyIn, _ any) permissionAck {
				return permissionAck{OK: true, SessionID: in.SessionID, RequestID: in.RequestID, Decision: "deny"}
			},
			Summary: func(_ denyIn, out permissionAck) string {
				return fmt.Sprintf("Permission %s denied for %s.", out.RequestID, out.SessionID)
			},
		}),
		define(spec[preflightIn, planOut]{
			Name: "run_preflight", Title: "Run preflight analysis",
			Doc: Doc{
				What:    "Analyse a task in a project and produce an execution plan.",
				UseWhen: "you want a plan before any code is touched.",
				NotFor:  "running the plan (use execute_plan) or ordinary work (use send_message).",
				Returns: "the plan, including its plan_id.",
				Effects: "runs an analysis session that spends tokens; the project is not changed.",
				Example: `{"project":"lumen","task":"Add retry to the HTTP client"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RunPreflight"},
			Translate: func(in preflightIn) (string, any, error) {
				return "/rpc", rpcCallBody("RunPreflight", map[string]string{"project": in.Project, "task": in.Task}), nil
			},
			Result:  func(_ preflightIn, raw any) planOut { return planOut{Plan: raw} },
			Summary: func(in preflightIn, _ planOut) string { return fmt.Sprintf("Plan generated for %s.", in.Project) },
		}),
		define(spec[planIn, planAck]{
			Name: "execute_plan", Title: "Execute plan",
			Doc: Doc{
				What:    "Start executing a plan produced by run_preflight.",
				UseWhen: "the plan from run_preflight looks right.",
				NotFor:  "creating a plan (use run_preflight).",
				Returns: "ok and the plan_id.",
				Effects: "starts sessions that edit the project and spend tokens.",
				Example: `{"plan_id":"7"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"ExecutePlan"},
			Translate: func(in planIn) (string, any, error) {
				return "/rpc", rpcCallBody("ExecutePlan", map[string]string{"plan_id": in.PlanID}), nil
			},
			Result:  func(in planIn, _ any) planAck { return planAck{OK: true, PlanID: in.PlanID} },
			Summary: func(in planIn, _ planAck) string { return fmt.Sprintf("Plan %s started.", in.PlanID) },
		}),
		// ── Mixed programming (MIXED-TASKS.md MP-07) ─────────────────────────
		define(spec[briefIn, briefOut]{
			Name: "register_mixed_brief", Title: "Register mixed brief",
			Doc: Doc{
				What:    "Register a self-contained brief (English task text, verbatim code excerpts, decisions, target files) for a worker model.",
				UseWhen: "you delegate a coding subtask to a cheaper worker model.",
				NotFor:  "running it (use dispatch_mixed_task).",
				Returns: "the brief_id.",
				Effects: "stores the brief in memory; registering the same id again replaces it.",
				Example: `{"id":"parse-header","task":"Implement parseHeader in internal/x/header.go"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"RegisterMixedBrief"},
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
			Doc: Doc{
				What:    "Run the brief, patches, checks, feedback loop for a registered brief against a worker model.",
				UseWhen: "a brief is registered and a worker is configured.",
				NotFor:  "watching progress from another call (use wait_for_worker_status or get_mixed_rounds).",
				Returns: "the task state: rounds, patches, check results and final status; blocks until done or needs_human.",
				Effects: "calls the worker model (cost) and applies its patches to the project.",
				Example: `{"project":"lumen","brief_id":"parse-header","worker":"step-flash"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"DispatchMixedTask"},
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
		define(spec[projectPageIn, mixedTasksOut]{
			Name: "get_mixed_rounds", Title: "Get mixed-programming rounds",
			Doc: Doc{
				What:    "List the saved mixed-programming tasks of a project with rounds, patches, check results and status.",
				UseWhen: "you review or resume delegated work.",
				NotFor:  "waiting for a task to finish (use wait_for_worker_status).",
				Returns: "tasks, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","limit":20}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetMixedRounds"},
			Translate: func(in projectPageIn) (string, any, error) {
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetMixedRounds", map[string]string{"project": in.Project}), nil
			},
			Result: func(in projectPageIn, raw any) mixedTasksOut {
				page, p := listPage(in.pageIn, raw)
				return mixedTasksOut{Tasks: page, pageOut: p}
			},
			Summary: func(in projectPageIn, out mixedTasksOut) string {
				return fmt.Sprintf("%s in %s.", plural(len(out.Tasks), "mixed task", "mixed tasks"), in.Project) + moreHint(out.pageOut)
			},
		}),
		define(spec[workerWaitIn, waitOut]{
			Name: "wait_for_worker_status", Title: "Wait for worker status",
			Doc: Doc{
				What:    "Block until a mixed-programming task reaches a terminal status, or the timeout passes.",
				UseWhen: "dispatch_mixed_task runs in another call, or has returned and you want its final event.",
				NotFor:  "session status (use wait_for_status).",
				Returns: "the worker:done event with its payload; past events count, so a late call still succeeds.",
				Effects: "none, read only; a timeout is reported as reached=false, not an error.",
				Example: `{"task_id":"lumen/parse-header/step-flash","status":"done"}`,
			},
			Input: tuneAll(tuneTimeout(60000), func(props map[string]*jsonschema.Schema) {
				props["status"].Enum = []any{"done", "needs_human"}
			}),
			Level: LevelRead, Annotations: ro, Covers: nil,
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
		define(spec[pageIn, sessionsOut]{
			Name: "get_sessions", Title: "List sessions",
			Doc: Doc{
				What:    "List all sessions with their status and metrics.",
				UseWhen: "you need a session_id, or want to see what is running, idle or failed.",
				NotFor:  "reading what a session said (use get_session_logs).",
				Returns: "sessions, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"limit":50}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetAllSessions"},
			Translate: func(in pageIn) (string, any, error) {
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetAllSessions", map[string]any{}), nil
			},
			Result: func(in pageIn, raw any) sessionsOut {
				page, p := listPage(in, raw)
				return sessionsOut{Sessions: page, pageOut: p}
			},
			Summary: func(_ pageIn, out sessionsOut) string {
				return plural(len(out.Sessions), "session", "sessions") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[sessionIn, sessionDetailOut]{
			Name: "get_session", Title: "Get session",
			Doc: Doc{
				What:    "Get one session's full state: status, model, runtime, current task, pending questions and permissions, saved conversation state.",
				UseWhen: "you decide what to do next with a session.",
				NotFor:  "listing sessions (use get_sessions) or reading its output (use get_session_logs).",
				Returns: "session, saved_state, pending_permissions and pending_question; an error status is explained in get_session_logs.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSessionState"},
			Translate: func(in sessionIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSessionOverview", map[string]string{"id": id}), nil
			},
			Result: func(_ sessionIn, raw any) sessionDetailOut {
				m, _ := raw.(map[string]any)
				return sessionDetailOut{
					Session: m["session"], SavedState: m["saved_state"],
					PendingPermissions: list(m["pending_permissions"]), PendingQuestion: m["pending_question"],
				}
			},
			Summary: func(_ sessionIn, out sessionDetailOut) string {
				st, _ := out.Session.(map[string]any)
				return fmt.Sprintf("Session %v is %v; %s pending.", st["id"], st["status"],
					plural(len(out.PendingPermissions), "permission", "permissions"))
			},
		}),
		define(spec[sessionSlashIn, slashOut]{
			Name: "list_slash_commands", Title: "List slash commands",
			Doc: Doc{
				What:    "List the slash commands and skills a session offers after a leading /.",
				UseWhen: "you want to invoke a skill or command through send_message.",
				NotFor:  "managing this app's skills (use the skills tools).",
				Returns: "commands, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"ListSlashCommands"},
			Translate: func(in sessionSlashIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				if _, _, err := in.pageIn.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("ListSlashCommands", map[string]string{"id": id}), nil
			},
			Result: func(in sessionSlashIn, raw any) slashOut {
				page, p := listPage(in.pageIn, raw)
				return slashOut{Commands: page, pageOut: p}
			},
			Summary: func(_ sessionSlashIn, out slashOut) string {
				return plural(len(out.Commands), "command", "commands") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[logsIn, logsOut]{
			Name: "get_session_logs", Title: "Get session log",
			Doc: Doc{
				What:    "Read a session's log of its latest run, oldest entry first, one page at a time.",
				UseWhen: "you want to know what a session did or said.",
				NotFor:  "live updates (use wait_for_event) or listing sessions (use get_sessions).",
				Returns: "entries, with next_cursor and truncated when the log is longer than the page.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1","limit":50}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSessionLog"},
			Translate: func(in logsIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				offset, limit, err := in.window()
				if err != nil {
					return "", nil, err
				}
				// One extra entry tells whether a further page exists.
				return "/rpc", rpcCallBody("GetSessionLog", map[string]any{"id": id, "offset": offset, "limit": limit + 1}), nil
			},
			Result: func(in logsIn, raw any) logsOut {
				id, _ := in.id()
				offset, limit, _ := in.window()
				page, next, more := pageOf(list(raw), 0, limit)
				return logsOut{SessionID: id, Entries: page, pageOut: pageInfo(offset, next, more)}
			},
			Summary: func(_ logsIn, out logsOut) string {
				return fmt.Sprintf("%s from %s.", plural(len(out.Entries), "log entry", "log entries"), out.SessionID) + moreHint(out.pageOut)
			},
		}),
		define(spec[pageIn, permissionsOut]{
			Name: "get_pending_permissions", Title: "List pending permissions",
			Doc: Doc{
				What:    "List permission requests that wait for a decision, across all sessions.",
				UseWhen: "a session shows status waiting_permission, or before approving or denying.",
				NotFor:  "answering them (use approve_permission or deny_permission).",
				Returns: "permissions, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetPendingPermissions"},
			Translate: func(in pageIn) (string, any, error) {
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetPendingPermissions", map[string]any{}), nil
			},
			Result: func(in pageIn, raw any) permissionsOut {
				page, p := listPage(in, raw)
				return permissionsOut{Permissions: page, pageOut: p}
			},
			Summary: func(_ pageIn, out permissionsOut) string {
				return plural(len(out.Permissions), "pending permission", "pending permissions") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[noIn, rateLimitOut]{
			Name: "get_rate_limit", Title: "Get rate-limit status",
			Doc: Doc{
				What:    "Get the account rate-limit status.",
				UseWhen: "sessions report rate_limited, or before starting a large batch of work.",
				NotFor:  "spending (use get_cost).",
				Returns: "rate_limit, the account status as the app reports it.",
				Effects: "none, read only.",
				Example: `{}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetRateLimitStatus"},
			Translate: func(noIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetRateLimitStatus", map[string]any{}), nil
			},
			Result:  func(_ noIn, raw any) rateLimitOut { return rateLimitOut{RateLimit: raw} },
			Summary: func(noIn, rateLimitOut) string { return "Rate-limit status." },
		}),
		// ── Blocking primitives ───────────────────────────────────────────────
		define(spec[waitStatusIn, waitOut]{
			Name: "wait_for_status", Title: "Wait for session status",
			Doc: Doc{
				What:    "Block until a session reports the given status, or the timeout passes.",
				UseWhen: "after start_session, send_message or approve_permission, instead of polling get_sessions.",
				NotFor:  "events other than status changes (use wait_for_event).",
				Returns: "the matching session:status event; on timeout reached=false with the current status and a hint, not an error.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1","status":"idle","timeout_ms":30000}`,
			},
			Input: tuneAll(tuneTimeout(5000), func(props map[string]*jsonschema.Schema) {
				props["status"].Enum = sessionStatuses
			}),
			Level: LevelRead, Annotations: ro,
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
			Doc: Doc{
				What:    "Block until an event whose payload contains the given fields is emitted, or the timeout passes.",
				UseWhen: "you wait for something other than a status, such as session:log or session:permission.",
				NotFor:  "session status changes (use wait_for_status).",
				Returns: "the matching event; recent past events count, so a short gap does not lose it; on timeout reached=false with a hint, not an error.",
				Effects: "none, read only.",
				Example: `{"event":"session:permission","match":{"id":"lumen/P1"}}`,
			},
			Input: tuneAll(tuneTimeout(5000), func(props map[string]*jsonschema.Schema) {
				for _, e := range EventNames {
					props["event"].Enum = append(props["event"].Enum, e)
				}
			}),
			Level: LevelRead, Annotations: ro,
			Translate: func(in waitEventIn) (string, any, error) {
				return "/wait", waitCallBody(in.Event, in.Match, orDefault(in.TimeoutMs, 5000)), nil
			},
			Result:  func(_ waitEventIn, raw any) waitOut { return waitResult(noIn{}, raw) },
			Summary: func(_ waitEventIn, out waitOut) string { return waitSummary(out) },
		}),
		define(spec[waitAnyIn, waitOut]{
			Name: "wait_for_any", Title: "Wait for any session to need a human",
			Doc: Doc{
				What:    "Block until the first of several sessions needs a human (question, permission, plan, error, finished task or turn), or the timeout passes.",
				UseWhen: "you operate several sessions and want one loop without polling: wait, handle what came, wait again.",
				NotFor:  "one specific status or event (use wait_for_status or wait_for_event).",
				Returns: "the first attention event with its session id in data.id; something already pending counts at once; on timeout reached=false with every session's status, not an error.",
				Effects: "none, read only.",
				Example: `{"session_ids":["lumen/P1","lumen/P2"],"timeout_ms":60000}`,
			},
			Input: tuneTimeout(60000),
			Level: LevelRead, Annotations: ro,
			Translate: func(in waitAnyIn) (string, any, error) {
				if len(in.SessionIDs) == 0 {
					return "", nil, fmt.Errorf(`session_ids is required: the sessions to watch, e.g. ["lumen/P1","lumen/P2"]; list them with get_sessions`)
				}
				for _, id := range in.SessionIDs {
					if p, n, ok := strings.Cut(id, "/"); !ok || p == "" || n == "" {
						return "", nil, fmt.Errorf(`session id %q must look like "project/session", e.g. "lumen/P1"; list sessions with get_sessions`, id)
					}
				}
				return "/wait", map[string]any{"attention": in.SessionIDs, "timeout_ms": orDefault(in.TimeoutMs, 60000)}, nil
			},
			Result:  func(_ waitAnyIn, raw any) waitOut { return waitResult(noIn{}, raw) },
			Summary: func(_ waitAnyIn, out waitOut) string { return waitSummary(out) },
		}),
	}
}
