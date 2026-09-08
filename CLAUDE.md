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
│   │   ├── protocol.go              # WriteProtocolFiles: install the developer-session protocol
│   │   │   └── protocol/            #   into a project (git-workflow.md, worktree-pool.sh, the two
│   │   │                            #   skills) from embedded per-project templates; never overwrites
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
│   ├── experience/                  # Experience layer (LEARN-TASKS.md LN-01..18, planned): mines
│   │   │                            #   the app's own run history into token/time savings for the
│   │   │                            #   next session. Off by default, stdlib only, no external workers.
│   │   └── transcript.go            # LN-01: Trajectory — backend-agnostic parse of Claude CLI's
│   │                                #   own JSONL transcripts (~/.claude/projects/<slug>/<id>.jsonl,
│   │                                #   CM_TRANSCRIPTS_DIR override); tool_use/tool_result stitching,
│   │                                #   incremental ReadFrom(path, offset). A second backend (LN-17,
│   │                                #   markdown logs) will produce the same Trajectory type.
│   ├── store/
│   │   ├── store.go                 # SQLite: init, CRUD for runs/logs/plans/metrics/briefs
│   │   ├── migrations.go            # CREATE TABLE statements, indexes
│   │   └── logfiles.go              # Auto-saved per-run log files in <project>/.claude-manager/logs/,
│   │                                #   shared RenderExport (md/json/txt) for auto-save + manual export
│   └── hooks/
│       └── hooks.go                 # Pre/post task hooks (shell commands)
├── frontend/src/
│   ├── App.svelte                   # Root layout: sidebar + main panel + status bar + modals
│   ├── stores/
│   │   ├── sessions.ts              # Session state, Wails event subscriptions
│   │   ├── projects.ts              # Projects state
│   │   ├── theme.ts                 # Dark/light theme toggle, localStorage persistence
│   │   ├── logSearch.ts             # Log filter store, Ctrl+F focus
│   │   ├── logView.ts               # Log rendering mode: markdown vs raw (localStorage)
│   │   └── workers.ts               # Mixed programming: worker:* events, per-project tasks/
│   │                                #   quality, register+dispatch+cancel actions (MP-08)
│   ├── components/
│   │   ├── Sidebar.svelte           # Project tree, session indicators, start/stop/delete,
│   │   │                            #   auto-routing trigger, resizable via drag handle
│   │   ├── ModelPicker.svelte       # Pre-start model selector: recommendation + override dropdowns
│   │   ├── ResumePrompt.svelte      # Unfinished previous run: continue (--resume) / start fresh
│   │   ├── LogStream.svelte         # Real-time log with color coding, autoscroll, search filter,
│   │   │                            #   "Markdown" checkbox (formatted ⇄ raw)
│   │   ├── TaskPanel.svelte         # Right of the log, two tabs — "Task": current task
│   │   │                            #   (TodoWrite), checklist, progress %, session prompt;
│   │   │                            #   "Roadmap": RoadmapTree (default for task_source sessions)
│   │   ├── RoadmapTree.svelte       # ROADMAP.md as a tree: header (progress, curated/pointer
│   │   │                            #   model, hide-done), project context, phase roots
│   │   ├── RoadmapNode.svelte       # One tree node, recursive via <svelte:self>: collapse,
│   │   │                            #   status icon, "← now"/queued marks, "+" loads detail
│   │   ├── roadmap.ts               # RoadmapView/RoadmapNode types (hand-written: the Go
│   │   │                            #   type is recursive, generated models flatten it)
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
│       ├── formatters.ts            # Log formatting, time, cost, tokens, percent;
│       │                            #   log colours are light/dark class pairs
│       ├── markdown.ts              # Dependency-free markdown → safe HTML for the log
│       │                            #   (hasMarkdown/renderMarkdown, escapes everything)
│       └── models.ts                # Model catalog: MODELS/EFFORTS + normalizeModel/modelLabel
│                                    #   (one spelling per model in every dropdown)
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
│                                    #   e2e/ (runner, incl. mixed-*.json), configs/,
│                                    #   transcripts/ (LN-01: trimmed real JSONL fixtures)
├── frontend/tests/                  # Playwright DOM specs (incl. mixed.spec.ts)
├── docs/
│   └── git-workflow.md              # How a developer session takes a task, merges and pushes:
│                                    #   p<N>- branches, worktree pool, completion invariant
├── scripts/
│   ├── worktree-pool.sh             # Persistent worktree slot per developer (.claude/worktrees/
│   │                                #   p<N>-work) — the branch survives an interrupted session
│   └── orchestrator.py              # Standalone predecessor of the session loop (reference)
├── .claude/skills/                  # Executable protocol, invoked as /cm-task-start, /cm-task-finish
├── config.example.toml
├── PLAN.md                          # Full specification (§14-21)
├── TASKS.md                         # App task breakdown (TASK-01..15)
├── HARNESS-TASKS.md                 # Test/control harness task breakdown (H1..H6)
├── MIXED-TASKS.md                   # Mixed programming task breakdown (MP-01..08, done)
└── LEARN-TASKS.md                   # Experience layer task breakdown (LN-01..18, planned)
    + LEARN-STATUS.md                #   pointer queue for the LN session
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

**Image attachments.** `InputPayload.Content` (`internal/session/session.go`) is
`any`, not a bare string, so a user turn can carry pasted images: with no
images it stays the plain-string shape (`userMessage`, unchanged, still what
`TestUserMessage_Envelope` locks in); with one or more it becomes an Anthropic
content-block array — one `{"type":"image","source":{"type":"base64",
"media_type":...,"data":...}}` block per image, followed by a trailing
`{"type":"text",...}` block if there's any text (`userMessageWithImages`).
`SendMessageWithImages(msg, images []ImageAttachment)` is `SendMessage`'s
superset (`SendMessage` now just calls it with `nil`), plumbed the same way
through `SessionManager` and `App`. `SessionInput.svelte` intercepts a
clipboard paste event only when it actually carries `image/*` items (plain
text paste is untouched), reads each via `FileReader.readAsDataURL`, and shows
removable thumbnails above the textarea until send. Not wired into the
control-plane/MCP tools — pasting a clipboard image isn't something an
automated caller does; same reasoning as the ask-user protocol above.

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
downstream session's success): runs `analysis.RunAnalysisStreaming` (not
`RunAnalysis` — see "Progress reporting" below) with a dedicated
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

