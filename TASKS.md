# Tasks — decomposed for single Sonnet sessions

Each task is scoped to fit within one Claude Code Sonnet session (~15-30 turns, ~5-10 files).
Tasks are ordered by dependency. A task can only start after all its prerequisites are done.

---

## TASK-01: Project scaffold + config system

**Depends on:** nothing
**Files to create:**
- `main.go` — Wails bootstrap
- `app.go` — App struct with OnStartup/OnShutdown stubs
- `go.mod`, `go.sum` — module init with wails/toml/sqlite deps
- `internal/config/types.go` — all config structs: AppConfig, GlobalSettings, ProjectConfig, SessionConfig, PermissionRule, OptimizationSettings
- `internal/config/config.go` — Load(path) / Save(path), TOML parsing, defaults, validation
- `config.example.toml` — working example with all fields documented
- Stub directories: `internal/session/`, `internal/permission/`, `internal/analysis/`, `internal/optimization/`, `internal/store/`, `internal/hooks/`

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 2-5. Initialize a Wails v2 project with svelte-ts template. Create the full config system: all Go structs from PLAN.md section 5 (AppConfig, GlobalSettings, ProjectConfig, SessionConfig, PermissionRule) and TOML loader with defaults. Create config.example.toml with all fields. Set up app.go with Wails lifecycle stubs. Create empty directories for all internal packages. Verify it compiles with `wails build`.
```

---

## TASK-02: SQLite store + migrations

**Depends on:** TASK-01
**Files to create:**
- `internal/store/migrations.go` — all CREATE TABLE/INDEX statements
- `internal/store/store.go` — Store struct, Init, Close, all CRUD methods

**Tables:** session_runs (with cost/tokens), session_logs, daily_metrics, task_plans, plan_subtasks.
**Methods:** SaveRun, SaveLogs (batch), GetHistory, GetSessionLogs, GetDailyCost, GetProjectCost, SearchLogs, SavePlan, GetPlan, UpdateSubtask, CleanOldLogs.

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 5 (SQLite schema) and 18.4 (daily_metrics, extended session_runs). Create internal/store/ package with migrations.go (all CREATE TABLE statements) and store.go (Store struct with Init/Close and all CRUD methods). Use database/sql with mattn/go-sqlite3. Batch insert for logs. Verify it compiles.
```

---

## TASK-03: Stream-JSON parser

**Depends on:** TASK-01
**Files to create:**
- `internal/session/parser.go` — parse all stream-json event types
- `internal/session/parser_test.go` — unit tests with real event samples

**Must parse:** system/init, assistant (text + tool_use + thinking), result (with total_cost_usd, usage, modelUsage), rate_limit_event (with utilization, resetsAt), permission_request. Extract: LogEntry, TokenUsage, SessionResult, RateLimitInfo, PermissionRequest, InitInfo.

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 6.3, 18.1-18.2. Create internal/session/parser.go that parses Claude CLI stream-json output. Must handle all event types: system/init, assistant (with text, tool_use, thinking blocks), result (with total_cost_usd, usage, modelUsage), rate_limit_event. Extract structured types: LogEntry, TokenUsage, SessionResult, RateLimitInfo, InitInfo. Use real stream-json samples from PLAN.md section 18.1 as test data in parser_test.go. Use encoding/json, no external deps.
```

---

## TASK-04: Session core — process management + bidirectional streaming

**Depends on:** TASK-01, TASK-03
**Files to create:**
- `internal/session/session.go` — Session struct, Run() goroutine loop, buildCLIArgs(), lifecycle
- `internal/session/input.go` — inputWriter goroutine, SendMessage(), stdin protocol
- `internal/session/ratelimit.go` — rate limit detection from parsed events, retry timers

**This is the core engine.** Session.Run() does: build CLI args from SessionConfig → exec.Command with stdin/stdout pipes → send initial prompt via stdin → spawn inputWriter goroutine → scanner loop parsing stdout → dispatch events (log, permission, rate_limit) → handle exit code → auto-restart logic.

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 6.2, 14 (CLI flags), 15 (bidirectional streaming). Create session.go with Session struct and Run() goroutine. Build CLI args from SessionConfig (all flags from section 14: model, effort, permission-mode, allowed-tools, worktree, max-budget-usd, etc). Use bidirectional streaming: stdin pipe for input, stdout pipe for parsing. Create input.go with inputWriter goroutine and SendMessage(). Create ratelimit.go for rate limit detection and retry logic. Import parser.go types. Session must emit events via a callback (not Wails directly — that's the manager's job). Handle all SessionStatus transitions.
```

