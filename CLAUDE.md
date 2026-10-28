# Claude Session Manager

Desktop app (Windows) to launch, monitor, and control parallel Claude Code CLI sessions across multiple projects.

## Documentation map

| Doc | Covers |
|---|---|
| [docs/architecture.md](./docs/architecture.md) | Tech stack, package/component tree, the Wails-bound method table (`app.go`), SQLite schema, file logging, build commands |
| [docs/design-decisions.md](./docs/design-decisions.md) | The *why* behind every non-obvious choice: config layering, bidirectional streaming, permissions, crash recovery, context handoff, the mixed-programming and experience-layer subsystems, and everything else under "Key Design Decisions" |
| [docs/testing-harness.md](./docs/testing-harness.md) | fakeclaude/fakeworker, the control-plane, `cm-mcp` — how to test without spending API tokens, and how Claude itself drives the running app for GUI testing |
| [docs/git-workflow.md](./docs/git-workflow.md) | Full developer-session protocol: branches, worktree pool, merge cadence, completion invariant |
| [docs/change-acceptance.md](./docs/change-acceptance.md) | How a change affecting speed, tokens or quality of sessions is accepted: the A/B stand, baseline (n≥3 runs of T5), acceptance card and threshold, hypothesis queue H1–H7, decision log |
| [PLAN.md](./PLAN.md) | Full specification — §14-21 cover CLI flags, bidirectional streaming, permissions, pre-flight analysis, token metrics, optimization, and the test/control harness |
| [TASKS.md](./TASKS.md) | App task breakdown (TASK-01..15) |
| [HARNESS-TASKS.md](./HARNESS-TASKS.md) | Test/control harness task breakdown (H1..H6) |
| [MIXED-TASKS.md](./MIXED-TASKS.md) | Mixed programming task breakdown (MP-01..08) |
| [LEARN-TASKS.md](./LEARN-TASKS.md) | Experience layer task breakdown (LN-01..29 done; LN-30..41: why the skill autopilot rejects everything, rules learned from recurring errors, applied by the autopilot via the system prompt); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [VIEW-TASKS.md](./VIEW-TASKS.md) | Hermes-style session feed in `LogStream` (UI-01..06); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [RUNTIME-TASKS.md](./RUNTIME-TASKS.md) | Common runtime event format + `Runtime` interface (RT-01..08); queue in [STATUS-P1.md](./STATUS-P1.md). Its RT-01 creates `docs/runtimes.md` — how Claude/Hermes streams map to `ParsedEvent` |
| [MCP-TASKS.md](./MCP-TASKS.md) | `cm-mcp` as a public MCP server with full GUI parity for third-party agents (MCP-01..18 done; audit fixes MCP-19..23); queue in [STATUS-P1.md](./STATUS-P1.md). Its MCP-01 creates `docs/mcp.md` — the agent-facing guide |
| [WT-TASKS.md](./WT-TASKS.md) | Session work folder: a persistent pool slot instead of a bare `claude --worktree` (WT-01..03); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [QUEUE-TASKS.md](./QUEUE-TASKS.md) | Queue panel sees every queued task: tasks read from `## <ID>:` headings when a task file has no status table, and a refresh when the queue/task files change on disk (QV-01..03); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [CTX-TASKS.md](./CTX-TASKS.md) | Context fill of a session = the last model call, not the sum over calls: the card's context bar and the `context_handoff` threshold (CTX-01); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [COST-TASKS.md](./COST-TASKS.md) | Cheaper waiting, journal and long interactive runs; honest measurement: blocking wait instead of `sleep` polls in the autonomous prompt, `journal_model` (haiku by default), the context budget as a question in interactive runs, `measure.py` counting subagents and compound commands (COST-01..04); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [WASTE-TASKS.md](./WASTE-TASKS.md) | Waste: useless *successful* work mined from `action_signatures` (repeat reads, empty searches, whole-file reads, needless reruns), the limit window and time as the unit instead of dollars, learned rules enforced as a `PreToolUse` hook instead of prompt text, loops remembered across sessions, blocking gates for Claude sessions; then reference cases mined from finished tasks so configurations can be compared on the same work, repo-scanned task suggestions, one "waiting for you" screen across projects, learned lessons exported as files, scheduled gate runs that file a task, quality labels and multi-configuration benchmark runs (WS-01..12); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [FIX-TASKS.md](./FIX-TASKS.md) | Bugs found while reviewing a Lumen session: empty `cm-mcp` log of a running session, Playwright setup hijacked by the manager's own `CM_CONTROL_TOKEN`, missing test-bridge stubs (FIX-01..03); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [LOGS-TASKS.md](./LOGS-TASKS.md) | Logs of every machine in one private GitHub repo (`cm-logs`): host and manager build in each log, `internal/logship` pushes run logs and manager logs into `logs/<host>/`, secret masking, settings/Settings/MCP (LG-01..04); the review skill is done separately in Chat; queue in [STATUS-P1.md](./STATUS-P1.md) |
| [LAB-TASKS.md](./LAB-TASKS.md) | Token and speed research on already recorded data, no paid runs: cache-write split (start vs growth), the Opus-1M vs Sonnet-handoff cost model (H8), a weekly passive report, the feature cost inventory with the R0 "all off" profile, pilot tools parametrized by clone (LAB-01..05); queue in [STATUS-P2.md](./STATUS-P2.md) |
| [GUI-TESTS.md](./GUI-TESTS.md) | ~100 Playwright test-case descriptions across all Svelte components (✓ exists / ○ missing) |
| [config.example.toml](./config.example.toml) | Annotated config reference |
| [docs/Science_lab/README.md](./docs/Science_lab/README.md) | Index of the cost and speed research: which document answers which question, what lives only on this machine, how to search, how to add results |
| [docs/Science_lab/MEASURE.md](./docs/Science_lab/MEASURE.md) | Runtime and cost study of Claude Code on this Windows machine (Lumen and claude-manager sessions), the `claude-code-setup` kit installed in `~/.claude`, and the before/after measurement procedure with the 2026-10-13 baseline |
| [docs/Science_lab/AB-PILOT-2026-10-28.md](./docs/Science_lab/AB-PILOT-2026-10-28.md) | A/B pilot of plain Claude Code against the manager (5 tasks): where time and dollars went, feature audit, test-suite growth; analysis scripts in `ab-pilot-tools/`, data in `measurements/2026-10-28-ab-pilot/` |
| [docs/Science_lab/NEXT-SESSION.md](./docs/Science_lab/NEXT-SESSION.md) | Handoff to the session that plans research on cutting token spend and speeding up tasks: what is known, what is decided, what to decide, how to verify, constraints |