**Progress reporting & recovery.** `GenerateRoadmap` is a single Wails call the
frontend awaits for the whole run — on Opus that can be 5-10+ minutes with
`RunAnalysis` (`--output-format json`), which gives the caller nothing at all
until the CLI process exits. `RunAnalysisStreaming`
(`internal/analysis/preflight.go`) requests `--output-format stream-json`
instead and reports each `assistant` event (a tool call or a text chunk,
summarized to one line by `summarizeAssistantLine`) through an `onProgress`
callback; `SessionManager.generateRoadmap` wires that callback to
`m.emit(EventNameRoadmapProgress, ...)` (`plan:roadmap_progress` — `project`,
`text`), which `Settings.svelte` shows next to the "Generating…" button so a
long run reads as "alive and doing X" instead of a frozen spinner. The
terminating `result` stream-json event carries the same wrapper shape
`ParseAnalysisOutput` already parses, so `AnalysisResult` decoding is
unchanged. `RunPreflight`'s ad-hoc single-task triage stays on the plain
`RunAnalysis`/`analyzeFn` — it's haiku-fast and doesn't need this.

Because the plan is saved to SQLite (`analysis.SavePlan`, status `draft`)
*before* `GenerateRoadmap` returns to its Wails caller, a dropped IPC response
— page reload, a suspended WebView2 during a long call, anything that loses
the awaited promise on the JS side — used to silently strand a fully-generated,
already-paid-for plan with nothing in the UI to show for it and no session ever
bootstrapped. `GetLatestDraftRoadmap(project)` looks up the newest
`Kind=roadmap, Status=draft` plan for a project via `store.ListPlans` +
`analysis.LoadPlan`, so `Settings.svelte` can offer to reopen it in
`PlanReview` instead of regenerating from scratch.

**Materialization** (`ApproveRoadmap` → `analysis.WriteRoadmapFiles`) writes
three things:

- `<project>/tasks/NN-<id>.md` — one detail file per task, holding the
  analyst's full `prompt` plus the metadata that used to be dropped on the
  floor (`estimated_tokens`, `files_to_touch`, `model`, dependencies). File
  name = zero-padded position + `slugify(subtask.ID)`, falling back to bare
  `NN.md` for a non-ASCII id.
- `<project>/ROADMAP.md` — a **navigation table only**: `| # | Task | Details |
  Depends on |`, where Task is the name plus the analyst's one-line `summary`
  (or, absent that, `firstSentence(prompt)`) and Details is a relative link to
  the task file. The full prompt deliberately does *not* go here: at 40+ tasks
  a table with 2000-character cells is unreadable, and a session would be
  paging through everyone else's tasks to find its own. Each row is budgeted
  to stay under `maxRowRunes` (200) because `resolveTaskSourceDescription`
  truncates the pointed-at line to 200 runes before the UI sees it — an
  overlong row would lose its Details link, the one part a session cannot
  reconstruct.
- `<project>/STATUS-P1.md` — bare `ROADMAP.md:NN` pointers in dependency
  order, computed by re-reading the just-written ROADMAP.md, in the exact
  canonical format Task Source Check already parses. Pointers keep pointing at
  *table rows*, not at task files, so nothing changes on the consumption side.

Refuses to touch anything (`ErrRoadmapFilesExist`) if `ROADMAP.md`,
`STATUS-P1.md`, or any `tasks/*.md` already exists, unless the caller passes
`overwrite=true` — a hand-maintained or in-flight roadmap is never silently
clobbered, and a fresh numbered set must not interleave with an older one;
`PlanReview.svelte` surfaces this as an inline "already exists — overwrite?"
banner (no `window.confirm()`). Existing roadmaps are never migrated to this
layout: `DefaultP1SessionPrompt` tells the session to follow the Details link
when the row has one and to treat the row itself as the whole task when it
doesn't, so older projects keep working untouched.

**Reading it back** (`internal/analysis/roadmapview.go`) turns a roadmap into
the `RoadmapView` tree the UI renders in TaskPanel's "Roadmap" tab. Columns are
located **by header name, not by position**, because very different roadmap
families exist in the wild and all of them must work:

| | Generated (`WriteRoadmapFiles`) | Curated (hand-maintained, e.g. lumen-browser `ROADMAP.md`) | Tracker (e.g. lumen-browser `BUGS.md`) |
|---|---|---|---|
| Columns | `# / Task / Details / Depends on` | `id / phase / parent / status / size / bugs / note / title` | `ID / Статус / Компонент / Описание` |
| Shape | flat | a real tree: phases → tasks → subtasks via `parent` | flat |
| Status source | `StatusModelPointer` — done ⇔ its pointer line is gone | `StatusModelCurated` — the roadmap's own `status` column | `StatusModelCurated` |
| Task text | `tasks/NN-*.md` | the `note` column, inline | `bugs/BUG-NNN-*.md`, linked from the id cell |

The tracker column shows why header matching has to be forgiving. Its headers
are Russian (`Статус`, `Описание` — `normalizeHeader` carries the Russian
synonyms alongside the English ones), its id cell *is* a markdown link
(`[BUG-349](bugs/BUG-349-OPEN.md)` — `plainCell` keeps the label for the name,
`linkTarget` takes the target as the detail path when no `Details` column
exists), and its statuses carry a date or a scope (`FIXED 2026-05-15`,
`WONTFIX (Phase 2+)` — `statusWord` matches the leading keyword). Miss any of
those and the panel degrades *silently* and badly: an unrecognised status
column drops the file back to the pointer model, i.e. 411 of 430 open bugs
rendered as done. A row with only an id gets its inline `Summary` from the
description column, truncated to `maxSummaryRunes` (120) — `BUG-349` alone
names nothing, but 430 full descriptions are not going into the tree payload.

