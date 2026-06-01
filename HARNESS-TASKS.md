# Harness Tasks — тестовый и управляющий слой

Задачи на построение инфраструктуры, дающей агенту (Claude/CI) полный контроль над приложением и детерминированное тестирование всех кнопок и параметров **без GUI и без расхода API-токенов**.

Полная спецификация: [PLAN.md](./PLAN.md) §21. Задачи приложения: [TASKS.md](./TASKS.md) (TASK-01…15).

Каждая задача рассчитана на одну Claude Code сессию (~15-30 turns, ~5-10 файлов). Порядок — по зависимостям; зависимости в TASK-NN ссылаются на задачи приложения.

**Рекомендуемый порядок:** H1 (независим, делать первым) → H2 (после TASK-03) → H3 (после TASK-06) → H4 → H5 → H6 (после TASK-07/08).

Каждая задача обязана сопровождаться тестами (правило проекта: каждая функция имеет тест; учитывать регрессию).

## Status: ALL HARNESS TASKS COMPLETE ✓

| Задача | Статус | Ключевые файлы |
|---|---|---|
| H1 | ✓ DONE | cmd/fakeclaude/, internal/testkit/scenario.go, runner.go |
| H2 | ✓ DONE | testdata/scenarios/ (9 сценариев), testkit/conformance_test.go |
| H3 | ✓ DONE | internal/control/emit.go, server.go, rpc.go, wait.go |
| H4 | ✓ DONE | cmd/cm-mcp/main.go, internal/control/mcptools.go |
| H5 | ✓ DONE | internal/control/e2e_runner.go, e2e_test.go, testdata/e2e/ |
| H6 | ✓ DONE | frontend/playwright.config.ts, frontend/tests/*.spec.ts |

---

## HARNESS-01: `fakeclaude` — двойник CLI + формат сценариев

**Depends on:** ничего (только формат stream-json из PLAN.md §18.1, §21.3)
**Files to create:**
- `cmd/fakeclaude/main.go` — точка входа: читает stdin, эмитит stream-json по сценарию
- `internal/testkit/scenario.go` — `Scenario`, `Step`, загрузчик JSON, движок подстановок `${...}`
- `internal/testkit/runner.go` — исполнение шагов: `emit` (с `delay_ms`), `await_stdin`, ветвление `on`
- `testdata/scenarios/happy-path.json`, `permission-allow.json`, `permission-deny.json`, `error-exit.json`
- `cmd/fakeclaude/main_test.go` — golden-тесты вывода
- `internal/testkit/scenario_test.go` — юнит-тесты загрузчика и подстановок

**Prompt:**
```
Read CLAUDE.md and PLAN.md sections 18.1 and 21.3. Create a fakeclaude binary that behaves like `claude -p --input-format stream-json --output-format stream-json`: it reads stdin messages (user_message, permission_response) and writes scripted stream-json events to stdout.

Create internal/testkit/scenario.go with the Scenario and Step structs and a JSON loader matching the format in PLAN.md §21.3.2 (steps of type "emit" with delay_ms and an event object, and "await_stdin" with expect/timeout_ms/store_as). Implement ${session_id}, ${model}, ${cwd}, ${name} substitution from CLI flags passed to fakeclaude.

Create internal/testkit/runner.go that executes a scenario against stdin/stdout: emit writes the event as a JSON line after delay_ms (scaled by FAKECLAUDE_SPEED env, default 1.0); await_stdin blocks reading stdin until a message of the expected type arrives (or times out), storing it under store_as for later branching via the "on" field.

Create cmd/fakeclaude/main.go: parse the scenario from FAKECLAUDE_SCENARIO env (a file path, or a directory in which case read the first user_message and pick the scenario whose "match" regex matches the prompt text). Ignore all unknown CLI flags except --session-id, --name, --model, --add-dir/cwd for substitution.

Create the 4 starter scenarios in testdata/scenarios/. Add golden tests (main_test.go) that feed a scripted stdin and assert the exact stdout event sequence, and unit tests for the loader/substitution. Use only encoding/json and the standard library. Verify `go test ./internal/testkit/... ./cmd/fakeclaude/...` passes.
```

---

## HARNESS-02: Библиотека сценариев на все состояния + conformance

**Depends on:** HARNESS-01, TASK-03 (stream-json parser)
**Files to create:**
- `testdata/scenarios/rate-limit.json`, `loop.json`, `context-growth.json`, `multi-turn.json`, `budget-exceeded.json`
- `internal/testkit/conformance_test.go` — каждый сценарий прогоняется через `session/parser.go`
- `internal/testkit/scenarios_doc.md` — таблица сценариев и какое состояние каждый покрывает

**Prompt:**
```
Read PLAN.md sections 18.1-18.2, 21.3.3 and the existing parser in internal/session/parser.go. Author the full scenario library covering every state in the Session Status Flow: rate-limit.json (rate_limit_event with utilization 0.9 and resetsAt, then resume), loop.json (the same tool_use input emitted 3 times to trigger LoopDetector), context-growth.json (usage growing past 75% of context_window to trigger auto-restart), multi-turn.json (responds to several user_message inputs in sequence), budget-exceeded.json (result whose total_cost_usd exceeds a small max_budget_usd).

Create internal/testkit/conformance_test.go: for every file in testdata/scenarios/, run each emitted event through internal/session/parser.go and assert that every event is recognized (no unknown/unparsed events) and that the extracted types (LogEntry, TokenUsage, SessionResult, RateLimitInfo, InitInfo) are populated as expected. This guarantees fakeclaude output and the real parser stay compatible — a regression guard.

Write scenarios_doc.md mapping each scenario to the state/feature it exercises. Verify `go test ./internal/testkit/...` passes.
```

---

## HARNESS-03: Control-plane — headless bridge + Emitter

**Depends on:** TASK-06 (SessionManager + Wails bindings)
**Files to create:**
- `internal/control/emit.go` — `Emitter`, `WailsEmitter`, `MultiEmitter`, `ControlEmitter` + ring-буфер событий
- `internal/control/server.go` — WS+HTTP сервер, активация по `CM_CONTROL`, токен-аутентификация
- `internal/control/rpc.go` — JSON-RPC роутер на методы `SessionManager`
- `internal/control/wait.go` — `POST /wait`, сверка с буфером, ожидание по `match`
- `internal/control/*_test.go` — тесты роутинга, аутентификации, wait-семантики
**Files to modify:**
- `internal/session/manager.go` — события через `Emitter` (если TASK-06 уже использует `Emitter` — только подключить `ControlEmitter`)
- `app.go` / `main.go` — поднять control-сервер при `CM_CONTROL=1`

**Prompt:**
```
Read CLAUDE.md and PLAN.md section 21.4. Create the internal/control package.

emit.go: define the Emitter interface { Emit(event string, data any) }. Implement WailsEmitter (wraps runtime.EventsEmit), MultiEmitter (fans out to several Emitters), and ControlEmitter (serializes {event, data, ts}, broadcasts to connected WS clients, and keeps a per-session ring buffer of the last N events for wait-for queries). If SessionManager already takes an Emitter, just wire ControlEmitter in; otherwise refactor manager.go to emit all session:* events through an injected Emitter (no direct runtime.EventsEmit outside WailsEmitter).

server.go: when CM_CONTROL=1 (or a -control flag), start an HTTP+WS server on 127.0.0.1:${CM_CONTROL_PORT:-7333}, loopback only, requiring an X-CM-Token header equal to CM_CONTROL_TOKEN (generate and print one to stdout if unset). The GUI must keep working — control-plane is additive.

rpc.go: POST /rpc handling JSON-RPC 2.0, dispatching to SessionManager public methods (StartSession, StopSession, SendMessage, RespondPermission, GetAllSessions, GetSessionLog, GetSessionMetrics, GetConfig, UpdateConfig, RunAnalysis, ExecutePlan, etc. per §21.4.3). Build the dispatch table from a single registry shared with the Wails bindings so new methods are exposed to both automatically.

wait.go: WS GET /events streams all session:* events; POST /wait with {event, match, timeout_ms} returns the first event satisfying match (checking the ring buffer first for already-occurred events) or a timeout.

Wire server startup into app.go/main.go behind CM_CONTROL. Add tests for RPC dispatch, token auth, and wait-matching. Verify `wails build` compiles and `go test ./internal/control/...` passes.
```

---

## HARNESS-04: MCP-сервер `claude-manager-mcp`

**Depends on:** HARNESS-03
**Files to create:**
- `cmd/cm-mcp/main.go` — stdio MCP-сервер, прокси в control-plane
- `internal/control/mcptools.go` — определения инструментов (имя, схема параметров, маппинг на RPC/wait)
- `cmd/cm-mcp/main_test.go` — тесты маппинга инструмент → RPC против фейкового control-сервера

**Prompt:**
```
Read PLAN.md section 21.5. Create cmd/cm-mcp: a stdio MCP server that is a thin proxy into the control-plane (address from CM_CONTROL_ADDR, token from CM_CONTROL_TOKEN env).

internal/control/mcptools.go: declare the tool set from the §21.5 table — actions (start_session, stop_session, restart_session, send_message, approve_permission, deny_permission, set_global_settings, run_preflight, execute_plan), queries (get_sessions, get_session_logs, get_pending_permissions, get_metrics), and the blocking primitives wait_for_status and wait_for_event. Each tool maps to a /rpc method or /wait call. Define JSON Schemas for each tool's parameters.

main.go: implement the MCP stdio protocol (initialize, tools/list, tools/call), translate tools/call into the corresponding control-plane HTTP request, and return the result. wait_for_status maps to POST /wait on session:status with a status match; wait_for_event to /wait on the given event.

Add tests that run mcptools against a stub control server and assert each tool issues the correct RPC/wait request. Document registration: `claude mcp add cm -- cm-mcp`. Verify `go test ./cmd/cm-mcp/... ./internal/control/...` passes.
```

---

## HARNESS-05: Сквозной scenario-runner (e2e)

**Depends on:** HARNESS-04, HARNESS-01
**Files to create:**
- `internal/control/e2e_test.go` — поднимает приложение в control-режиме с `fakeclaude`, проигрывает e2e-сценарии
- `internal/control/e2e_runner.go` — парсер и исполнитель e2e-формата (`do` / `wait` / `assert`)
- `testdata/e2e/permission-flow.json`, `rate-limit-recovery.json`, `loop-detected.json`, `budget-alert.json`
- `testdata/configs/one-session.toml`, `two-sessions.toml`

**Prompt:**
```
Read PLAN.md section 21.6. Create the end-to-end scenario runner.

e2e_runner.go: load an e2e scenario (PLAN.md §21.6.1 format — steps with "do"+"with" for RPC actions, "wait"+"match" for events, "assert"+"with"+"expect" for queries). Execute steps in order against a running control-plane via /rpc, /events and /wait.

e2e_test.go: a Go test that builds fakeclaude, starts the app with CM_CONTROL=1, claude_path pointing at the built fakeclaude, and FAKECLAUDE_SCENARIO=testdata/scenarios, then runs each file in testdata/e2e/ through the runner and fails on any unmet wait or assert. Cover: permission-flow (start → WaitingPermission → allow → Idle → assert cost), rate-limit-recovery (RateLimited → Retrying → Idle), loop-detected (LoopDetector fires), budget-alert (budget exceeded event).

Provide the test configs in testdata/configs/. The same e2e scenario files must be drivable interactively by an agent via the MCP tools from HARNESS-04. Verify `go test ./internal/control/...` passes deterministically (no real claude, no network).
```

---

## HARNESS-06: Frontend DOM-харнес (Playwright)

**Depends on:** TASK-07, TASK-08, HARNESS-03
**Files to create:**
- `frontend/playwright.config.ts`
- `frontend/tests/sidebar.spec.ts`, `session.spec.ts`, `permission.spec.ts`, `settings.spec.ts`
- `frontend/tests/helpers/control.ts` — клиент control-plane WS/RPC для двойной проверки

**Prompt:**
```
Read PLAN.md section 21.7 and CLAUDE.md frontend section. Set up Playwright DOM testing for the Svelte frontend.

playwright.config.ts: launch the vite dev server (or `wails dev`) for the frontend and assume the Go backend runs in control mode (CM_CONTROL=1) with fakeclaude as claude_path.

helpers/control.ts: a small client that connects to the control-plane WS (/events) and /rpc so each test can assert both the DOM and the corresponding backend event — catching "button clicked but method not called" desync.

Specs: sidebar.spec.ts (click start on a project, expect a status dot turn green and a session:status event), session.spec.ts (type into SessionInput, click Send, expect SendMessage RPC + log entries in LogStream), permission.spec.ts (drive a permission scenario, click Allow in PermissionBanner, expect RespondPermission + session resumes), settings.spec.ts (open Settings, switch tabs, edit a SessionConfig field, Save, expect UpdateConfig RPC and persisted TOML).

Verify `npx playwright test` passes against the app running with fakeclaude.
```

---

## Граф зависимостей

```
HARNESS-01 (fakeclaude)            независим — делать первым
   └── HARNESS-02 (scenarios + conformance)   ← + TASK-03 (parser)

TASK-06 (manager + bindings)
   └── HARNESS-03 (control-plane + Emitter)
          └── HARNESS-04 (MCP server)
                 └── HARNESS-05 (e2e runner)   ← + HARNESS-01

TASK-07/08 (frontend)
   └── HARNESS-06 (Playwright DOM)             ← + HARNESS-03
```

## Параллельный запуск (через worktrees)

- **Сразу:** HARNESS-01 (параллельно идущим TASK-01…05).
- После TASK-03: HARNESS-02.
- После TASK-06: HARNESS-03 → HARNESS-04 → HARNESS-05 (цепочка).
- После TASK-08: HARNESS-06.

## Сцепка с приложением

`SessionManager` (TASK-06) обязан эмитить события через интерфейс `Emitter` (PLAN.md §21.4.2), а не вызывать `runtime.EventsEmit` напрямую. Требование добавлено в промпт TASK-06 — это избавляет HARNESS-03 от рефакторинга манагера.
