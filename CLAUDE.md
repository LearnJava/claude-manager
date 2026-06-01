# Claude Session Manager

Desktop app (Windows) to launch, monitor, and control parallel Claude Code CLI sessions across multiple projects.

## Tech Stack

- **Backend:** Go 1.23+, Wails v2 (desktop shell)
- **Frontend:** Svelte + TypeScript + Tailwind CSS
- **Config:** TOML (`~/.claude-manager/config.toml`)
- **Storage:** SQLite (session history, metrics, logs)
- **IPC:** Wails bindings (Go functions callable from JS as async, events via `runtime.EventsEmit`)

## Architecture

```
claude-manager/
├── main.go                          # Wails bootstrap
├── app.go                           # App struct, Wails lifecycle, exported methods
├── internal/
│   ├── config/
│   │   ├── types.go                 # AppConfig, GlobalSettings, ProjectConfig, SessionConfig, PermissionRule
│   │   └── config.go                # Load/save TOML, defaults, validation
│   ├── session/
│   │   ├── manager.go               # SessionManager: Start/Stop/Resume/SendMessage/GetAll
│   │   ├── session.go               # Session goroutine: bidirectional streaming, CLI args builder
│   │   ├── parser.go                # Parse stream-json: assistant, tool_use, result, permission_request, rate_limit_event, init
│   │   ├── input.go                 # Write to stdin: user_message, permission_response
│   │   └── ratelimit.go             # Rate limit detection, retry logic, timers
│   ├── permission/
│   │   ├── handler.go               # Handle permission_request: auto-approve rules -> UI queue -> respond via stdin
│   │   ├── rules.go                 # Match rules from TOML + runtime (glob patterns)
│   │   └── queue.go                 # Global pending permissions queue
│   ├── analysis/
│   │   ├── preflight.go             # Run analyst session (haiku, --permission-mode plan, --json-schema)
│   │   ├── plan.go                  # TaskPlan: subtasks, execution order, dependencies, context passing
│   │   └── schema.go                # JSON Schema for analyst structured output
│   ├── optimization/
│   │   ├── context.go               # Context utilization monitor, auto-restart at threshold
│   │   ├── cache.go                 # Cache efficiency tracking, warming delay between session starts
│   │   ├── loop.go                  # Loop detection (repeated tool calls)
│   │   ├── routing.go               # Auto model routing by task complexity
│   │   └── reporter.go              # Optimization summary report
│   ├── store/
│   │   ├── store.go                 # SQLite: init, CRUD for runs/logs/plans/metrics
│   │   └── migrations.go            # CREATE TABLE statements, indexes
│   └── hooks/
│       └── hooks.go                 # Pre/post task hooks (shell commands)
├── frontend/src/
│   ├── App.svelte                   # Root layout: sidebar + main panel + status bar
│   ├── stores/
│   │   ├── sessions.ts              # Session state, Wails event subscriptions
│   │   └── projects.ts              # Projects state
│   ├── components/
│   │   ├── Sidebar.svelte           # Project tree, session status indicators, start/stop
│   │   ├── LogStream.svelte         # Real-time log with color coding, autoscroll
│   │   ├── SessionCard.svelte       # Session header: metrics, context bar, cost
│   │   ├── SessionInput.svelte      # Message input for bidirectional streaming
│   │   ├── PermissionBanner.svelte  # Permission request overlay on log
│   │   ├── PermissionQueue.svelte   # Global permission queue (all sessions)
│   │   ├── PlanReview.svelte        # Pre-flight analysis plan view/edit
│   │   ├── StatusBar.svelte         # Bottom bar: active count, waiting, cost, rate limit
│   │   ├── History.svelte           # Past runs table with expandable logs
│   │   ├── Settings.svelte          # Global/project/session settings modals
│   │   └── RateLimitBanner.svelte   # Rate limit countdown banner
│   └── lib/
│       └── formatters.ts            # Log formatting, time, cost
├── cmd/                             # Test/control harness binaries (see PLAN.md §21)
│   ├── fakeclaude/                  # Scripted Claude CLI double (deterministic stream-json)
│   └── cm-mcp/                      # MCP server: drive the app as agent tools
├── internal/
│   ├── testkit/                     # Scenario loader + fakeclaude<->parser conformance
│   └── control/                     # Headless control-plane: Emitter, RPC, WS, wait, MCP tools
├── testdata/                        # scenarios/ (fakeclaude), e2e/ (runner), configs/
├── frontend/tests/                  # Playwright DOM specs
├── config.example.toml
├── PLAN.md                          # Full specification (§14-21)
├── TASKS.md                         # App task breakdown (TASK-01..15)
└── HARNESS-TASKS.md                 # Test/control harness task breakdown (H1..H6)
```

## Key Design Decisions

### Bidirectional Streaming
Sessions use `--input-format stream-json` + `--output-format stream-json`. Manager writes to stdin (user messages, permission responses) and reads stdout (events). This enables interactive sessions, not just one-shot `-p` calls.