---

## TASK-05: Permission system

**Depends on:** TASK-01, TASK-03
**Files to create:**
- `internal/permission/rules.go` — PermissionRule matching, glob patterns, RuntimeRuleSet
- `internal/permission/handler.go` — PermissionHandler: check rules → auto-approve or queue
- `internal/permission/queue.go` — PendingQueue: thread-safe queue of waiting permissions

**Prompt:**
```
Read CLAUDE.md and PLAN.md section 16 (Permission handling). Create the permission package. rules.go: match PermissionRequest against PermissionRule list using glob patterns (filepath.Match). Support tool+pattern matching (e.g., Bash+"cargo *"). RuntimeRuleSet for session-scoped rules ("allow for session"). handler.go: PermissionHandler that takes a PermissionRequest, checks config rules then runtime rules, returns decision or blocks on channel waiting for UI response. queue.go: thread-safe PendingQueue with Add/Remove/GetAll. All types from PLAN.md section 16.3.
```

---

## TASK-06: Session Manager + Wails bindings

**Depends on:** TASK-02, TASK-04, TASK-05
**Files to create/modify:**
- `internal/session/manager.go` — SessionManager: owns all sessions, config, store, wails runtime
- `app.go` — wire SessionManager, export all methods as Wails bindings

**SessionManager methods:** StartSession, StopSession, StopAll, RestartSession, ResumeSession, SendMessage, RespondPermission, GetPendingPermissions, GetAllSessions, GetSessionLog, GetHistory, GetSessionMetrics, GetDailyCost, GetProjectCost, GetRateLimitStatus, StartProject, StopProject. Uses runtime.EventsEmit for all session events.

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 6.1 (SessionManager), 8 (Wails bindings). Create manager.go with SessionManager struct that owns sessions map, config, store, wails runtime. Implement all public methods from section 6.1. Wire sessions to store (save runs on completion). Use runtime.EventsEmit for events: session:status, session:log, session:task_done, session:rate_limit, session:permission, session:context. Update app.go to create SessionManager in OnStartup, expose all methods as Wails bindings. Implement cache warming delay (stagger session starts by session_start_delay seconds).
```

---

## TASK-07: Frontend — layout, stores, sidebar, status bar

**Depends on:** TASK-06
**Files to create:**
- `frontend/src/App.svelte` — root layout: sidebar (250px) + main panel (flex) + status bar
- `frontend/src/stores/sessions.ts` — Svelte store, subscribe to Wails events
- `frontend/src/stores/projects.ts` — projects store from GetAllSessions
- `frontend/src/components/Sidebar.svelte` — project tree, status dots, start/stop all buttons
- `frontend/src/components/StatusBar.svelte` — active/waiting/rate-limited/errors/cost/uptime

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 7.1, 7.2, 7.6, 8. Create the frontend foundation. App.svelte: three-panel layout (sidebar 250px, main flex, status bar). sessions.ts: Svelte writable store, subscribe to Wails events (session:status, session:log, session:task_done, session:rate_limit, session:permission) via EventsOn. projects.ts: derived store grouping sessions by project. Sidebar.svelte: collapsible project tree, colored status dots (green=working, orange=waiting permission, yellow=rate limited, red=error, gray=idle, blue=starting), start/stop all per project. StatusBar.svelte: active count, waiting count (clickable), rate limited, errors, cost today, uptime. Use Tailwind CSS. Dark theme default.
```

---

## TASK-08: Frontend — session log view, controls, input

