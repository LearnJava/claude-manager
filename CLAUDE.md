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
├── app.go                           # App struct, Wails lifecycle, 40 exported methods
├── internal/
│   ├── config/
│   │   ├── types.go                 # AppConfig, GlobalSettings, OptimizationSettings,
│   │   │                            #   ProjectConfig, SessionConfig, PermissionRule,
│   │   │                            #   WorkerConfig (mixed programming, MIXED-TASKS.md)
│   │   └── config.go                # Load/save TOML, defaults, validation
│   ├── logger/
│   │   └── logger.go                # Global slog logger (logger.L); Init() opens app.log
│   ├── session/
│   │   ├── manager.go               # SessionManager: Start/Stop/Resume/Override/SendMessage/GetAll
│   │   │                            #   + ClearSessionState/GetSessionState (crash recovery)
│   │   ├── session.go               # Session goroutine: bidirectional streaming, CLI args builder,
│   │   │                            #   task source check (hasTasks), crash recovery (--resume),
│   │   │                            #   rate-limit fallback restart, auth error (403) handling
│   │   ├── state.go                 # StateStore: persist session_id to ~/.claude-manager/state/
│   │   │                            #   for crash recovery; atomic write (tmp → rename)
│   │   ├── parser.go                # Parse stream-json: assistant, tool_use, result,
│   │   │                            #   permission_request, rate_limit_event, init,
│   │   │                            #   TodoWrite todos (current task + progress)
│   │   ├── input.go                 # Write to stdin: user_message, permission_response
│   │   └── ratelimit.go             # Rate limit detection, retry logic, timers
│   ├── permission/
│   │   ├── handler.go               # Handle permission_request: auto-approve rules -> UI queue -> stdin
│   │   ├── rules.go                 # Match rules from TOML + runtime (glob patterns)
│   │   └── queue.go                 # Global pending permissions queue
│   ├── analysis/
│   │   ├── preflight.go             # Run analyst session (haiku, --permission-mode plan, --json-schema)
│   │   ├── plan.go                  # TaskPlan: subtasks, execution order, dependencies, context passing
│   │   ├── executor.go              # One-shot CLI executor for approved plan subtasks (context handoff)
│   │   ├── brief.go                 # Mixed-programming brief generation (MP-06): self-contained
│   │   │                            #   English ТЗ, verbatim excerpts, patch-format instructions
│   │   └── schema.go                # JSON Schema for analyst structured output + brief output
│   ├── optimization/
│   │   ├── routing.go               # ModelRouter: auto model routing by task complexity
│   │   ├── context.go               # Context utilization monitor, auto-restart at threshold
│   │   ├── cache.go                 # Cache efficiency tracking, warming delay between session starts
│   │   ├── loop.go                  # Loop detection (repeated tool calls, ring buffer)
│   │   └── reporter.go              # Reporter: aggregates ContextMonitor+CacheTracker+LoopDetector
│   │                                #   into Snapshot(sessionID) and GlobalReport() for the UI
│   ├── worker/                      # Mixed programming (MIXED-TASKS.md MP-02..06,08)
│   │   ├── client.go                # OpenAI-compatible worker client: SSE, backoff,
│   │   │                            #   length-continuation, per-key_env semaphore
│   │   ├── persist.go               # Store: worker dialogue persistence (resume by replay)
│   │   ├── patch.go                 # FIND/REPLACE parser + worktree-confined applicator
│   │   ├── gates.go                 # Blocking gate runner + worktree commit (Co-Authored-By)
│   │   ├── round.go                 # RoundOrchestrator: brief→patches→gates loop, TaskStore,
│   │   │                            #   worker:round/patch/gate/done events, crash-resume
│   │   └── quality.go               # BuildQualityReport: per-worker ModelQuality aggregate
│   ├── store/
│   │   ├── store.go                 # SQLite: init, CRUD for runs/logs/plans/metrics/briefs
│   │   └── migrations.go            # CREATE TABLE statements, indexes
│   └── hooks/
│       └── hooks.go                 # Pre/post task hooks (shell commands)
├── frontend/src/
│   ├── App.svelte                   # Root layout: sidebar + main panel + status bar + modals
│   ├── stores/
│   │   ├── sessions.ts              # Session state, Wails event subscriptions
│   │   ├── projects.ts              # Projects state
│   │   ├── theme.ts                 # Dark/light theme toggle, localStorage persistence
│   │   ├── logSearch.ts             # Log filter store, Ctrl+F focus
│   │   └── workers.ts               # Mixed programming: worker:* events, per-project tasks/
│   │                                #   quality, register+dispatch+cancel actions (MP-08)
│   ├── components/
│   │   ├── Sidebar.svelte           # Project tree, session indicators, start/stop/delete,
│   │   │                            #   auto-routing trigger, resizable via drag handle
│   │   ├── ModelPicker.svelte       # Pre-start model selector: recommendation + override dropdowns
│   │   ├── LogStream.svelte         # Real-time log with color coding, autoscroll, search filter
│   │   ├── TaskPanel.svelte         # Right of the log: current task (TodoWrite), todo
│   │   │                            #   checklist, progress %, collapsible session prompt
│   │   ├── SessionView.svelte       # Session header: metrics, context bar, cost, export
│   │   ├── SessionCard.svelte       # Session status badge, model, effort, task count, branch
│   │   ├── SessionInput.svelte      # Message input for bidirectional streaming
│   │   ├── PermissionBanner.svelte  # Permission request inline overlay on log
│   │   ├── PermissionQueue.svelte   # Global permission queue (all sessions)
│   │   ├── PlanReview.svelte        # Pre-flight analysis plan view/edit/approve
│   │   ├── StatusBar.svelte         # Bottom bar: active count, waiting, cost, rate limit
│   │   ├── History.svelte           # Past runs table with filter, sort, CSV export
│   │   ├── Settings.svelte          # Global/project/session/worker settings, model routing,
│   │   │                            #   Workers tab (CRUD + presets), project mixed opt-in
│   │   ├── CostDashboard.svelte     # Cost by period/project, cache efficiency, rate limit
│   │   ├── MixedRun.svelte          # Mixed programming: dispatch form, live activity, round
│   │   │                            #   timelines w/ gate output, model-quality table (MP-08)
│   │   └── RateLimitBanner.svelte   # Rate limit countdown banner
│   └── lib/
│       └── formatters.ts            # Log formatting, time, cost, tokens, percent
├── cmd/                             # Test/control harness binaries (see PLAN.md §21)
│   ├── fakeclaude/                  # Scripted Claude CLI double (deterministic stream-json)
│   ├── fakeworker/                  # Scripted OpenAI-compatible worker double (SSE, MP-07)
│   └── cm-mcp/                      # MCP server: drive the app as agent tools
├── internal/
│   ├── testkit/                     # Scenario loaders + fakeclaude<->parser conformance,
│   │                                #   fakeworker handler (MP-07)
│   └── control/                     # Headless control-plane: Emitter, RPC (incl. mixed), WS,
│                                    #   wait/wait-for-worker, MCP tools, e2e runner
├── testdata/                        # scenarios/ (fakeclaude), worker-scenarios/ (fakeworker),
│                                    #   e2e/ (runner, incl. mixed-*.json), configs/
├── frontend/tests/                  # Playwright DOM specs (incl. mixed.spec.ts)
├── config.example.toml
├── PLAN.md                          # Full specification (§14-21)
├── TASKS.md                         # App task breakdown (TASK-01..15)
├── HARNESS-TASKS.md                 # Test/control harness task breakdown (H1..H6)
└── MIXED-TASKS.md                   # Mixed programming task breakdown (MP-01..08, done)
```

## Key Design Decisions

### Config Layering (Per-Project Settings in the Project Folder)
Config is layered like Claude Code's own `user → project → local`, so a project's
context travels with its repo instead of living only in a global file:

| Layer | File | Holds | Committed? |
|---|---|---|---|
| Global | `~/.claude-manager/config.toml` | `[settings]`, `[optimization]`, project registry (`name`+`path`), shared `[[worker]]` presets | n/a (home dir) |
| Project (shared) | `<project>/.claude-manager/config.toml` | `[[session]]`, `gates`, `default_permission_mode` | **yes** — share project setup |
| Project (private) | `<project>/.claude-manager/config.local.toml` | `mixed_programming` opt-in, `mixed_max_rounds`, private `[[worker]]` | **no** — auto-added to `.claude-manager/.gitignore` |

**Load** (`config.go`): after decoding the global file, `applyProjectOverlays`
merges each project's overlay onto its `[[project]]` entry (via
`LoadProjectOverlay` → `config.toml` then `config.local.toml`, local wins),
merges overlay workers into the global registry (dedup by name), *then* runs
`applyDefaults`/`validate` on the merged result. Missing overlay → the global
inline `[[project]]` is used unchanged (additive: nothing is required in the
folder, so test fixtures and quick global setups still work).

**Save** (`SaveProjectOverlay`): splits a `ProjectConfig` into the committed
file (sessions, gates) and the private file (mixed opt-in, workers), atomic
tmp→rename, and appends `config.local.toml` to `.gitignore` (idempotent).
`app.go:UpdateConfig` folds each project whose `Path` is a real dir into its
overlay and shrinks the global `[[project]]` to a registry pointer; projects
without a writable folder keep their settings inline in the global file.

**Privacy rationale.** `mixed_programming = true` is the "my code may leave this
machine" opt-in, so it lives only in the gitignored `config.local.toml` — a
teammate cloning the repo gets sessions+gates but must opt into external workers
themselves. Workers stay global (reusable presets, and the home-dir file is
never in a repo). `MixedProgramming` is a `*bool` in `ProjectOverlay` so an
absent overlay field leaves the global value untouched.

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
Initial prompt (and every user turn) is sent via stdin using the Claude CLI
stream-json envelope: `{"type":"user","message":{"role":"user","content":"..."}}`.
The `type` **must** be `user` with a nested `{role,content}` message — the CLI
does not recognise a flat `{"type":"user_message","message":"..."}` and hangs on
stdin if it receives one. In autonomous mode (`auto_restart`/`stop_when_no_tasks`)
the manager closes stdin when the turn's `result` event arrives so the CLI exits
cleanly and the run loop can iterate; real Claude keeps the process alive on open
stdin otherwise.

### Stream-JSON Events (stdout)
Key event types to parse:
- `{"type":"system","subtype":"init",...}` — session info, model, tools, version
- `{"type":"assistant","message":{"content":[...],"usage":{...}}}` — text/tool_use with per-turn token usage
- `{"type":"result","total_cost_usd":...,"usage":{...},"modelUsage":{...}}` — final metrics. In autonomous runs, its `result` text is also scanned for the ```` ```ask-user ```` marker (see "Ask-User Questions" below) before being treated as a finished turn.
- `{"type":"stream_event",...}` — partial-message deltas (from `--include-partial-messages`); dropped silently, the full `assistant` message follows
- `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"|"allowed_warning"|"rejected",...}}` — rate limit status. Real Claude emits an informational `status:"allowed"` event on **every** session; only a rejecting status (`rejected`/`exceeded`/…) pauses/restarts the run. `allowed_warning` (with utilization) is surfaced to the UI but does not abort.

