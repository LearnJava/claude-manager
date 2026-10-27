# Testing & Control Harness

How to drive and test the app without a GUI and without spending API tokens, and how Claude itself can exercise the running app via cm-mcp. See [CLAUDE.md](../CLAUDE.md) for the project index and [HARNESS-TASKS.md](../HARNESS-TASKS.md) for the task breakdown.

## Harness binaries & control-plane
The app is fully drivable and testable without a GUI and without spending API tokens (see PLAN.md §21):
- **`fakeclaude`** (`cmd/fakeclaude`) — a scripted Claude CLI double. Set as `claude_path`, it emits deterministic stream-json from JSON scenarios in `testdata/scenarios/`. Covers every Session Status (permission, rate limit, error, loop, context-growth).
- **`fakeworker`** (`cmd/fakeworker`) — a scripted OpenAI-compatible worker double for mixed programming (MP-07). Serves `POST /chat/completions` as SSE from JSON scenarios in `testdata/worker-scenarios/` (clean patch, broken patch → feedback, 429 storm, `finish_reason=length` continuation/loop, missing `>>>END`). Point a `[[worker]]` `base_url` at it; scenario via `-scenario` flag or `FAKEWORKER_SCENARIO`.
- **`Emitter` indirection** (`internal/control/emit.go`) — all `session:*` events flow through an `Emitter` interface, not direct `runtime.EventsEmit`. `WailsEmitter` feeds the UI; `ControlEmitter` broadcasts to the control-plane. The SessionManager takes an `Emitter` in its constructor.
- **Running the Playwright suite from a developer session.** The session's own environment carries `CM_CONTROL_TOKEN` (and `CM_SESSION_ID`) of the running manager, which makes `global-setup.ts` skip building and starting its own server and try the manager's port instead (`ECONNREFUSED 127.0.0.1:7334`, or worse, a real app). Run it as `env -u CM_CONTROL_TOKEN -u CM_SESSION_ID CM_CONTROL_PORT=7335 CM_VITE_PORT=5199 npx playwright test <spec>` (a free port of your own for each). The server's CORS must allow every header the bridge sends (`X-CM-Token`, `X-CM-Level`); a header added to `helpers/bridge.ts` without `Access-Control-Allow-Headers` in `internal/control/server.go` makes the whole page see "No projects configured".
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
claude mcp add cm -e CM_MCP_LEVEL=admin -- D:/GoProjects/claude-manager/build/cm-mcp.exe
```

`cm-mcp` runs at access level `operate` by default (no `approve_permission`, no `update_global_settings`); the GUI test scenarios need `admin`, hence `CM_MCP_LEVEL=admin` above. See "Уровни доступа" and "Безопасность" in [docs/mcp.md](./mcp.md).

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

The tool reference (name, access level, annotations, what it does) and the `App`-method parity map are generated from the registry — see "Справочник инструментов" and "Карта паритета" in [docs/mcp.md](./mcp.md). Regenerate with `go run ./cmd/cm-mcp -gen-docs`; `TestDocsInSync` fails when they drift.

#### Typical test workflow

```
1. User runs: wails dev   (or playwright-server for headless) — control-plane is on by default
2. Claude starts a new conversation — cm-mcp tools are now available
3. Claude calls start_session → wait_for_status(working)
4. Claude reads get_session_logs to verify output
5. Claude calls approve_permission if the session is waiting
6. Claude calls stop_session → wait_for_status(idle)
7. Claude asserts final state via get_sessions / get_cost
```

#### Evaluating the tools with a real agent

`CM_MCP_EVAL=1 go run ./cmd/cm-mcp-eval` (spends tokens) lets `claude -p` manage a throw-away manager through `cm-mcp` alone and judges the outcome by the manager's state; `go test ./cmd/cm-mcp-eval` checks the same tasks with scripted agents, free. See "Оценка реальным агентом" in [docs/mcp.md](./mcp.md).

#### Using fakeclaude for deterministic scenarios

Set `claude_path` in the config to `build/fakeclaude.exe` and point `FAKECLAUDE_SCENARIO` at `testdata/scenarios/`. Each scenario file is a JSON array of stream-json events that fakeclaude emits deterministically — no real Claude CLI, no API tokens spent.

```toml
# testdata/configs/playwright.toml
[settings]
claude_path = "build/fakeclaude.exe"
```

