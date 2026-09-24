# Testing & Control Harness

How to drive and test the app without a GUI and without spending API tokens, and how Claude itself can exercise the running app via cm-mcp. See [CLAUDE.md](../CLAUDE.md) for the project index and [HARNESS-TASKS.md](../HARNESS-TASKS.md) for the task breakdown.

## Harness binaries & control-plane
The app is fully drivable and testable without a GUI and without spending API tokens (see PLAN.md §21):
- **`fakeclaude`** (`cmd/fakeclaude`) — a scripted Claude CLI double. Set as `claude_path`, it emits deterministic stream-json from JSON scenarios in `testdata/scenarios/`. Covers every Session Status (permission, rate limit, error, loop, context-growth).
- **`fakeworker`** (`cmd/fakeworker`) — a scripted OpenAI-compatible worker double for mixed programming (MP-07). Serves `POST /chat/completions` as SSE from JSON scenarios in `testdata/worker-scenarios/` (clean patch, broken patch → feedback, 429 storm, `finish_reason=length` continuation/loop, missing `>>>END`). Point a `[[worker]]` `base_url` at it; scenario via `-scenario` flag or `FAKEWORKER_SCENARIO`.
- **`Emitter` indirection** (`internal/control/emit.go`) — all `session:*` events flow through an `Emitter` interface, not direct `runtime.EventsEmit`. `WailsEmitter` feeds the UI; `ControlEmitter` broadcasts to the control-plane. The SessionManager takes an `Emitter` in its constructor.
- **Control-plane** (`internal/control`) — a loopback HTTP+WS server exposing every SessionManager method via JSON-RPC, streaming events, plus `/wait` for blocking until a status/event. Additive to the GUI and **on by default** (`control.Enabled()`; set `CM_CONTROL=0`/`false`/`off` to turn it off): an app that is only drivable after a relaunch with a special environment variable is, in practice, never drivable when it matters — the session you want to inspect is the one already running. Port from `CM_CONTROL_PORT` (default 7333), token from `CM_CONTROL_TOKEN` (else generated). Since a GUI build has no terminal to print a generated token to, the server writes `~/.claude-manager/control.json` (`{addr, token, pid}`, mode 0600, atomic tmp→rename) and deletes it when it stops; `cm-mcp` reads that file when `CM_CONTROL_ADDR`/`CM_CONTROL_TOKEN` are unset, so the tools work against a normally-launched app with no setup. The listener is bound synchronously in `StartFromEnv`, so a port clash (a second instance, or `wails dev` next to the installed app) fails loudly instead of leaving a stale endpoint file pointing at someone else's server.
- **MCP server** (`cmd/cm-mcp`) — proxies the control-plane into agent tools (`start_session`, `send_message`, `approve_permission`, `wait_for_status`, …) so Claude can press every button.
- **E2E runner** (`internal/control/e2e_test.go`) — replays `testdata/e2e/*.json` (`do`/`wait`/`assert`) against the app + fakeclaude, deterministically.
- **Playwright** (`frontend/tests`) — clicks the real DOM and double-checks via the control-plane event stream.

Task breakdown: [HARNESS-TASKS.md](../HARNESS-TASKS.md) (H1-H6).

## GUI Testing by Claude (via cm-mcp)

Claude can interact with the running application directly — starting sessions, approving permissions, reading logs, waiting for state transitions — without human intervention. This is the primary way to test UI flows end-to-end.

#### One-time setup (already done)

```bash
# Build all harness binaries
go build -o build/cm-mcp.exe          ./cmd/cm-mcp
go build -o build/fakeclaude.exe       ./cmd/fakeclaude
go build -o build/playwright-server.exe ./cmd/playwright-server

# Register cm-mcp as an MCP server in Claude Code (project-local)
claude mcp add cm -- D:/GoProjects/claude-manager/build/cm-mcp.exe
```

The registration is stored in `.claude.json` (project scope) and persists across sessions.

#### Starting a testable instance

```bash
wails dev
```

Any normal launch — `wails dev` or the built `claude-manager.exe` — starts the full Wails GUI **and** binds the control-plane on `http://127.0.0.1:7333`, writing its address and token to `~/.claude-manager/control.json`. The cm-mcp server reads that file, so it connects with no environment set. Pass `CM_CONTROL=0` to run without it.

> For headless (no Wails GUI) backend-only testing, use `playwright-server` instead:
> ```bash
> ./build/playwright-server.exe -config testdata/configs/playwright.toml -scenarios testdata/scenarios
> ```

#### cm-mcp tools available to Claude

| Tool | What it does |
|---|---|
| `start_session` | Start a session by project + name |
| `stop_session` | Stop a session (soft=true waits for current task) |
| `restart_session` | Hard restart or resume from saved CLI session ID |
| `send_message` | Send a user message to a running session |
| `approve_permission` | Approve a pending permission request |
| `deny_permission` | Deny a pending permission request |
| `get_sessions` | Snapshot of all sessions (status, model, metrics) |
| `get_session_logs` | Tail log entries for a session |
| `get_pending_permissions` | List all pending permission requests |
| `get_metrics` | Cost/token metrics for a project |
| `wait_for_status` | Block until session reaches a target status |
| `wait_for_event` | Block until a specific event is emitted |
| `set_global_settings` | Partial-update AppConfig at runtime |
| `run_preflight` | Run pre-flight analysis for a task |
| `execute_plan` | Execute a pre-generated task plan |
| `register_mixed_brief` | Register a mixed-programming brief for dispatch |
| `dispatch_mixed_task` | Run the mixed-programming round loop (brief → patches → gates) |
| `get_mixed_rounds` | Persisted mixed-task state (rounds, patches, gates) per project |
| `wait_for_worker_status` | Block until a mixed task reaches done / needs_human |

#### Typical test workflow

```
1. User runs: wails dev   (or playwright-server for headless) — control-plane is on by default
2. Claude starts a new conversation — cm-mcp tools are now available
3. Claude calls start_session → wait_for_status(working)
4. Claude reads get_session_logs to verify output
5. Claude calls approve_permission if the session is waiting
6. Claude calls stop_session → wait_for_status(idle)
7. Claude asserts final state via get_sessions / get_metrics
```

#### Using fakeclaude for deterministic scenarios

Set `claude_path` in the config to `build/fakeclaude.exe` and point `FAKECLAUDE_SCENARIO` at `testdata/scenarios/`. Each scenario file is a JSON array of stream-json events that fakeclaude emits deterministically — no real Claude CLI, no API tokens spent.

```toml
# testdata/configs/playwright.toml
[settings]
claude_path = "build/fakeclaude.exe"
```