### Permission Handling
When `permission_mode != "bypassPermissions"`, Claude CLI sends permission requests via stdout and blocks waiting for response on stdin. The manager MUST:
1. Parse the permission request from stream-json
2. Check auto-approve rules (TOML config + runtime rules)
3. If no rule matches → emit to UI, set status=WaitingPermission, block goroutine
4. Wait for user response via UI → write response to stdin
5. Never ignore — session hangs forever if no response

### Auto Model Routing
When `optimization.auto_model_routing = true`, clicking ▶ on a session triggers:
1. Pre-flight analysis (haiku, one-shot) on the session's configured prompt
2. `ModelRouter.Route()` maps `estimated_complexity` → model/effort recommendation
3. Frontend shows `ModelPicker` with the recommendation
4. User can **accept** or **override** model/effort before the session starts
5. `StartSessionWithOverride(project, name, model, effort)` launches with chosen values

Routing table (`internal/optimization/routing.go`):

| Complexity | Model | Effort |
|---|---|---|
| trivial | haiku | low |
| standard | sonnet | medium |
| complex | sonnet | high |
| architectural | opus | high |

If `auto_model_routing = false` (default), clicking ▶ starts the session immediately with the model from config.

### Task Source Check
When `stop_when_no_tasks = true` and `task_source` is set, `Run()` checks the file before each iteration using `hasTasks()`. Two formats are supported (mirrors `orchestrator.py has_tasks()`):

1. **Pointer format (canonical, primary).** The file contains bare pointer lines `<source>:NN` — one open task per line, priority top-down (e.g. `ROADMAP.md:92`, `MIXED-TASKS.md:102`, `crates/x/src/lib.rs:76`). Any bare pointer line (`^\S+:\d+$` after trim; headings `#`, quotes `>`, list markers `-`/`_`/`*` are ignored) → `true`. Completing a task = deleting its line; an empty file stops the loop. No In progress/Next sections, no checkboxes — "in progress" is tracked in the master list the pointers point to.
2. **Legacy format.** Returns `true` if the file contains `"In progress:"` (a task is started) or both `"Next:"` and `"- ["` (queued tasks).

Returns `false` otherwise or if the file is missing.
The `task_source` path is resolved relative to `ProjectPath` when not absolute.