## Research in progress: tokens and speed

The A/B pilot of 2026-10-28 found the manager about twice as slow and twice as expensive as plain Claude Code on five
tasks at equal quality (gates after the merge, 11 sessions instead of 5, manager overhead, a test suite that grew 4x in
a month). Start at [docs/Science_lab/README.md](./docs/Science_lab/README.md), then
[NEXT-SESSION.md](./docs/Science_lab/NEXT-SESSION.md). A change meant to cut token spend or speed up tasks is accepted
only with a closed acceptance card ([docs/change-acceptance.md](./docs/change-acceptance.md)): the effect must be
confirmed on the stand, and hidden tests, their paths and thresholds never go into this repository.

## Mirrored projects

Projects the user opted in (Settings → Projects → "Mirror into Claude Manager")
have a read-only copy of their settings and saved run logs in `projects/<name>/`
of the main checkout (`projects/README.md` is the index; `PROJECT.md` gives the real
folder). Use it to inspect another project's setup or runs; fix things in the real
project folder, never in the copy. `projects/` is a separate local git repository —
never commit it into claude-manager or push it. See "Project Mirror" in
`docs/design-decisions.md`.

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
| `Developer 1` | `STATUS-P1.md` | `VIEW-TASKS.md` (UI-01..13 done, UI-14), `RUNTIME-TASKS.md` (RT-01..08, done), `WT-TASKS.md` (WT-01..03), `QUEUE-TASKS.md` (QV-01..03), `CTX-TASKS.md` (CTX-01), `COST-TASKS.md` (COST-01..04), `MCP-TASKS.md` (MCP-01..18 done, MCP-19..23), `LEARN-TASKS.md` (LN-30..41), `WASTE-TASKS.md` (WS-01..12), `FIX-TASKS.md` (FIX-01..03), `LOGS-TASKS.md` (LG-01..04) | `p1-` | `p1-work` |
| `Developer 2` | `STATUS-P2.md` | `LAB-TASKS.md` (LAB-01..05) | `p2-` | `p2-work` |

Read the task file's own rules before starting — each breakdown carries
invariants that are not repeated here (`LEARN-TASKS.md` §Инварианты: everything
new is off by default, a prompt must stay byte-identical with its flags off,
nothing is written into a user's repository without approval in the UI;
`VIEW-TASKS.md` §Инварианты блока: the classic log view stays reachable and
unchanged behind a toggle).

**A `p<N>-…` branch that already exists is your own interrupted task —
continue it**, do not take a new one; see `docs/git-workflow.md` §Session start
for the full sequence.

One task = one session. Use the skills rather than running the protocol by hand:

| Skill | When |
|---|---|
| `/cm-task-start <task>` | starting a task — **explicit `/` invocation only** |
| `/cm-task-finish [branch]` | task ready: gate → doc-sync → merge `--no-ff` → push → free the slot |

Ad-hoc sessions (`Chat`, `Init`) have no queue and are not covered by this — they
work wherever the user points them.

## Git workflow

Full protocol, worktree pool, completion invariant — [`docs/git-workflow.md`](./docs/git-workflow.md).

**Push boundary.** A developer session pushes exactly two things without
asking: its own `p<N>-…` branch, and `master` after a green gate and a
`--no-ff` merge. Any other push — a different branch, a tag, a force-push, a
push from an ad-hoc session — still requires the user to ask for it. Never
`--no-verify`, `--force`, or `git config`. Never rewrite published history.
One standing exception (user-approved 2026-10-27): the log review skill
`/cm-logs-review` may push its own files (`ANALYZED.md`, reports) to the logs
repo `git@github.com:LearnJava/cm-logs.git` from an ad-hoc session — never
another machine's `logs/<host>/` folder, never a force-push
([LOGS-TASKS.md](./LOGS-TASKS.md)).