A **blank line does not end a table**: hand-maintained trackers break long
tables into visual chunks, and treating that as the end of the table dropped
every row below it (BUGS.md rows 366..435 were invisible). `parseTables` keeps
the header across a blank line but still clears `inTable`, so a genuinely new
table one blank line down is re-detected from its own separator row.

The status-model split is the load-bearing part. Under the pointer model a
missing pointer means done, which is right for a roadmap this app generated and
owns. For a curated roadmap it would be catastrophically wrong: lumen-browser
has 572 tasks and its `STATUS-P1.md` holds two pointers, because that file is
*one session's queue*, not the backlog — inferring "done" would mark 570 tasks
finished. So when a `status` column exists it wins, and pointers only set
`Current` (the first pointer, what the session takes next) and `InQueue`.
Curated status vocabulary is normalized onto `done|active|blocked|pending`
(`done/fixed/closed`, `active/inprogress/wip`, `blocker/blocked/wait/wontfix`,
and `open/planned/queued/ready/opt` → pending), with a pointed-at `planned` task
shown as active — the session is on it regardless of what the column says.
`wontfix` counts as blocked, not done: those rows are deferred (`WONTFIX (Phase
N+)`) and marking them finished would inflate the progress bar.

**One status file, several roadmaps.** A queue is not required to live in one
file — lumen-browser's `STATUS-P1.md` holds nineteen `BUGS.md` pointers and one
`ROADMAP.md:620`. `parsePointers` therefore groups the pointer lines by the
file they address (in order of first appearance) and `ReadRoadmap` parses each
one; a pointer into something that isn't a table (`crates/x/src/lib.rs:76`) or
into an unreadable file contributes nothing. With more than one source,
`mergeViews` wraps each file's roots in a collapsible group node named after
the file, and `RoadmapNode.RoadmapFile` — set on *every* node — tells the panel
which file to ask for a row's text, since a line number only means something
within its own file. Only the very first pointer overall is `Current`; the rest
are `InQueue`. `view.status_model` is `"mixed"` when the sources disagree.
Taking only the first pointer's file (what it used to do) hid the queued
`ROADMAP.md` task completely.

Other invariants: an *emptied* status file means "everything done", not "no
roadmap", so a finished project still renders; a status file with no pointers
at all (the legacy `In progress:`/`Next:` format `STATUS-P4.md` still uses)
falls back to `ROADMAP.md` and renders the tree without a current marker; a
table with no header row falls back to the historical positional layout; and a
phase table whose ids nothing references is treated as a flat task list rather
than sprouting empty phase headers. `Done`/`Total` roll up through the tree, so
a parent with three finished subtasks reads 3/4.