**Depends on:** TASK-07
**Files to create:**
- `frontend/src/components/LogStream.svelte` — real-time log with color coding, autoscroll
- `frontend/src/components/SessionCard.svelte` — session header with metrics, context usage bar
- `frontend/src/components/SessionInput.svelte` — message input field for bidirectional streaming
- `frontend/src/lib/formatters.ts` — log formatting, time, cost, tokens

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 7.3, 15.6, 18.5, 20.6. Create the session view components. LogStream.svelte: scrollable log with autoscroll (freeze on scroll up), color-coded entries (gray=text, blue=read/grep/glob, green=bash, orange=edit/write, red=error, purple=agent). Each line: [HH:MM:SS] icon message. Optional per-turn cost line. SessionCard.svelte: session header showing name, project, status, time, branch, task, turns, tokens in/out, cost, cache hit%, context usage bar (green<60%, yellow<80%, red>80%). SessionInput.svelte: text input + Send button, disabled when session not active, calls SendMessage binding. formatters.ts: formatTime, formatCost, formatTokens, formatDuration. Use Tailwind CSS. Control buttons: Pause, Stop, Stop after task, Restart, Copy log, Clear log — call Wails bindings.
```

---

## TASK-09: Frontend — permission UI

**Depends on:** TASK-07
**Files to create:**
- `frontend/src/components/PermissionBanner.svelte` — overlay banner on session log
- `frontend/src/components/PermissionQueue.svelte` — global permission queue view

**Prompt:**
```
Read PLAN.md section 16.8-16.11. Create PermissionBanner.svelte: overlay on top of log when session has pending permission. Shows tool, command/file, risk level (color), waiting time. Buttons: Allow, Deny, Allow similar, Always allow, Always deny. Calls RespondPermission Wails binding. Flashing animation after 30s. PermissionQueue.svelte: list all pending permissions across all sessions. Each entry shows session name, tool, command, waiting time, approve/deny buttons. Quick actions: Allow all safe, Deny all. Shown when clicking "Waiting: N" in StatusBar. Use Tailwind CSS.
```

---

## TASK-10: Frontend — settings modals

**Depends on:** TASK-07
**Files to create:**
- `frontend/src/components/Settings.svelte` — tabbed modal: Global / Project / Session settings

**Prompt:**
```
Read PLAN.md sections 4, 7.5, 16.5, 17.6, 20.4. Create Settings.svelte as a tabbed modal dialog. Global tab: claude_path, theme, retry_delay, rate_limit_pause, log_retention, preflight settings, permission timeout settings, budget alerts, optimization settings. Project tab: add/edit/remove projects (name, path with folder picker via Wails dialog). Session tab: all SessionConfig fields — name, prompt (multiline textarea), model/effort/fallback, permission_mode, allowed/disallowed tools, max_budget_usd, use_worktree, auto_restart, max_tasks, hooks, preflight mode, permission rules list (add/remove). Save calls UpdateConfig Wails binding which writes TOML. Use Tailwind CSS.
```

---

## TASK-11: Frontend — history view + cost dashboard

**Depends on:** TASK-07
**Files to create:**
- `frontend/src/components/History.svelte` — past runs table
- `frontend/src/components/CostDashboard.svelte` — cost/token metrics view

**Prompt:**
```
Read PLAN.md sections 7.4, 18.5, 18.6. Create History.svelte: table of past session runs (session, started, duration, turns, tasks, cost, status). Sortable columns. Click row to expand and show log entries. Filter by project, session, date range, status. Calls GetHistory and GetSessionLogs Wails bindings. Create CostDashboard.svelte: total cost (period selector: today/week/month), cost breakdown by model (bar chart), by project, cache efficiency percentage, avg cost per task, rate limit utilization, daily breakdown. Calls GetDailyCost, GetProjectCost, GetRateLimitStatus bindings. Use Tailwind CSS.
```

---

## TASK-12: Token optimization engine

**Depends on:** TASK-04, TASK-06
**Files to create:**
- `internal/optimization/context.go` — context utilization monitor, auto-restart at threshold
- `internal/optimization/cache.go` — cache efficiency tracking, warming delay
- `internal/optimization/loop.go` — loop detection (repeated tool calls)

**Prompt:**
```
Read PLAN.md section 20 (full). Create the optimization package. context.go: ContextMonitor that receives TokenUsage per turn, calculates utilization (totalTokens/contextWindow), emits warnings at warn_threshold, triggers auto-restart at restart_threshold (resume or fresh based on config). cache.go: CacheTracker that calculates cache efficiency (reads/total), tracks per-session and global stats, implements StartProjectOptimized with stagger delay. loop.go: LoopDetector with ring buffer of recent tool calls, detects N repeated identical tool+input combos, returns LoopDetected with suggestion (send_hint/restart/warn). All components receive config from OptimizationSettings.
```

---

## TASK-13: Pre-flight analysis backend

**Depends on:** TASK-04, TASK-06
**Files to create:**
- `internal/analysis/schema.go` — JSON Schema string for analyst output
- `internal/analysis/preflight.go` — run analyst session, parse result
- `internal/analysis/plan.go` — TaskPlan management, execution order, context passing

**Prompt:**
```
Read PLAN.md section 17 (full). Create analysis package. schema.go: AnalysisJSONSchema constant string with the full schema from section 17.5. preflight.go: RunAnalysis(projectPath, task, config) that launches claude -p with --model haiku, --permission-mode plan, --json-schema, --output-format json, optional --max-budget-usd. Parses JSON result into AnalysisResult struct. plan.go: TaskPlan and PlannedSubtask structs from section 17.9. ExecutePlan() that launches subtasks respecting execution_order (parallel within group, sequential between groups). Passes context between sequential subtasks via --append-system-prompt (summary of previous subtask results + files changed). Saves plans to store.
```

---

## TASK-14: Pre-flight analysis UI

**Depends on:** TASK-07, TASK-13
**Files to create:**
- `frontend/src/components/PlanReview.svelte` — plan review/edit screen

**Prompt:**
```
Read PLAN.md section 17.7. Create PlanReview.svelte: shown after RunAnalysis completes. Displays: feasibility assessment (single_session true/false, confidence, complexity bar, estimated files/tokens), risks list, recommended approach. If multi-session: visual pipeline of subtasks as cards with arrows showing dependencies. Each card shows: id, name, model, effort, estimated tokens, files count. Edit button on each card to modify prompt/model/effort. Add Step / Remove Step buttons. Reorder via drag or buttons. Bottom: estimated total cost + time, Cancel / Edit Plan / Execute Plan buttons. Calls ApprovePlan and ExecutePlan Wails bindings. Use Tailwind CSS.
```

---

## TASK-15: Hooks + notifications + polish

**Depends on:** TASK-06, TASK-08
**Files to create:**
- `internal/hooks/hooks.go` — pre/post task hook execution
- Modify: session.go to call hooks in the Run loop

**Also implement:**
- Dark/light theme toggle (Tailwind dark mode)
- Keyboard shortcuts (Ctrl+1..4 session switch, Ctrl+F log search)
- Tray icon (minimize to tray, Wails systray API)
- Native Windows toast notifications for: task_done, error, permission waiting, budget alert
- Log search (filter input above LogStream)
- Log export (Markdown/JSON/text)
- Auto-cleanup old logs (by log_retention_days)

**Prompt:**
```
Read PLAN.md sections 9 (hooks), 10 stage 8 (polish). Create hooks.go: RunHook(command, cwd) that executes shell command, returns stdout/stderr/exitCode. Session.Run loop calls pre_task_hook before claude launch (skip session if fails) and post_task_hook after success. Add polish features: 1) Dark/light theme with Tailwind dark: prefix and toggle in settings. 2) Keyboard shortcuts via window.addEventListener (Ctrl+1-4 switch sessions, Ctrl+F focus search). 3) Log search: filter input above LogStream that filters visible entries by text. 4) Log export: button that calls ExportLog Wails binding, saves as .md/.json/.txt via Wails SaveFileDialog. 5) Auto-cleanup: store.CleanOldLogs(retentionDays) called on startup. 6) Tray icon with Wails systray. 7) Windows toast notifications via Wails notification API.
```

---

## Dependency Graph

```
TASK-01 (scaffold + config)
├── TASK-02 (SQLite store)
├── TASK-03 (stream-json parser)
│   ├── TASK-04 (session core) ──────────┐
│   └── TASK-05 (permission system)      │
│        │                               │
│        └───────── TASK-06 (manager + wails bindings) ←──┘
│                      │
│                      ├── TASK-07 (frontend: layout + sidebar)
│                      │   ├── TASK-08 (frontend: log view + controls)
│                      │   ├── TASK-09 (frontend: permission UI)
│                      │   ├── TASK-10 (frontend: settings)
│                      │   └── TASK-11 (frontend: history + cost dashboard)
│                      │
│                      ├── TASK-12 (optimization engine)
│                      ├── TASK-13 (pre-flight analysis backend)
│                      │   └── TASK-14 (pre-flight analysis UI) ← also depends on TASK-07
│                      │
│                      └── TASK-15 (hooks + polish) ← also depends on TASK-08
```

## Parallel Execution

These task groups can run in parallel (if worktrees are used):

- **Group A:** TASK-02, TASK-03 (after TASK-01)
- **Group B:** TASK-04, TASK-05 (after TASK-03)
- **Group C:** TASK-08, TASK-09, TASK-10, TASK-11 (after TASK-07)
- **Group D:** TASK-12, TASK-13 (after TASK-06)