## Known gotchas

- **`window.confirm()` is disabled in Wails WebView2** and always returns `false` — see Conventions above; every confirmation flow in this codebase uses a two-click button or an inline banner instead.
- **`HideWindowOnClose: true`** (`main.go`) means closing the main window (✕) only hides it to the system tray — it does **not** quit the app or stop sessions. Only the tray "Quit" menu item (`runtime.Quit` → `app.shutdown` → `manager.Shutdown()` → `StopAll()`) stops every running CLI process.
- **Never kill or restart `claude-manager.exe` from a session.** Your session is a child of it, and every session is in its Job Object (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`), so killing the manager kills *you and every other session in every project*, instantly and with no saved logs. Rebuilding is fine — then tell the user the binary is ready and let them restart. A `PreToolUse` hook (`.claude/hooks/guard-self-kill.py`) blocks the attempt; do not work around it. See "Surviving the Manager's Death" in `docs/design-decisions.md`.
- **systray must start before `wails.Run`** and is torn down via `systray.Quit()` from `app.shutdown` — see the ordering comment in `main.go`. Getting this backwards leaves an orphaned tray icon after quit.
- **Build the GUI exe only with `wails build`, never a bare `go build`** — `run-claude-manager.bat`/`.sh` do it on every update. A bare `go build` skips regenerating `frontend/wailsjs` (gitignored: stale bindings break the frontend build once a bound method is added, and the launcher silently keeps the old exe) and the Windows resources (icon, manifest with per-monitor DPI awareness and Common Controls v6). The Wails CLI must match `github.com/wailsapp/wails/v2` in `go.mod` (v2.16.0 — CLI 2.12 crashes on Go 1.27 with `package "os" without types`). Pass `-nosyncgomod`; `wails build` still rewrites `go.mod` line endings (the launcher restores it) and writes `frontend/package.json.md5`, which is machine-specific and gitignored. Details: `docs/architecture.md` "Build & Run".
- **Pure-Go SQLite (`modernc.org/sqlite`), not `mattn/go-sqlite3`.** Deliberate: avoids requiring a CGO/C toolchain on a machine that only has Go installed. Don't swap it for a CGO driver without a strong reason.
- **No `.gitattributes`** — line endings are whatever Git's `core.autocrlf` does locally. A `git diff`/`git add` on Windows may print `LF will be replaced by CRLF` warnings; this is expected noise, not a bug.
- **fakeclaude/testkit scenarios match by prompt keyword regex** (`internal/testkit/scenario.go:MatchScenario`), not by session name — a new e2e scenario needs a prompt substring distinct enough not to collide with an existing scenario's `match` regex (see `testdata/scenarios/scenarios_doc.md`).

## Doc-sync update matrix

Update docs **in the same change** as the code, not as a follow-up:

| Change | Update |
|---|---|
| New/changed Wails-bound method on `App` | Row in `docs/architecture.md`'s "Wails Bindings (app.go)" table + an MCP tool in the registry (`internal/control/mcptools.go`) or an entry in `parityExcluded` (`app_parity_test.go`, the parity test) |
| New `SessionConfig`/`ProjectConfig`/`GlobalSettings` field | `config.example.toml` + relevant PLAN.md section |
| New stream-json event type or field the parser handles | "Stream-JSON Events (stdout)" section in `docs/design-decisions.md` |
| New `cm-mcp` tool | Nothing by hand: run `go run ./cmd/cm-mcp -gen-docs` (regenerates the tool reference and parity map in `docs/mcp.md`; `TestDocsInSync` enforces it). Add a recipe row to `docs/mcp.md` if the tool opens a new workflow |
| New fakeclaude/fakeworker scenario | `testdata/scenarios/scenarios_doc.md` entry describing state/feature covered and its `match` pattern |
| New app-level task breakdown item done | Corresponding checkbox/row in `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md` / `VIEW-TASKS.md` / `RUNTIME-TASKS.md` (+ delete its pointer line from the matching `*-STATUS.md` queue) |
| New package or otherwise notable file under `internal/` | The architecture tree in `docs/architecture.md` |
| Change to how a developer session takes/finishes a task | `docs/git-workflow.md` + the two skills under `.claude/skills/` — never a second copy of the steps here |

## When in doubt

- **Wails binding signatures / IPC shape** — `docs/architecture.md`'s "Wails Bindings (app.go)" table, then `app.go` itself.
- **Session lifecycle / CLI flags** — "Bidirectional Streaming" and "CLI Launch Command" in `docs/design-decisions.md`, then `internal/session/session.go`.
- **Why a design decision was made** — `docs/design-decisions.md`; if still unclear, `git log -p` on the relevant file.
- **What's left to build** — `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md`.
- **How to test without spending API tokens** — `docs/testing-harness.md`.

If none of these answer it — ask the user, don't assume.