Long-form text is **never** in the tree payload — a curated roadmap carries
kilobytes of closing notes per row, and 572 of them per refresh is a
multi-megabyte payload. The UI fetches one node on expand via
`ReadRoadmapTaskDetail` (a `tasks/NN-*.md` file) or `ReadRoadmapRowDetail` (the
row's `note`). Both go through `confinedPath`, which rejects anything escaping
the project folder — a roadmap is repo-editable, and a crafted
`../../.ssh/id_rsa` link must not turn the panel into a file reader.

Pointer-line parsing is duplicated here rather than imported from
`internal/session` (that would be an import cycle);
`TestRoadmapView_AgreesWithPointerParsing` in `internal/session` is the bridge
test that keeps the two in step.

`PlannedSubtask.Summary` is the only new analyst field. Since `plan_subtasks`
has no column for it (nor for `estimated_tokens`/`files_to_touch`/`effort`/
`use_worktree`), `LoadPlan` restores those from the plan's analysis blob via
`restorePlanningFields` — `ApproveRoadmap` renders task files from a freshly
loaded plan, so without that they'd silently come out empty. Existing values
win, so an operator edit in the Plan Review UI is never overwritten.

**Bootstrap**: on a successful write, `ApproveRoadmap` upserts a `"P1"`
`SessionConfig` (Sonnet, `permission_mode` = the project's
`default_permission_mode` or `bypassPermissions` if unset — an autonomous
`auto_restart` loop with nobody watching to answer a permission prompt needs
full permissions, not `acceptEdits`, or it silently stalls on the first
disallowed `Bash` call — `use_worktree = false` (see "Developer-Session
Protocol" below: the protocol written into the project owns the worktree),
`task_source = "STATUS-P1.md"`, `stop_when_no_tasks = true`, `auto_restart =
true`, and `analysis.DefaultP1SessionPrompt`) into the project — reusing the
exact `GetConfig`→mutate→`UpdateConfig` round-trip every other project/session
edit already goes through, no new persistence path. If a `"P1"` session already
exists, only the `task_source`-related fields and `use_worktree` are forced so
a user's manual model/prompt edits survive re-generating the roadmap.
`DefaultP1SessionPrompt` is short by design: it points at
`docs/git-workflow.md` and the two skills installed alongside the roadmap, and
restates only the two rules that must survive even if the session never opens
the doc (a task is reserved by its branch; nothing is committed directly to the
integration branch). This prompt is only ever written for a project whose
roadmap went through this generate→approve flow — a project added via
Settings' plain Projects tab (no roadmap) never has anything written to it
by this mechanism.

### Developer-Session Protocol Installed Into the Project

A queue-driven session (`task_source` + `auto_restart`) needs rules for taking,
verifying and landing a task. Those rules are written **into the project**
(`analysis.WriteProtocolFiles`, `internal/analysis/protocol.go`, templates
embedded from `internal/analysis/protocol/*.tmpl`), not into the session's
prompt:

| Installed file | What it is |
|---|---|
| `docs/git-workflow.md` | the protocol: branches, reservation, worktree pool, merge cadence, completion invariant |
| `scripts/worktree-pool.sh` | one persistent worktree slot per developer, with guards that refuse to switch a slot holding uncommitted or unmerged work |
| `.claude/skills/cm-task-start/SKILL.md` | executable session start |
| `.claude/skills/cm-task-finish/SKILL.md` | executable completion: gate → doc-sync → merge `--no-ff` → push → free slot → delete branch |

plus `.claude/worktrees/` added to the project's `.gitignore`
(`config.EnsureGitignore`) — pool slots are working copies of the repository
itself.

**The load-bearing rule is that a task's state lives in git, not in the
session.** A `p<N>-<task>` branch that exists means the task is taken and
started; a session killed by a rate limit, a 403 or an app restart leaves that
branch behind, and the next session finds it with `git branch` and continues it.
Without that, an interrupted task is silently re-implemented from scratch —
observed in this repo on 2026-09-08: the same task was implemented seven times
in seven anonymous worktrees, four of them to a green committed state, none
reaching `master`, for ~$12. That is also why `use_worktree` is forced **off**
for such a session (`upsertP1Session`): the manager's bare `--worktree` makes a
fresh anonymous worktree from HEAD on every process start, which is exactly the
mechanism that loses the previous attempt.

**Why files rather than a longer prompt.** A prompt is invisible to git,
unversioned, unreviewable, cannot be improved by the sessions working under it,
and is silently truncated by nobody's error message. `DefaultP1SessionPrompt` is
therefore one paragraph pointing at `docs/git-workflow.md`; the protocol is the
repository's, and the app never touches it again.

**Never overwrites.** Every file is skipped if it already exists — a project may
have its own protocol (lumen-browser does, with different skill names), and its
version is the authority. A second call therefore writes nothing.

**Per-project rendering.** `ProtocolParams` carries the queue file (the
session's `task_source`), the integration branch (`gitutil.MainBranch`: what
`origin/HEAD` says, else the checked-out branch, else whichever of main/master
exists, else `main`) and the project's `Gates`. With gates configured the
finish skill lists the real commands; without them it describes what a gate must
be and deliberately names no build system — the app writes protocols into
projects whose language it does not know.

**Two entry points.** `ApproveRoadmap` installs it in the same step that writes
`ROADMAP.md`/`STATUS-P1.md`, because a queue without rules for working through
it is the setup that re-implements its own tasks. `InstallSessionProtocol
(project)` is the retrofit path for a project that predates this or whose queue
was written by hand.

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
1. Before each `runOnce()`, `StateStore.Save()` writes `~/.claude-manager/state/<project>-<session>.json` with `{started_at, task}` (atomic: tmp → rename). `task` is the resolved task-source description of the run, so the resume prompt below can name what was left unfinished.
2. When the `system/init` event arrives, `StateStore.UpdateSessionID()` patches the file with `session_id` (loads → patches → saves, so `task` survives).
3. On successful task completion, `StateStore.Clear()` deletes the file.
4. On app restart, `Run()` loads the state file. If `session_id` is present, it sets `resumeSessionID` and `buildCLIArgs()` uses `--resume <id>` instead of `--session-id <uuid>`.
5. The recovery prompt is `CrashRecoveryPrompt` from config, or a built-in default that mirrors `orchestrator.py`'s: run `git status`, read the session's `task_source` file, reconcile that with the resumed conversation history to work out what is already done, then continue — and it names the interrupted task when one is known. The model's own last message is not treated as evidence of committed work.
6. `ClearSessionState(project, name)` / the UI button is the equivalent of `--new`: deletes the state file so the next start is fresh.

**"Was the previous session finished?" is decided exactly as in `orchestrator.py`:** a
state file that still carries a `session_id` means *no*. The orchestrator clears
its `.session-PN.json` only on `exit_code == 0`; this app clears it only in
`Run()`'s `default:` branch — the one taken when `runOnce` returns without error.
Every other exit (stop, ctx cancel, app shutdown, crash, error retry path) leaves
the file behind, and a file whose `session_id` was never filled in (crash before
the first `init` event) is discarded as unresumable, again as the orchestrator does.

**The choice is the user's, not the manager's** (Sidebar → `ResumePrompt.svelte`).
Silently resuming was wrong in both directions: it dragged a possibly-derailed
conversation into a new run, and it gave no way to say "drop it, start over"
without hunting for the state file. So clicking ▶ on an idle session first calls
`GetSessionState`; if it reports an unfinished run the `ResumePrompt` modal shows
the interrupted task, when it started and the CLI session id, and offers
**Continue** (start as-is → `--resume`), **Start fresh** (`ClearSessionState`,
then start) or **Cancel**. Idle sessions with such a state file also carry a ⏸
marker in the sidebar, refreshed on every status change (the state file appears
when a run is interrupted and is deleted when a task completes cleanly, so no
polling is needed). Bulk paths — the project-level "Start all", the control-plane
and MCP `start_session` — are unchanged and still auto-resume: there is nobody
there to ask.

**Config:** `crash_recovery = true` in `[settings]` (default: true). Per-session: `crash_recovery_prompt`. With `crash_recovery = false` no state files are written at all, so no prompt ever appears.

### Rate-Limit Fallback Model
When `fallback_model_on_rate_limit = true` and `fallback_model` is set:
- On the first rate-limit hit, `Run()` sets `usingFallback = true` and `activeModel = FallbackModel`.
- `buildCLIArgs()` passes `--model <activeModel>` and **omits** `--fallback-model` (to avoid circular fallback).
- The session restarts immediately — no 5-minute wait.
- If the fallback model is also rate-limited, the standard `waitRateLimit` pause applies.

**Config:** `fallback_model_on_rate_limit = true` per session (default: false). Set `fallback_model = "haiku"`.

### Automatic Log Saving

Every finished run auto-saves its log as markdown into
`<project>/.claude-manager/logs/`, independent of the SQLite `session_logs`
history and of the on-screen buffer ("Clear log" in `SessionView` only empties
that buffer, never touches disk or SQLite). This is what makes a completed
autonomous task's log durable without the user remembering to click
"Export log" — in an `auto_restart` loop each `runOnce` is one task, so this
produces one file per completed task; for an interactive session it produces
one file when the whole process stops.

**Hook point** (`internal/session/manager.go:finishRun`): this is the single
choke point that already finalizes every run regardless of how it ended —
`"completed"` (autonomous `EvtTaskDone`), `"error"` (`EvtError`), or
`"stopped"` (the run loop exiting to `StatusIdle`) — and already flushes
`ms.pendingLogs` to SQLite via `InsertLogs`. Right after that flush, if there
were any log entries, a `store.SaveSessionLogFile(ms.session.ProjectPath,
ms.session.ID, logs)` call runs in its own goroutine (fire-and-forget,
`logger.Recover`-guarded) so a filesystem failure can never affect the run's
own completed/error/stopped status — only a log line either way.

**Rendering** (`internal/store/logfiles.go:RenderExport`): the exact same
function backs both this automatic save (always `"md"`) and the manual
"Export log" button (`app.go:ExportLog`, any of md/json/txt) — moved out of
`app.go` so both call sites render identically; there is no separate
auto-save-only formatter to keep in sync.

**File layout**: `SaveSessionLogFile` writes atomically (temp file + rename)
to `<project>/.claude-manager/logs/<session>-<timestamp>.md`, creating the
`logs/` directory on first use and gitignoring it via
`config.EnsureGitignore` — these are local run artifacts, not something to
commit next to the shared `config.toml` in the same folder. A no-op (empty
path, nil error) when there were no log entries, or when the session has no
`ProjectPath` (e.g. a config that never got a real project folder).

**Managing saved files** (Settings → Projects, per-project panel): `GetProjectLogFiles`
lists `store.LogFileInfo` (name/path/size/mod_time) so the UI can show a
count and total size; `ClearProjectLogs` deletes every file under that
project's `logs/` dir (`store.ClearProjectLogFiles`) *and* the project's
`session_logs` rows in SQLite (`store.DeleteLogsForProject`) — but leaves
`session_runs` rows alone, so History/Dashboard still show past runs, just
without their log bodies. Two-click confirm button (`window.confirm()` is
disabled in Wails WebView2 — see Conventions).

### Tokens vs Dollars in the Usage Readouts

Every usage figure is available in two units and the UI leads with **tokens**
(`frontend/src/stores/units.ts`, `costUnit`, persisted in localStorage like the
theme and the log's markdown switch). On a subscription the dollar number is an
API price-list valuation of something already paid for, while the rate limit
meters tokens — "how much more can I get done today" is a token question.
Dollars stay one click away, because they are still the right unit for
comparing models (what `ModelRouter`/LN-13 reason about) and for API-key
billing.

**The total always includes cache traffic** (`tokenSplit` /`totalTokens`,
`frontend/src/lib/formatters.ts`): input + output + cache read + cache
creation. Cache reads usually dominate the volume, so a total that ignored them
would understate a run by an order of magnitude — and since a cache read costs
roughly a tenth of fresh input, two equal totals can differ several-fold in
real spend. That is why the split is never hidden: it is in the tooltip
everywhere the total is shown (`tokenSplitLabel`), and spelled out inline on
`SessionCard`.

`tokenSplit` accepts both the snake_case shape (`SessionState`,
`SessionMetrics`, `DailyTokens`) and the Go-exported PascalCase one
(`store.SessionRun` rows from `GetHistory`) — the dashboard sums run rows while
the sidebar sums live session state, and a helper that understood only one of
them would silently report zero on half the screens (`frontend/tests/tokens.spec.ts`).

`daily_metrics` gained `total_cache_read_tokens` / `total_cache_creation_tokens`
for this (additive `ALTER TABLE`, same tolerate-duplicate-column pattern as
`task_plans.kind`). `GetDailyTokens` / `GetProjectTokens` read the *same rows*
as `GetDailyCost` / `GetProjectCost`, so the two units shown side by side can
never describe different sets of runs — `TestGetDailyTokensMatchesGetDailyCost`
locks that in.

### Markdown in the Log

Claude writes markdown — tables, headings, checklists, fenced code — and the
log used to show it as raw source in a monospace column, where a comparison
table is a wall of pipes. `LogStream` now renders it, with a **Markdown**
checkbox in the log's toolbar (next to the search box) to fall back to the
verbatim text; the choice is global and persisted (`stores/logView.ts`,
localStorage), because it is a reading preference, not per-session state.

**Own renderer, no dependency** (`frontend/src/lib/markdown.ts`). The output is
injected with `{@html}`, so every tag has to be one this file emitted:
`renderInline` pulls code spans out first, escapes everything that is left, and
only then re-inserts its own markup — safe by construction rather than by a
sanitizer pass. URLs are scheme-checked (`http(s)`, `mailto`, relative only), so
a `[click](javascript:…)` link degrades to its label. Supported: fenced code,
ATX headings, rules, blockquotes, nested/ordered/task lists, pipe tables with
alignment, soft line breaks, bold/italic/strike/inline-code/links/bare URLs.
`marked`/`markdown-it` would have been a runtime dependency plus a sanitizer for
a renderer this small, in an app that otherwise ships zero frontend deps.

**Two tiers of detection, split by who wrote the message.** Prose levels
(`text`, `thinking`, `result`, `user`, plus unset) format on any single signal
`hasMarkdown` finds — Claude's own writing, where markup is intentional.
Everything else, tool output above all, needs `hasStrongMarkdown` (a table, a
fence, a heading — or two different kinds of markup in one message) *and* must
not trip `looksLikeMachineOutput` (Read's numbered lines, a diff, a git
listing, a mostly-indented body). Those two guards are the difference between
formatting a `.md` file someone `cat`-ed and mangling `cargo build` output,
where column alignment is the content.

The thresholds come from measuring the real corpus (46 313 rows of
`~/.claude-manager/history.db`): `result` 85.5 % of rows carry markup, `text`
43.8 %, `tool_result` 40.8 %, everything else under 4 %. Of `tool_result`,
17.3 % clears the strong bar and 8 points of that are machine output the
markers above catch. What survives is roughly the `cat SKILL.md` / `tail
docs/tasks/*.md` population — plus a residue that no content-based rule can
separate (`git status --short --branch` really does start a line with
`## main`), which is what the per-entry button below is for.

**The per-entry `M` button** sits next to the ＋/− toggle on non-prose rows that
carry markup, lit when that row is being rendered. It flips *that one row* —
back to raw when the guess was wrong, or into markdown when the machine-output
guard held it back. Deliberately not persisted: unlike the global checkbox this
is a per-glance decision, and the log buffer it keys off (`seq`) doesn't
outlive the session anyway. Toggling it pins the row's collapse state, because
that state otherwise follows the markdown decision — switching back to raw
would fold the row to its first line, so the click would read as "hid my entry"
instead of "unformatted it".

**Markdown entries default to expanded.** The collapse-to-first-line rule for
long entries (see `COLLAPSE_CHARS`) is exactly wrong for a table or a code
block, which is the part that needed formatting. The default open state is
decided by whether the *message* is markdown, deliberately independent of the
checkbox — flipping raw/formatted changes only the presentation and never
collapses a row under the user. Explicit clicks are still remembered per `seq`
(`overrides`), and rendered HTML is memoized per entry so a long log is not
re-parsed on every keystroke in the filter box.

**Every log colour is a light/dark pair** (`logEntryColor`,
`frontend/src/lib/formatters.ts`). The level palette was picked against the
dark background, so `text-amber-300` (a `user` entry), `text-sky-400` (a read
tool) or the `status-*` tokens sit at ~2:1 contrast on the light theme's white
panel — legible in dark mode, washed out in light. Each level therefore returns
a darker base class plus the original as a `dark:` variant; only the
theme-derived tokens (`text-text-muted` / `-dim`) need no pair, since they
already follow the CSS vars. The link blue inside `.md-body` is set the same
way, per theme. `frontend/tests/formatters.spec.ts` asserts the invariant
directly — no bare 300/400 tint or `status-*` token as a light-theme base — so
a new level can't quietly reintroduce it.

Covered by `frontend/tests/markdown.spec.ts` (the renderer, incl. the escaping
cases) and `frontend/tests/log-markdown.spec.ts` (the DOM: toggle, expansion,
persistence) — GUI-TESTS.md LS-11..16.

**`.md-body` is a global style, not LogStream's.** The rules above live in
`frontend/src/style.css`, not a component `<style>` block — `{@html}`-injected
nodes never get Svelte's scoping class, so the selectors had to be global
(`:global(...)`) either way, and a shared definition means every component
that renders analyst/Claude-written prose gets the same look for free.
`PlanReview.svelte`'s roadmap "Project summary" (`shared_context`, always
analyst prose — no `hasMarkdown` gate needed, unlike LogStream's tool-output
rows) renders through the same `renderMarkdown` + `.md-body` pair.

### Live Model Switching

Lets the user change a **running** session's model from a small dropdown in
the sidebar (under each session's name, next to the start/stop button) instead
of only choosing it before start. Real Claude CLI has no hot model swap
mid-process — `--model` is fixed at launch — so this reuses the same
`activeModel` override the rate-limit fallback above already relies on, and
picks one of two paths depending on the session's lifecycle:

- **Autonomous** (`task_source`/`auto_restart`, `Session.Autonomous()`):
  `SessionManager.SetSessionModel` just calls `Session.SetModel` (updates the
  live `activeModel` override) and returns. One CLI process already equals one
  task there, so the next task's `buildCLIArgs()` picks up the new model on
  its own; the task currently in flight is not interrupted.
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

**The choice is remembered, not just applied.** `activeModel` alone dies with
the `Session` object, so a switch used to silently revert to the config value
the next time the session was started (and always after an app restart) —
the sidebar then showed a model the user never chose. `app.go` therefore wraps
both entry points, `SetSessionModel` and `StartSessionWithModel` (the
`ModelPicker` override), with `rememberSessionModel`: it folds the chosen
model — and the effort, when the caller supplied one — into that session's
`SessionConfig` through the same `GetConfig`→mutate→`UpdateConfig` round-trip
every other config edit uses, so it lands in the project overlay next to the
session's other settings. Persisting runs *before* `manager.SetSessionModel`,
because `findConfig` hands out a pointer straight into the live config — the
manager's own "session never started" branch would otherwise have already
written the new model into the struct `setSessionModelInConfig` compares
against, and the write to disk would be skipped as a no-op. A failed save is
logged (`app.remember_session_model`) rather than returned: the switch itself
already happened, and surfacing an error for it would report the opposite of
what the user just watched.

**One spelling per model** (`frontend/src/lib/models.ts`). Two vocabularies
meet in the UI: the aliases passed to `--model` (`haiku`/`sonnet`/`opus`; Fable
has no alias, so `claude-fable-5` is its canonical value) and the resolved ids
the CLI reports back in `system/init` (`claude-sonnet-5`,
`claude-haiku-4-5-20251001`, a pinned id someone typed into `config.toml`).
The sidebar appended the latter as an extra `<option>`, so one dropdown listed
both `sonnet` and `claude-sonnet-5`. `normalizeModel` folds any spelling onto
the canonical value (substring match on the family name; an unrecognised model
is passed through untouched so a custom id still round-trips) and `modelLabel`
renders it — every model `<select>` in the app (sidebar, `ModelPicker`,
`Settings`, `PlanReview`) is now generated from the shared `MODELS`/`EFFORTS`
lists rather than hand-written `<option>` tags. Exact resolved ids are still
shown where the *version* is the point: the sidebar select's tooltip and
`SessionCard`'s "Model:" line.

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

### Background-Task Warning (Autonomous Sessions)

`backgroundTaskWarningPrompt` (`internal/session/session.go`) is appended
alongside `askUserProtocolPrompt` for every autonomous run — same
`--append-system-prompt` call, same `autonomous` gate. It exists because a
CLI process's own `run_in_background` Bash tool (spawn a long command, keep
working, get told later when it finishes) silently cannot work in this
session model: the manager closes stdin as soon as it sees the turn's
`result` event (see "Bidirectional Streaming"), which kills the CLI process
— and any `run_in_background` child with it — before the notification the
model is waiting for can ever be delivered. Observed live: a task-source
session launched an 842-case test run with `run_in_background`, said it
would "wait for the automatic notification", ended its turn, got killed,
and the next auto-restart iteration inherited a crash-recovery prompt and
repeated the same dead-end pattern — no test run ever completed. The prompt
tells the model to either run the command in the foreground within the
current turn (picking a timeout that fits, polling its output file itself
with short waits if needed) or, if it genuinely can't finish in one turn,
persist progress to a file the *next* session can pick up — never end a
turn assuming background work will still be running or will be reported
back later.

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
| `GenerateRoadmap(project, idea, model)` | Decompose a project idea into a draft roadmap plan (Opus by default); streams `plan:roadmap_progress` while running |
| `GetLatestDraftRoadmap(project)` | Recover the most recent unapproved roadmap plan for a project — e.g. after a `GenerateRoadmap` response never reached the frontend — without re-running the analyst |
| `ApproveRoadmap(planID, overwrite)` | Write ROADMAP.md/STATUS-P1.md into the project + bootstrap the "P1" session |
| `GetSessionRoadmap(project, session)` | Roadmap tree for the TaskPanel "Roadmap" tab (tasks + done/current/pending, one group node per pointed-at roadmap file); nil when the session has no task source or the project has no roadmap |
| `GetRoadmapTaskDetail(project, relPath)` | Body of one `tasks/NN-*.md` file (path confined to the project folder) |
| `GetRoadmapRowDetail(project, roadmapFile, line)` | Long-form text of one roadmap row (its `note` column, else the raw row) for roadmaps that keep descriptions inline |
| `InstallSessionProtocol(project)` | Write the developer-session protocol (`docs/git-workflow.md`, `scripts/worktree-pool.sh`, the two skills) into an existing project; returns the files actually created, never overwrites |
| `HasClaudeMd(projectPath)` | Whether `<projectPath>/CLAUDE.md` exists (sidebar banner check) |
| `GenerateClaudeMdSession(project)` | Bootstrap (or re-point) the "Init" session with `analysis.ClaudeMdInitPrompt` and start it |
| `StartAdHocChatSession(project)` | Bootstrap (or reuse) a plain interactive "Chat" session (no prompt/task_source) and start it |
| `RegisterMixedBrief(id, task, systemPrompt)` | Register a mixed-programming brief; returns id |
| `DispatchMixedTask(project, briefID, workerName)` | Run the round loop; blocks until done/needs_human |
| `GetMixedRounds(project)` | Persisted mixed tasks (rounds, patches, gates) for a project |
| `GetMixedQuality(project)` | Per-worker comparative quality report (ModelQuality) |
| `CancelMixedTask(id)` | Cancel a running mixed task by ID |
| `StartSession(project, name)` | Launch session with config model/effort |
| `StartSessionWithModel(project, name, model, effort)` | Launch with model/effort override; the override is remembered as the session's default |
| `SetSessionModel(id, model)` | Switch a running session's model live and remember it (see "Live Model Switching") |
| `StopSession(id, soft)` | Stop (soft=true finishes current task first) |
| `RestartSession(id)` | Hard stop + restart |
| `ResumeSession(id)` | Resume from saved CLI session ID |
| `StartProject(project)` | Start all sessions in a project |
| `StopProject(project)` | Stop all sessions in a project |
| `StopAll()` | Stop every session |
| `SendMessage(id, message)` | Write user_message to stdin |
| `SendMessageWithImages(id, message, images)` | Write a user turn with pasted image attachments (`session.ImageAttachment{media_type, data_base64}`) as an Anthropic content-block array |
| `RespondPermission(id, requestID, decision)` | Write permission response to stdin |
| `GetPendingPermissions()` | All sessions with pending permission requests |
| `AnswerQuestion(id, questionID, answer)` | Resolve a pending ask-user question (genuine decision), continuing the same conversation |
| `GetPendingQuestions()` | All sessions currently blocked on a genuine-decision ask-user question |
| `GetAllSessions()` | Snapshot of all session states |
| `GetSessionLog(id, offset, limit)` | Paginated log entries |
| `GetSessionMetrics(id)` | Token/cost metrics for one session |
| `GetHistory(project, limit)` | Past session runs from SQLite |
| `GetDailyCost(date)` | Cost aggregate for a date |
| `GetDailyTokens(date)` | Token volume for a date (input/output/cache split + total), from the same `daily_metrics` rows as `GetDailyCost` |
| `GetProjectCost(project, days)` | Cost aggregate for a project over N days |
| `GetProjectTokens(project, days)` | Token volume for a project over N days — the token twin of `GetProjectCost` |
| `GetRateLimitStatus()` | Current rate limit info |
| `ExportLog(id, entries, format)` | Save log as MD/JSON/TXT via native dialog |
| `CleanOldLogs(days)` | Delete logs older than N days from SQLite |
| `GetProjectLogFiles(project)` | List auto-saved log files under `<project>/.claude-manager/logs/` (name/size/mod_time) |
| `ClearProjectLogs(project)` | Delete a project's saved log files + their `session_logs` SQLite rows (keeps `session_runs`) |
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
- `daily_metrics` — aggregated cost/tokens per day per project (input, output, **cache read, cache creation**)
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

## Roles and session start

A **developer session** is one driven by a task queue (`task_source` +
`auto_restart` in the manager). It follows the protocol in
[`docs/git-workflow.md`](./docs/git-workflow.md) — not a protocol pasted into its
prompt, which is why the prompt is one line.

| Session | Queue (`task_source`) | Task specs | Branch prefix | Pool slot |
|---|---|---|---|---|
| `Программист 1` | `LEARN-STATUS.md` | `LEARN-TASKS.md` (LN-01..18) | `p1-` | `p1-work` |

Read the task file's own rules before starting — each breakdown carries
invariants that are not repeated here (`LEARN-TASKS.md` §Инварианты: everything
new is off by default, a prompt must stay byte-identical with its flags off,
nothing is written into a user's repository without approval in the UI).

**Session start:**

1. `git pull origin master` — before reading any `*-STATUS.md` or creating a
   branch.
2. `git branch -a`. **A `p<N>-…` branch that already exists is your own
   interrupted task — continue it**, do not take a new one. This is the recovery
   mechanism: it works regardless of how the previous run died (rate limit, 403,
   app restart), because the state lives in git rather than in the manager.
3. Otherwise take the **top** pointer line of your `*-STATUS.md` whose
   dependencies are `✓ DONE`, mark it `● IN PROGRESS` in its task file, and
   occupy your pool slot:
   `cd "$(bash scripts/worktree-pool.sh p<N>-work p<N>-<task> | tail -1)"`.
4. **Confirm you are not working in the root checkout.** The manager starts the
   session in the repo root, which holds `master`; `git branch --show-current`
   must print your `p<N>-…` branch before you edit anything.

One task = one session. Use the skills rather than running the protocol by hand:

| Skill | When |
|---|---|
| `/cm-task-start <task>` | starting a task — **explicit `/` invocation only** |
| `/cm-task-finish [branch]` | task ready: gate → doc-sync → merge `--no-ff` → push → free the slot |

Ad-hoc sessions (`Chat`, `Init`) have no queue and are not covered by this — they
work wherever the user points them.

## Git workflow

Full protocol, worktree pool, completion invariant —
[`docs/git-workflow.md`](./docs/git-workflow.md).

- **Direct commits to `master` are forbidden for developer sessions.** Work
  happens in `p<N>-<task>` branches (developer-number prefix mandatory), merged
  with `--no-ff`. The branch itself is the task reservation.
- **Every session works in its own pool slot** — `.claude/worktrees/p<N>-work`,
  persistent, one per developer, taken via `scripts/worktree-pool.sh`. The
  manager's own `use_worktree` must be `false` for such a session: it would make
  a second, anonymous worktree from HEAD on every process start, and unmerged
  work would be lost on the next restart (this cost ~$12 and seven duplicate
  implementations of LN-01 on 2026-09-08 — see the doc).
- **Merge and push after every commit**, not at the end of the task: gate →
  commit → `git merge --no-ff` into `master` → `git push origin master`.
  Unpushed work does not exist for the next session.
- Commit messages: Conventional Commits style (`fix:`, `feat:`, `refactor:` —
  see `git log` for examples), English, imperative subject line, body explains
  *why* when non-obvious.
- **Push boundary.** A developer session pushes exactly two things without
  asking: its own `p<N>-…` branch, and `master` after a green gate and a
  `--no-ff` merge. Any other push — a different branch, a tag, a force-push, a
  push from an ad-hoc session — still requires the user to ask for it.
- Never `--no-verify`, `--force`, or `git config`. Never rewrite published
  history.

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
| New app-level task breakdown item done | Corresponding checkbox/row in `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md` (+ delete its pointer line from the matching `*-STATUS.md` queue) |
| New package or otherwise notable file under `internal/` | The architecture tree at the top of this file |
| Change to how a developer session takes/finishes a task | `docs/git-workflow.md` + the two skills under `.claude/skills/` — never a second copy of the steps in this file |

## When in doubt

- **Wails binding signatures / IPC shape** — the "Wails Bindings (app.go)" table above, then `app.go` itself.
- **Session lifecycle / CLI flags** — "Bidirectional Streaming" and "CLI Launch Command" above, then `internal/session/session.go`.
- **Why a design decision was made** — "Key Design Decisions" above; if still unclear, `git log -p` on the relevant file.
- **What's left to build** — `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md`.
- **How to test without spending API tokens** — "Testing & Control Harness" section above.

If none of these answer it — ask the user, don't assume.

## Reference

Full specification: [PLAN.md](./PLAN.md) — sections 14-21 cover CLI flags, bidirectional streaming, permissions, pre-flight analysis, token metrics, optimization, and the test/control harness.

Task breakdowns: [TASKS.md](./TASKS.md) (app, TASK-01..15), [HARNESS-TASKS.md](./HARNESS-TASKS.md) (harness, H1..H6), [MIXED-TASKS.md](./MIXED-TASKS.md) (mixed programming, MP-01..08 — включает очередь задач для сессий и стартовый промпт), and [LEARN-TASKS.md](./LEARN-TASKS.md) (experience layer — transcript mining, permission allowlists, context primer, project journal, distilled skills with measured effect; LN-01..18, planned; queue in [LEARN-STATUS.md](./LEARN-STATUS.md)).

GUI test descriptions (Playwright): [GUI-TESTS.md](./GUI-TESTS.md) — ~100 test cases across all Svelte components, with status (✓ exists / ○ missing).