### CLI Launch Command
```bash
claude -p \
  --input-format stream-json --output-format stream-json \
  --include-partial-messages --replay-user-messages \
  --session-id <uuid> --name <name> --model <model> \
  --permission-mode <mode> --effort <level> \
  [--fallback-model <model>] [--max-budget-usd <amount>] \
  [--max-turns <n>] [--worktree <name>] \
  [--allowed-tools <tools>] [--disallowed-tools <tools>] \
  [--append-system-prompt <text>] [--add-dir <dirs>] \
  [--exclude-dynamic-system-prompt-sections]
```
Initial prompt sent via stdin as `{"type":"user_message","message":"..."}`.

### Stream-JSON Events (stdout)
Key event types to parse:
- `{"type":"system","subtype":"init",...}` — session info, model, tools, version
- `{"type":"assistant","message":{"content":[...],"usage":{...}}}` — text/tool_use with per-turn token usage
- `{"type":"result","total_cost_usd":...,"usage":{...},"modelUsage":{...}}` — final metrics
- `{"type":"rate_limit_event","rate_limit_info":{"utilization":0.88,"resetsAt":...}}` — rate limit status

### Permission Handling
When `permission_mode != "bypassPermissions"`, Claude CLI sends permission requests via stdout and blocks waiting for response on stdin. The manager MUST:
1. Parse the permission request from stream-json
2. Check auto-approve rules (TOML config + runtime rules)
3. If no rule matches → emit to UI, set status=WaitingPermission, block goroutine
4. Wait for user response via UI → write response to stdin
5. Never ignore — session hangs forever if no response

### Token Optimization
- Context grows with every turn (all messages re-sent). Monitor `usage` in each `assistant` event
- Auto-restart session when context > 75% of `contextWindow` (from init event)
- Use `--exclude-dynamic-system-prompt-sections` to share system prompt cache across sessions
- Stagger session starts by 3s for cache warming (first session pays cache creation, others get cache reads)
- Loop detection: if same tool+input appears 3+ times in last 20 calls, alert/hint

### Session Status Flow
```
Idle → Starting → Working ⇄ WaitingPermission
                    ↓
              RateLimited → Retrying → Working (loop)
                    ↓
              Stopping → Idle
                    ↓
                 Error
```

## Build & Run

```bash
wails dev          # Dev mode (hot reload frontend)
wails build        # Production: build/bin/claude-manager.exe (~15-20 MB)
```

## Go Dependencies

```
github.com/wailsapp/wails/v2
github.com/BurntSushi/toml
github.com/mattn/go-sqlite3
```

No DI frameworks, no ORMs. Standard library for everything else.

### Testing & Control Harness
The app is fully drivable and testable without a GUI and without spending API tokens (see PLAN.md §21):
- **`fakeclaude`** (`cmd/fakeclaude`) — a scripted Claude CLI double. Set as `claude_path`, it emits deterministic stream-json from JSON scenarios in `testdata/scenarios/`. Covers every Session Status (permission, rate limit, error, loop, context-growth).
- **`Emitter` indirection** (`internal/control/emit.go`) — all `session:*` events flow through an `Emitter` interface, not direct `runtime.EventsEmit`. `WailsEmitter` feeds the UI; `ControlEmitter` broadcasts to the control-plane. The SessionManager takes an `Emitter` in its constructor.
- **Control-plane** (`internal/control`) — at `CM_CONTROL=1`, a loopback HTTP+WS server exposes every SessionManager method via JSON-RPC and streams events, plus `/wait` for blocking until a status/event. Additive to the GUI.
- **MCP server** (`cmd/cm-mcp`) — proxies the control-plane into agent tools (`start_session`, `send_message`, `approve_permission`, `wait_for_status`, …) so Claude can press every button.
- **E2E runner** (`internal/control/e2e_test.go`) — replays `testdata/e2e/*.json` (`do`/`wait`/`assert`) against the app + fakeclaude, deterministically.
- **Playwright** (`frontend/tests`) — clicks the real DOM and double-checks via the control-plane event stream.

Task breakdown: [HARNESS-TASKS.md](./HARNESS-TASKS.md) (H1-H6).

## SQLite Tables

- `session_runs` — completed runs with cost/tokens/duration/model
- `session_logs` — log entries per run (batch insert)
- `daily_metrics` — aggregated cost/tokens per day per project
- `task_plans` — pre-flight analysis plans
- `plan_subtasks` — subtasks within plans

## Conventions

- Go: standard project layout, `internal/` for private packages
- Errors: return `error`, no panics. Log to session log buffer
- Concurrency: each session is a goroutine, communicate via channels (`inputCh`, `permissionCh`). Protect shared state with `sync.Mutex`
- Frontend: Svelte stores subscribe to Wails events via `runtime.EventsOn()`. Call Go via auto-generated bindings
- Config: TOML with sensible defaults. Zero values mean "disabled" or "unlimited" (e.g., `max_budget_usd = 0` means no limit)
- IDs: sessions identified as `"{project}/{session}"` (e.g., `"lumen/P1"`)

## Reference

Full specification: [PLAN.md](./PLAN.md) — sections 14-21 cover CLI flags, bidirectional streaming, permissions, pre-flight analysis, token metrics, optimization, and the test/control harness.

Task breakdowns: [TASKS.md](./TASKS.md) (app, TASK-01..15) and [HARNESS-TASKS.md](./HARNESS-TASKS.md) (harness, H1..H6).