**No pending tasks → interactive fallback, not a silent no-op.** When
`hasTasks()` is `false`, `Run()` no longer just logs and flips the session back
to Idle — from the UI that looked indistinguishable from clicking ▶ and
nothing happening. Instead it emits a `system`-level log entry naming the empty
`task_source` file, then launches one CLI turn with `runOnce(ctx, true)`
(`forceInteractive`), so the user can hand it an ad-hoc task outside the plan.
`forceInteractive` does two things inside `runOnce`: it forces `autonomous =
false` regardless of `AutoRestart`/`StopWhenNoTasks` (stdin stays open after
the turn's `result`, exactly like a manually-started interactive session), and
`initialPromptText()` sends a dedicated "no pending tasks, wait for
instructions" message instead of the session's configured task-source prompt
(crash-recovery still takes priority over both). After that one `runOnce` call
returns, `Run()` unconditionally goes Idle and exits — it does **not** rejoin
the auto-restart loop, so the user must stop it manually (the Stop button) or
it exits when the CLI process does. Covered by the `no-tasks-interactive` e2e
scenario (`testdata/e2e/`, `testdata/scenarios/`, `testdata/configs/`).

**Task description resolution (TaskPanel).** Once `hasTasks()` confirms work
remains, `resolveTaskSourceDescription()` (`internal/session/session.go`)
mirrors `orchestrator.py`'s `resolve_pointer_desc()`: it takes the first bare
pointer line (`ROADMAP.md:92`) and reads the exact text of that line from the
referenced file (relative to `ProjectPath`), truncated to 200 chars — e.g. a
ROADMAP.md table row or a task-file heading. Legacy-format files fall back to
the text following `"In progress:"` or the first `"- ["` item under
`"Next:"`. The result is stored on the session (`setTaskSourceDesc`, emits
`session:task_source` only when it changes) and exposed as
`SessionState.task_source_description`. `TaskPanel.svelte` shows it under
"Now" whenever Claude hasn't yet emitted a `TodoWrite` for the new task —
`current_task` (from TodoWrite) always takes precedence once available, since
it's more specific and reflects Claude's own live breakdown. This only
resolves the *description* of the top task; it does not change which task
`hasTasks()`/Claude picks.

### AI-Generated Project Roadmap → P1 Session Bootstrap
Bridges "describe a project idea" to a running `task_source`-driven session, so
`ROADMAP.md`/`STATUS-P1.md` no longer have to be hand-authored before the Task
Source Check above has anything to consume.

**Generation** (`GenerateRoadmap(project, idea, model)`, Opus by default —
unlike pre-flight triage, roadmap quality is the main lever on every
downstream session's success): runs `analysis.RunAnalysis` with a dedicated
`RoadmapJSONSchema`/`RoadmapSystemPrompt` (`internal/analysis/schema.go`) that
decomposes a whole project idea into a dependency-ordered backlog of
session-sized tasks for a **single developer** (parallel `execution_order`
groups are for independent scaffolding only, never concurrent developers —
multi-developer, P2..PN partitioning is intentionally out of scope). Reuses
`TaskPlan`/`PlannedSubtask`/`NewPlanFromAnalysis` unchanged; the plan is
tagged `Kind: "roadmap"` (`analysis.PlanKind`) so it can never be handed to
the ad-hoc `ExecutePlan` (which would immediately fire off each backlog entry
as a live session instead of writing files — `executePlan` rejects a roadmap
plan explicitly). The idea is reviewed/edited in `PlanReview.svelte`
(`mode="roadmap"` — same subtask editor as the ad-hoc pre-flight flow, with
the single-task feasibility panel hidden and `shared_context` shown as a
"Project summary" instead).

**Materialization** (`ApproveRoadmap` → `analysis.WriteRoadmapFiles`): renders
one markdown table row per task into `<project>/ROADMAP.md` (self-descriptive
single-line rows — `resolveTaskSourceDescription` above returns exactly one
line, so each row must stand on its own) and, after re-reading the just-written
file to get real 1-based line numbers, `<project>/STATUS-P1.md` as bare
`ROADMAP.md:NN` pointers in dependency order — the exact canonical format
Task Source Check already parses, so nothing changes on the consumption side.
Refuses to touch either file if it already exists (`ErrRoadmapFilesExist`)
unless the caller passes `overwrite=true` — a hand-maintained roadmap is never
silently clobbered; `PlanReview.svelte` surfaces this as an inline "already
exists — overwrite?" banner (no `window.confirm()`).

**Bootstrap**: on a successful write, `ApproveRoadmap` upserts a `"P1"`
`SessionConfig` (Sonnet, `permission_mode` = the project's
`default_permission_mode` or `bypassPermissions` if unset — an autonomous
`auto_restart` loop with nobody watching to answer a permission prompt needs
full permissions, not `acceptEdits`, or it silently stalls on the first
disallowed `Bash` call — `use_worktree = true` — bare
`--worktree`, fresh from HEAD every run, so "one task = one session = one
worktree" holds without any prompt-level bookkeeping — `task_source =
"STATUS-P1.md"`, `stop_when_no_tasks = true`, `auto_restart = true`, and
`analysis.DefaultP1SessionPrompt`) into the project — reusing the exact
`GetConfig`→mutate→`UpdateConfig` round-trip every other project/session edit
already goes through, no new persistence path. If a `"P1"` session already
exists, only the `task_source`-related fields are forced so a user's manual
model/prompt edits survive re-generating the roadmap. `DefaultP1SessionPrompt`
is deliberately language/tool-agnostic (no build system or linter named) and
spells out a full session-start → work → session-end protocol: sync with the
remote and confirm worktree isolation before reading `STATUS-P1.md`; at the
end, run the project's strictest lint/test gates once, update docs if the
project keeps any, merge with `--no-ff`, delete the branch/worktree, and push
`main` immediately. This prompt is only ever written for a project whose
roadmap went through this generate→approve flow — a project added via
Settings' plain Projects tab (no roadmap) never has anything written to it
by this mechanism.

### CLAUDE.md Generation

Detects a project with no `CLAUDE.md` and offers to generate one, instead of
letting sessions there start with zero project context silently. The built-in
`/init` slash command can't be driven programmatically — it only runs inside
an interactive Claude Code session, there is no `claude -p "/init"` — so this
replicates its intent as a plain prompt (`analysis.ClaudeMdInitPrompt`) instead
of invoking `/init` itself.

**Detection** (`Sidebar.svelte`): checked lazily per project, on the sidebar's
project-header click (not a bulk scan of every configured project at startup).
`HasClaudeMd(projectPath)` is a pure `os.Stat` — no config lookup, since the
sidebar already has each project's path from `GetProjects`. A result of
`false` shows a dismissible inline banner ("No CLAUDE.md in this project… ")
above that project's session list; dismissal is client-side only (a `Set` in
the component), not persisted.

**Generation** (`GenerateClaudeMdSession`): mirrors the
`ApproveRoadmap`/`upsertP1Session` pattern above — a canned prompt bootstrapped
into a named, reusable session (`"Init"`) rather than a one-off CLI call, via
the same `GetConfig`→mutate→`UpdateConfig` round-trip. `upsertInitSession`
creates the session (Sonnet, the project's `default_permission_mode` or
`bypassPermissions` if unset) if absent, or refreshes just its `Prompt` if
present — a user's manual `Model`/`PermissionMode` override on "Init" survives
being re-run. `ClaudeMdInitPrompt` (`internal/analysis/claudemd.go`) follows
Anthropic's own CLAUDE.md guidance: under ~200 lines, specific/concise/
verifiable, covers tech stack + build/test/lint commands + architecture, folds
in any existing `.cursorrules`/`.clinerules`/`.github/copilot-instructions.md`/
`AGENTS.md`, and explicitly avoids inventing gotchas or business context it
can't verify — those are left for the maintainer to add by hand later. The
prompt also tells Claude to read and refine an existing `CLAUDE.md` rather than
overwrite it, so re-running "Init" later (e.g. after a roadmap-driven project
has grown) is safe.

The "Init" session runs as an ordinary interactive session (no `task_source`,
no `auto_restart`) — one turn, then it stays open on stdin exactly like any
manually-started session, so the user can ask for refinements in the same
conversation. Not covered by an e2e scenario: `GenerateClaudeMdSession` isn't
on `control.AppAPI` (same reasoning as `ApproveRoadmap`/`GenerateRoadmap`
above — Wails-only, not control-plane-dispatched); its config-mutation logic
(`upsertInitSession`) is unit-tested directly in `app_test.go` instead.

### Ad-Hoc Chat Session

The sidebar's per-project header has a 💬 button (`StartAdHocChatSession` →
`onStartChat` in `Sidebar.svelte`) for when the user just wants to talk to
Claude in a project and hand it instructions directly — no `task_source` file,
no pre-configured session, no canned prompt. Same bootstrap-a-named-session
pattern as `GenerateClaudeMdSession`/`upsertInitSession` above, but simpler:
`upsertChatSession` creates a bare `"Chat"` `SessionConfig` (no `Prompt`, no
`TaskSource`, no `AutoRestart` — `Model`/`Effort`/`PermissionMode` fall back to
the usual `applySessionDefaults`) only if one doesn't already exist; re-clicking
never overwrites a `"Chat"` session the user has since customized. An empty
`Prompt` means `initialPromptText` sends nothing on launch, so the CLI process
comes up and just waits on stdin for the user's first message — unlike the
`forceInteractive` "no pending tasks" fallback (see "Task Source Check" above),
which does send a synthetic "wait for instructions" turn because it's
reacting to an *emptied* task queue rather than a session that never had one.
Also not on `control.AppAPI` (same Wails-only reasoning as `GenerateClaudeMdSession`);
`upsertChatSession` is unit-tested directly in `app_test.go`.

### Crash Recovery
Mirrors `orchestrator.py` session state files (`.session-PN.json`).

**How it works:**
1. Before each `runOnce()`, `StateStore.Save()` writes `~/.claude-manager/state/<project>-<session>.json` with `{started_at}` (atomic: tmp → rename).
2. When the `system/init` event arrives, `StateStore.UpdateSessionID()` patches the file with `session_id`.
3. On successful task completion, `StateStore.Clear()` deletes the file.
4. On app restart, `Run()` loads the state file. If `session_id` is present, it sets `resumeSessionID` and `buildCLIArgs()` uses `--resume <id>` instead of `--session-id <uuid>`.
5. The recovery prompt is `CrashRecoveryPrompt` from config (or a built-in English default).
6. `ClearSessionState(project, name)` / the UI button is the equivalent of `--new`: deletes the state file so the next start is fresh.

**Config:** `crash_recovery = true` in `[settings]` (default: true). Per-session: `crash_recovery_prompt`.

### Rate-Limit Fallback Model
When `fallback_model_on_rate_limit = true` and `fallback_model` is set:
- On the first rate-limit hit, `Run()` sets `usingFallback = true` and `activeModel = FallbackModel`.
- `buildCLIArgs()` passes `--model <activeModel>` and **omits** `--fallback-model` (to avoid circular fallback).
- The session restarts immediately — no 5-minute wait.
- If the fallback model is also rate-limited, the standard `waitRateLimit` pause applies.

**Config:** `fallback_model_on_rate_limit = true` per session (default: false). Set `fallback_model = "haiku"`.

### Live Model Switching

Lets the user change a **running** session's model from a small dropdown in
the sidebar (under each session's name, next to the start/stop button) instead
of only choosing it before start. Real Claude CLI has no hot model swap
mid-process — `--model` is fixed at launch — so this reuses the same
`activeModel` override the rate-limit fallback above already relies on, and
picks one of two paths depending on the session's lifecycle:

- **Autonomous** (`task_source`/`auto_restart`, `Session.Autonomous()`):
  `SessionManager.SetSessionModel` just calls `Session.SetModel` (updates
  `activeModel`, leaves `Config.Model` — the persisted default — untouched)
  and returns. One CLI process already equals one task there, so the next
  task's `buildCLIArgs()` picks up the new model on its own; the task
  currently in flight is not interrupted.
- **Interactive** (one long-lived CLI process, no task boundary):
  `SetSessionModel` soft-restarts it immediately — `StopSession` (hard),
  then a private `startSessionResuming` (a `StartSessionWithOverride` twin)
  relaunches with `Params.ResumeSessionID` pre-seeded to the stopped
  session's `CLISessionID`, so `buildCLIArgs()` uses `--resume` and the
  conversation continues instead of starting over. Whatever tool call was
  in flight at that moment is interrupted — an accepted tradeoff for
  actually taking effect immediately, since there's no natural task boundary
  to wait for otherwise. `ResumeSessionID` is independent of the
  `crash_recovery` setting: it seeds `Session.resumeSessionID` directly, so
  the switch resumes the conversation even when `crash_recovery = false`;
  a crash-recovery state file, if also present, still wins if it loads
  first (same `resumeSessionID` in practice — both reflect the same
  confirmed `system/init` session id).

`SessionState.Model` (`GetSession`) reports `ActiveModel` (falling back to
`Config.Model`) so the sidebar/status bar reflect a switch immediately,
before the next `system/init` event confirms it from the CLI itself.

### Auth Error Handling (403)
`drainStderr()` detects lines containing `"403"` + `"forbidden"` / `"authenticate"` / `"unauthorized"`.
On detection, `authErrorHit` atomic is set → `runOnce()` returns `errAuthError` → `Run()` pauses 60 seconds and retries.
The crash-recovery state file is cleared on auth errors (not resumable).

### Token Optimization
- Context grows with every turn (all messages re-sent). Monitor `usage` in each `assistant` event.
- Auto-restart session when context > 75% of `contextWindow` (from init event).
- Use `--exclude-dynamic-system-prompt-sections` to share system prompt cache across sessions.
- Stagger session starts by `session_start_delay` seconds for cache warming.
- Loop detection: if same tool+input appears 3+ times in last 20 calls, alert/hint/restart.

### Optimization Reporter
`internal/optimization/reporter.go` is the read-only aggregator that the session manager uses to emit `session:context` events to the frontend. It wraps all three optimization monitors into one place:

```go
r := optimization.NewReporter(ctxMonitor, cacheTracker, loopDetector)

// Per session — called after every assistant event:
snap := r.Snapshot(sessionID)
// snap.ContextUtilization  — 0..1, fraction of context window used
// snap.CacheEfficiency      — cache_read / (input + cache_read + cache_creation)
// snap.LastLoop             — non-nil when a loop was detected

// After loop is detected and handled:
r.RecordLoop(sessionID, loopResult)
r.ClearLoop(sessionID)

// Global stats across all sessions:
g := r.GlobalReport()
```

`Snapshot` reads from `ContextMonitor.Utilization()`, `CacheTracker.SessionStats()`, and the internal loop map — all under their respective locks, no extra state.

### Sidebar Resizing
The sidebar width is controlled from `App.svelte` via a draggable 4px divider. Width is stored in a reactive variable (150–500px). The `<Sidebar>` component uses `w-full` and fills its parent container.

### Project Deletion (Two-Click Confirm)
`window.confirm()` is disabled in Wails WebView2 and always returns `false`. Project deletion uses a two-click pattern: first click arms the `✕` button (turns it red `?`, auto-cancels after 3s), second click executes `GetConfig → filter → UpdateConfig → initProjects()`.

### Session Status Flow
```
Idle → Starting → Working ⇄ WaitingPermission
                    ↓ ⇄ WaitingForUser
              RateLimited → Retrying → Working (loop)
                    ↓
              Stopping → Idle
                    ↓
                 Error
```

### Ask-User Questions (Autonomous Sessions)

A task_source/auto_restart loop resets its CLI conversation every task, so a
question Claude asks in prose and never gets answered used to be silently
lost — the next run starts fresh with no memory of it. The marker convention
below fixes that, but an autonomous session is never watched live, so a run
that just blocks on any question sits there forever with nobody to answer it
(observed live: `Lumen browser/S1` asked "S8 merged — start S9 in this same
session?" and idled in `waiting_for_user` all night). Two different fixes
apply depending on *what* is being asked:

- **Session/task boundary questions** ("should I keep working in this same
  session?") have a deterministic answer per the one-session-per-task rule —
  always no — so they are resolved instantly, without waiting for anyone.
- **Genuine external decisions** (an ambiguous requirement, a stuck
  investigation) still pause the run for a human, but with a 5-minute timeout
  that auto-picks the first listed option so the run is never stuck forever.

**Marker convention.** Every autonomous run's system prompt gets
`askUserProtocolPrompt` (`internal/session/session.go`) appended via
`--append-system-prompt`, alongside any configured `SystemPromptAppend` —
never replacing it. It teaches Claude both forms of the fenced block:
````
```ask-user
{"question": "<one sentence>", "options": ["Continue in this session", "Stop — a new session will pick up the next task"], "kind": "continue_session"}
```
````
for the session-boundary case (end the turn immediately after emitting it —
never keep working in the same reply), and the same block without `"kind"`
for a genuine decision, with the instruction that unanswered options are
tried in listed order — so list them by actual preference.

**Detection** (`ParseAskUserQuestion`, `internal/session/parser.go`): a plain
regex extracts the fenced block from the `result` event's text and decodes
the JSON into `AskUserQuestion{Question, Options, Kind}`. A missing marker or
malformed JSON returns `nil` — silently falling back to normal turn
completion, since a false positive must never hang the session forever. Only
checked when the run is autonomous (`(AutoRestart || StopWhenNoTasks) &&
!forceInteractive`, the same flag that gates closing stdin) — an interactive
session's user is already reading every reply directly.

**`Kind == KindContinueSession` (`"continue_session"`)** (`handleLine`,
`internal/session/session.go`): logs a `system`-level entry naming the
question and returns `true` (finished turn) exactly as it would without the
marker — `runOnce`'s autonomous branch still calls `closeInput()`, the CLI
process exits normally, and `Run()`'s loop proceeds to its next iteration
(fresh session, fresh `CLISessionID`). There is no pause and nothing to
answer.

**`Kind == ""` (a genuine decision)**: the session stores a
`PendingQuestion{ID, Question, Options, AskedAt}` (`ID` is generated locally,
not CLI-issued), sets status `WaitingForUser`, emits `session:question`, and
— critically — returns `false` instead of the usual `true` for a result
event. `runOnce`'s autonomous branch does **not** call `closeInput()`: stdin
stays open, so the real Claude CLI process (which already keeps a process
alive on open stdin between turns, see "Bidirectional Streaming" above)
simply waits for the next turn instead of exiting. `Run()`'s outer loop is
still blocked inside that one `runOnce` call, so no new task starts and no
`EvtTaskDone` fires prematurely. `startQuestionTimeout` arms a
`time.AfterFunc` (`Session.questionTimeout`, default 5 minutes, overridable
via `Params.QuestionTimeoutSec` — tests use a 1s override) that calls
`autoAnswerQuestion` if nobody responds in time; it re-checks the pending
question still matches by ID (a real answer may have raced it), picks
`Options[0]` (or a generic fallback if none were given), logs the auto-choice,
and answers through the same path as a human.

**Answering** (`AnswerQuestion` on `Session` and `SessionManager`, human or
timeout): clears the pending question, cancels the timeout timer, writes the
answer as a normal user turn on the same stdin, and returns status to
`Working` — this continues the *same* CLI conversation (same `CLISessionID`,
full context intact) rather than restarting a fresh run, so the answer
actually reaches the decision that prompted it. The process-exit cleanup path
in `runOnce` also stops any live timer and drops a stale `pendingQuestion` if
the process ends some other way (killed, crashed) while a question was
pending, so a later stray timer fire is a harmless no-op.

**Frontend**: `SessionState.pending_question` (`session:question` event)
drives `QuestionBanner.svelte` (rendered in `SessionView.svelte` next to
`PermissionBanner`) — one button per parsed option plus a free-text field for
anything else, both calling `AnswerQuestion(id, questionID, answer)`.
`waitingSessions` (`stores/sessions.ts`) and the sidebar/status-bar "waiting"
indicators treat `waiting_for_user` the same as `waiting_permission`. A
`continue_session` marker never reaches the frontend at all — it's fully
resolved on the backend before any event is emitted.

Not wired into the control-plane/MCP tools yet (see "cm-mcp tools" below) —
only the Wails binding exists so far.

### Mixed Programming (MIXED-TASKS.md, MP-01..08)

Claude prepares self-contained briefs; external free models write the code as
FIND/REPLACE patches; the manager applies them in an isolated git worktree, runs
blocking gates (build/lint/test), returns the exact gate output as feedback for
up to `mixed_max_rounds` (default 3), and produces a comparative quality report.
Ported from lumen-browser's «смешанное программирование» workflow.

**Opt-in & config.** Per-project `mixed_programming = true` is an explicit
privacy opt-in — briefs and verbatim code excerpts go to external endpoints that
log requests. Enabling requires non-empty `[project.gates]` and at least one
`[[worker]]`. `WorkerConfig` (`internal/config/types.go`) carries `base_url`,
`model`, `key_env` (API key read from env, never stored), `role`
(hands|quality|eyes) and quirks (`reasoning_effort`, `max_output_tokens`,
`continuation_cap`, `ascii_anchors_only`, `request_timeout_sec`).
`config.WorkerPresets()` ships `step37` and `nemotron-ultra` from the lumen bench.

**Round loop** (`internal/worker/round.go`, `RoundOrchestrator.RunTask`):
1. Create worktree + branch `mp-<brief>-<worker>-<HHMMSS>`.
2. Send the brief to the worker (`Client.Complete`, SSE, per-`key_env` semaphore,
   progressive backoff on 403/429, `finish_reason=length` continuation capped at
   `continuation_cap`).
3. Parse patches (`patch.go` — tolerant of missing `>>>END`, `===END`; rejects a
   FIND that is not verbatim-unique with an exact expected/actual diff).
4. Apply inside the worktree only (`_safe_path` analog, atomic writes).
5. Run gates (`gates.go`) — **blocking**: a non-zero exit rejects the round.
   Green → commit (`Co-Authored-By: <model>`), status `done`, worktree left for
   review. Red / rejected / parse error → the exact output is fed back verbatim
   (model-written tests are never trusted; gates are ground truth).
6. After `max_rounds` without green → status `needs_human`.

Every step emits `worker:round` / `worker:patch` / `worker:gate` / `worker:done`
through the `Emitter`, so the UI, control-plane and MCP get progress for free.
Round state + the reconstructable message list persist to
`~/.claude-manager/state/mixed-task-<id>.json` (`TaskStore`) and
`worker-<id>.json` (`Store`) for crash-resume by replay (the OpenAI API is
stateless). Task ID = `<project>/<brief>/<worker>`.

**Briefs** (`internal/analysis/brief.go`, MP-06) are generated by the preflight
mechanic (haiku/sonnet, `--json-schema`): English task text, verbatim code
excerpts with line numbers, accepted decisions (no "choose between A/B"),
typed-locals hints, patch-format instructions; persisted to SQLite
(`mixed_briefs`). For a new file, pre-create it with a `// PLACEHOLDER` anchor
and patch via FIND on it.

**Quality report** (`internal/worker/quality.go`) aggregates persisted tasks into
one `ModelQuality` per worker: tasks done/needs-human, mean rounds-to-green,
clean-patch rate (`applied / (applied + rejected)`), and defects by type (parse
errors, gate failures). Surfaced in `MixedRun.svelte`.

**UI** (`MixedRun.svelte` + `stores/workers.ts`, MP-08): the "Mixed" header
button opens a modal to pick a mixed-enabled project + worker, enter a brief and
dispatch; it shows a live activity feed, per-task round timelines with collapsible
gate output, and the quality table. Worker CRUD (with presets) and the project
privacy opt-in live in Settings → Workers.

**Testing.** `fakeworker` (`cmd/fakeworker`) is the SSE double; scenarios in
`testdata/worker-scenarios/`. Go e2e: `testdata/e2e/mixed-*.json` drive the full
loop against a seeded temp repo via the control-plane. Playwright:
`frontend/tests/mixed.spec.ts` covers the Workers tab and a dispatch→timeline→
quality flow. MCP tools: `register_mixed_brief`, `dispatch_mixed_task`,
`get_mixed_rounds`, `wait_for_worker_status`.

## Wails Bindings (app.go)

All exported methods become async JS functions via auto-generated bindings in `frontend/wailsjs/`.

| Method | Description |
|---|---|
| `GetConfig()` | Full AppConfig including Optimization settings |
| `UpdateConfig(cfg)` | Persist config to TOML, reload session manager |
| `GetProjects()` | Project list shortcut |
| `GetAutoModelRouting()` | Whether auto_model_routing is enabled |
| `GetModelRecommendation(project, name)` | Run preflight analysis, return ModelRecommendation or null |
| `RunPreflight(project, task)` | Run analyst on an ad-hoc task, persist and return draft TaskPlan |
| `ApprovePlan(plan)` | Persist an (edited) plan as approved; returns plan with store ID |
| `ExecutePlan(planID)` | Execute persisted plan (one-shot CLI per subtask, context handoff) |
| `GetPlan(planID)` | Load persisted plan with subtasks (poll during execution) |
| `GenerateRoadmap(project, idea, model)` | Decompose a project idea into a draft roadmap plan (Opus by default) |
| `ApproveRoadmap(planID, overwrite)` | Write ROADMAP.md/STATUS-P1.md into the project + bootstrap the "P1" session |
| `HasClaudeMd(projectPath)` | Whether `<projectPath>/CLAUDE.md` exists (sidebar banner check) |
| `GenerateClaudeMdSession(project)` | Bootstrap (or re-point) the "Init" session with `analysis.ClaudeMdInitPrompt` and start it |
| `StartAdHocChatSession(project)` | Bootstrap (or reuse) a plain interactive "Chat" session (no prompt/task_source) and start it |
| `RegisterMixedBrief(id, task, systemPrompt)` | Register a mixed-programming brief; returns id |
| `DispatchMixedTask(project, briefID, workerName)` | Run the round loop; blocks until done/needs_human |
| `GetMixedRounds(project)` | Persisted mixed tasks (rounds, patches, gates) for a project |
| `GetMixedQuality(project)` | Per-worker comparative quality report (ModelQuality) |
| `CancelMixedTask(id)` | Cancel a running mixed task by ID |
| `StartSession(project, name)` | Launch session with config model/effort |
| `StartSessionWithModel(project, name, model, effort)` | Launch with model/effort override |
| `SetSessionModel(id, model)` | Switch a running session's model live (see "Live Model Switching") |
| `StopSession(id, soft)` | Stop (soft=true finishes current task first) |
| `RestartSession(id)` | Hard stop + restart |
| `ResumeSession(id)` | Resume from saved CLI session ID |
| `StartProject(project)` | Start all sessions in a project |
| `StopProject(project)` | Stop all sessions in a project |
| `StopAll()` | Stop every session |
| `SendMessage(id, message)` | Write user_message to stdin |
| `RespondPermission(id, requestID, decision)` | Write permission response to stdin |
| `GetPendingPermissions()` | All sessions with pending permission requests |
| `AnswerQuestion(id, questionID, answer)` | Resolve a pending ask-user question (genuine decision), continuing the same conversation |
| `GetPendingQuestions()` | All sessions currently blocked on a genuine-decision ask-user question |
| `GetAllSessions()` | Snapshot of all session states |
| `GetSessionLog(id, offset, limit)` | Paginated log entries |
| `GetSessionMetrics(id)` | Token/cost metrics for one session |
| `GetHistory(project, limit)` | Past session runs from SQLite |
| `GetDailyCost(date)` | Cost aggregate for a date |
| `GetProjectCost(project, days)` | Cost aggregate for a project over N days |
| `GetRateLimitStatus()` | Current rate limit info |
| `ExportLog(id, entries, format)` | Save log as MD/JSON/TXT via native dialog |
| `CleanOldLogs(days)` | Delete logs older than N days from SQLite |
| `ClearSessionState(project, name)` | Delete crash-recovery state file (equivalent to --new) |
| `GetSessionState(project, name)` | Return persisted state (session_id + started_at) or nil |
| `PickDirectory(title)` | Native folder picker dialog |
| `ShowMainWindow()` | Restore window from tray |
| `MinimizeToTray()` | Hide window to system tray |
| `Notify(title, body)` | Windows toast notification |

## Build & Run

```bash
wails dev          # Dev mode (hot reload frontend)
wails build        # Production: build/bin/claude-manager.exe (~15-20 MB)
```

## Go Dependencies

```
github.com/wailsapp/wails/v2
github.com/BurntSushi/toml
modernc.org/sqlite            # pure-Go SQLite driver (no cgo/C toolchain needed)
github.com/google/uuid
git.sr.ht/~jackmordaunt/go-toast/v2
```

No DI frameworks, no ORMs. Standard library for everything else.

### Testing & Control Harness
The app is fully drivable and testable without a GUI and without spending API tokens (see PLAN.md §21):
- **`fakeclaude`** (`cmd/fakeclaude`) — a scripted Claude CLI double. Set as `claude_path`, it emits deterministic stream-json from JSON scenarios in `testdata/scenarios/`. Covers every Session Status (permission, rate limit, error, loop, context-growth).
- **`fakeworker`** (`cmd/fakeworker`) — a scripted OpenAI-compatible worker double for mixed programming (MP-07). Serves `POST /chat/completions` as SSE from JSON scenarios in `testdata/worker-scenarios/` (clean patch, broken patch → feedback, 429 storm, `finish_reason=length` continuation/loop, missing `>>>END`). Point a `[[worker]]` `base_url` at it; scenario via `-scenario` flag or `FAKEWORKER_SCENARIO`.
- **`Emitter` indirection** (`internal/control/emit.go`) — all `session:*` events flow through an `Emitter` interface, not direct `runtime.EventsEmit`. `WailsEmitter` feeds the UI; `ControlEmitter` broadcasts to the control-plane. The SessionManager takes an `Emitter` in its constructor.
- **Control-plane** (`internal/control`) — at `CM_CONTROL=1`, a loopback HTTP+WS server exposes every SessionManager method via JSON-RPC and streams events, plus `/wait` for blocking until a status/event. Additive to the GUI.
- **MCP server** (`cmd/cm-mcp`) — proxies the control-plane into agent tools (`start_session`, `send_message`, `approve_permission`, `wait_for_status`, …) so Claude can press every button.
- **E2E runner** (`internal/control/e2e_test.go`) — replays `testdata/e2e/*.json` (`do`/`wait`/`assert`) against the app + fakeclaude, deterministically.
- **Playwright** (`frontend/tests`) — clicks the real DOM and double-checks via the control-plane event stream.

Task breakdown: [HARNESS-TASKS.md](./HARNESS-TASKS.md) (H1-H6).

### GUI Testing by Claude (via cm-mcp)

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

In **PowerShell**:
```powershell
$env:CM_CONTROL=1; wails dev
```
In **bash / Git Bash**:
```bash
CM_CONTROL=1 wails dev
```

This starts the full Wails GUI **and** binds the control-plane on `http://127.0.0.1:7333`. The cm-mcp server connects to that address automatically.

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
1. User runs: CM_CONTROL=1 wails dev   (or playwright-server for headless)
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

## SQLite Tables

- `session_runs` — completed runs with cost/tokens/duration/model
- `session_logs` — log entries per run (batch insert)
- `daily_metrics` — aggregated cost/tokens per day per project
- `task_plans` — pre-flight analysis plans
- `plan_subtasks` — subtasks within plans
- `mixed_briefs` — generated mixed-programming briefs (MP-06)

## File Logging

All application events are written to **`~/.claude-manager/logs/app.log`** in append mode (never truncated between runs). The logger is initialised in `app.go:startup()` before anything else, so every event from startup to shutdown is captured.

### Implementation

- Package: `internal/logger/logger.go`
- Global variable: `logger.L` (`*slog.Logger`) — safe to use before `Init()` (defaults to stderr)
- Format: `slog.TextHandler` — human-readable key=value pairs
- Destination: file **and** stderr simultaneously (stderr is useful during `wails dev`)
- Level: `DEBUG` and above

### What is logged

| Source | Event | Level | Key fields |
|---|---|---|---|
| `app.go` | Application startup | INFO | `cfg=`, `projects=`, `claude_path=`, `auto_model_routing=` |
| `app.go` | Store open | INFO | `path=` |
| `app.go` | Shutdown | INFO | — |
| `manager.go` | Session start requested | INFO | `id=`, `path=`, `model=`, `effort=`, `permission_mode=`, `auto_restart=`, `prompt_len=` |
| `manager.go` | Session stop requested | INFO | `id=`, `soft=` |
| `manager.go` | Init event received | INFO | `id=`, `model=`, `cli_session_id=`, `tools_count=` |
| `manager.go` | Result event | INFO | `id=`, `cost_usd=`, `num_turns=`, `duration_ms=` |
| `manager.go` | Permission request | INFO | `id=`, `tool=`, `description=`, `risk=` |
| `manager.go` | Rate limit hit | WARN | `id=`, `utilization=`, `resets_at=` |
| `manager.go` | Session error event | ERROR | `id=`, `error=` |
| `manager.go` | Mixed task dispatched | INFO | `task_id=`, `worker=` |
| `manager.go` | Mixed task done | INFO | `task_id=`, `status=` |
| `manager.go` | Mixed task failed | ERROR | `task_id=`, `error=` |
| `session.go` | Claude CLI command | DEBUG | `id=`, `claude=`, `cwd=`, full `args=` |
| `session.go` | Process spawned | INFO | `id=`, `pid=` |
| `session.go` | Initial prompt sent | DEBUG | `id=`, `prompt_preview=` (first 200 chars) |
| `session.go` | Status change | INFO | `id=`, `from=`, `to=` |
| `session.go` | Stderr line from Claude | WARN | `id=`, `line=` |
| `session.go` | Process exit (ok) | INFO | `id=`, `ok=true` |
| `session.go` | Process exit (error) | ERROR | `id=`, `error=` |
| `session.go` | Run-loop error + retry | ERROR | `id=`, `error=`, `retry_delay_sec=` |
| `session.go` | Task completed | INFO | `id=`, `tasks_done=` |

### Example output

```
time=2025-05-24T10:23:44Z level=INFO  msg=startup cfg=C:\Users\user\.claude-manager\config.toml
time=2025-05-24T10:23:44Z level=INFO  msg=config.loaded projects=2 claude_path=claude auto_model_routing=false
time=2025-05-24T10:23:45Z level=INFO  msg=manager.start_session id=lumen-browser/S2 model=sonnet effort=high permission_mode=acceptEdits
time=2025-05-24T10:23:45Z level=DEBUG msg=session.launch id=lumen-browser/S2 claude=claude args="-p --verbose --input-format stream-json --output-format stream-json ..."
time=2025-05-24T10:23:45Z level=INFO  msg=session.spawned id=lumen-browser/S2 pid=19432
time=2025-05-24T10:23:45Z level=INFO  msg=session.status id=lumen-browser/S2 from=starting to=working
time=2025-05-24T10:23:46Z level=INFO  msg=session.init id=lumen-browser/S2 model=claude-sonnet-4-6 cli_session_id=abc-123
time=2025-05-24T10:23:50Z level=WARN  msg=session.stderr id=lumen-browser/S2 line="Error: some CLI error"
time=2025-05-24T10:23:50Z level=ERROR msg=session.process_exit id=lumen-browser/S2 error="exit status 1"
time=2025-05-24T10:23:50Z level=ERROR msg=session.error id=lumen-browser/S2 error="claude exited: exit status 1"
```

## Conventions

- Go: standard project layout, `internal/` for private packages
- Errors: return `error`, no panics. Log to session log buffer
- Concurrency: each session is a goroutine, communicate via channels (`inputCh`, `permissionCh`). Protect shared state with `sync.Mutex`
- Frontend: Svelte stores subscribe to Wails events via `runtime.EventsOn()`. Call Go via auto-generated bindings
- Config: TOML with sensible defaults. Zero values mean "disabled" or "unlimited" (e.g., `max_budget_usd = 0` means no limit)
- IDs: sessions identified as `"{project}/{session}"` (e.g., `"lumen/P1"`)
- No `window.confirm()` — disabled in Wails WebView2. Use two-click or custom modal patterns instead

## Communication

- **Reply language: Russian.** The user writes in Russian; match it even though code, comments, and this file stay in English.
- **Tone: direct and technical.** State what changed and why; skip preamble and trailing summaries unless asked.

## Git workflow

- Current branch is `master`; there is no enforced feature-branch policy — direct commits to `master` are the norm for this solo project. Only branch off when the user asks for isolation (e.g. a risky exploratory change).
- Commit messages: Conventional Commits style (`fix:`, `feat:`, `refactor:` — see `git log` for examples), English, imperative subject line, body explains *why* when non-obvious.
- Never `--no-verify`, `--force`, `git config`, or `git push` without the user explicitly asking — same rule this repo's own session manager enforces on the projects *it* drives (see permission handling above), applied to itself.

## Known gotchas

- **`window.confirm()` is disabled in Wails WebView2** and always returns `false` — see Conventions above; every confirmation flow in this codebase uses a two-click button or an inline banner instead.
- **`HideWindowOnClose: true`** (`main.go`) means closing the main window (✕) only hides it to the system tray — it does **not** quit the app or stop sessions. Only the tray "Quit" menu item (`runtime.Quit` → `app.shutdown` → `manager.Shutdown()` → `StopAll()`) stops every running CLI process.
- **systray must start before `wails.Run`** and is torn down via `systray.Quit()` from `app.shutdown` — see the ordering comment in `main.go`. Getting this backwards leaves an orphaned tray icon after quit.
- **Pure-Go SQLite (`modernc.org/sqlite`), not `mattn/go-sqlite3`.** Deliberate: avoids requiring a CGO/C toolchain on a machine that only has Go installed. Don't swap it for a CGO driver without a strong reason.
- **No `.gitattributes`** — line endings are whatever Git's `core.autocrlf` does locally. A `git diff`/`git add` on Windows may print `LF will be replaced by CRLF` warnings; this is expected noise, not a bug.
- **fakeclaude/testkit scenarios match by prompt keyword regex** (`internal/testkit/scenario.go:MatchScenario`), not by session name — a new e2e scenario needs a prompt substring distinct enough not to collide with an existing scenario's `match` regex (see `testdata/scenarios/scenarios_doc.md`).

## Doc-sync update matrix

Update docs **in the same change** as the code, not as a follow-up:

| Change | Update |
|---|---|
| New/changed Wails-bound method on `App` | Row in the "Wails Bindings (app.go)" table above |
| New `SessionConfig`/`ProjectConfig`/`GlobalSettings` field | `config.example.toml` + relevant PLAN.md section + this file |
| New stream-json event type or field the parser handles | "Stream-JSON Events (stdout)" section above |
| New `cm-mcp` tool | "cm-mcp tools available to Claude" table above |
| New fakeclaude/fakeworker scenario | `testdata/scenarios/scenarios_doc.md` entry describing state/feature covered and its `match` pattern |
| New app-level task breakdown item done | Corresponding checkbox/row in `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` |

## When in doubt

- **Wails binding signatures / IPC shape** — the "Wails Bindings (app.go)" table above, then `app.go` itself.
- **Session lifecycle / CLI flags** — "Bidirectional Streaming" and "CLI Launch Command" above, then `internal/session/session.go`.
- **Why a design decision was made** — "Key Design Decisions" above; if still unclear, `git log -p` on the relevant file.
- **What's left to build** — `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md`.
- **How to test without spending API tokens** — "Testing & Control Harness" section above.

If none of these answer it — ask the user, don't assume.

## Reference

Full specification: [PLAN.md](./PLAN.md) — sections 14-21 cover CLI flags, bidirectional streaming, permissions, pre-flight analysis, token metrics, optimization, and the test/control harness.

Task breakdowns: [TASKS.md](./TASKS.md) (app, TASK-01..15), [HARNESS-TASKS.md](./HARNESS-TASKS.md) (harness, H1..H6), and [MIXED-TASKS.md](./MIXED-TASKS.md) (mixed programming, MP-01..08 — включает очередь задач для сессий и стартовый промпт).

GUI test descriptions (Playwright): [GUI-TESTS.md](./GUI-TESTS.md) — ~100 test cases across all Svelte components, with status (✓ exists / ○ missing).
