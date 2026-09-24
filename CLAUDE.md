# Claude Session Manager

Desktop app (Windows) to launch, monitor, and control parallel Claude Code CLI sessions across multiple projects.

## Documentation map

| Doc | Covers |
|---|---|
| [docs/architecture.md](./docs/architecture.md) | Tech stack, package/component tree, the Wails-bound method table (`app.go`), SQLite schema, file logging, build commands |
| [docs/design-decisions.md](./docs/design-decisions.md) | The *why* behind every non-obvious choice: config layering, bidirectional streaming, permissions, crash recovery, context handoff, the mixed-programming and experience-layer subsystems, and everything else under "Key Design Decisions" |
| [docs/testing-harness.md](./docs/testing-harness.md) | fakeclaude/fakeworker, the control-plane, `cm-mcp` — how to test without spending API tokens, and how Claude itself drives the running app for GUI testing |
| [docs/git-workflow.md](./docs/git-workflow.md) | Full developer-session protocol: branches, worktree pool, merge cadence, completion invariant |
| [PLAN.md](./PLAN.md) | Full specification — §14-21 cover CLI flags, bidirectional streaming, permissions, pre-flight analysis, token metrics, optimization, and the test/control harness |
| [TASKS.md](./TASKS.md) | App task breakdown (TASK-01..15) |
| [HARNESS-TASKS.md](./HARNESS-TASKS.md) | Test/control harness task breakdown (H1..H6) |
| [MIXED-TASKS.md](./MIXED-TASKS.md) | Mixed programming task breakdown (MP-01..08) |
| [LEARN-TASKS.md](./LEARN-TASKS.md) | Experience layer task breakdown (LN-01..18); queue in [LEARN-STATUS.md](./LEARN-STATUS.md) |
| [VIEW-TASKS.md](./VIEW-TASKS.md) | Hermes-style session feed in `LogStream` (UI-01..06); queue in [STATUS-P1.md](./STATUS-P1.md) |
| [GUI-TESTS.md](./GUI-TESTS.md) | ~100 Playwright test-case descriptions across all Svelte components (✓ exists / ○ missing) |
| [config.example.toml](./config.example.toml) | Annotated config reference |

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
| `Developer 1` | `STATUS-P1.md` | `VIEW-TASKS.md` (UI-01..06) | `p1-` | `p1-work` |

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
| New/changed Wails-bound method on `App` | Row in `docs/architecture.md`'s "Wails Bindings (app.go)" table |
| New `SessionConfig`/`ProjectConfig`/`GlobalSettings` field | `config.example.toml` + relevant PLAN.md section |
| New stream-json event type or field the parser handles | "Stream-JSON Events (stdout)" section in `docs/design-decisions.md` |
| New `cm-mcp` tool | "cm-mcp tools available to Claude" table in `docs/testing-harness.md` |
| New fakeclaude/fakeworker scenario | `testdata/scenarios/scenarios_doc.md` entry describing state/feature covered and its `match` pattern |
| New app-level task breakdown item done | Corresponding checkbox/row in `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md` / `VIEW-TASKS.md` (+ delete its pointer line from the matching `*-STATUS.md` queue) |
| New package or otherwise notable file under `internal/` | The architecture tree in `docs/architecture.md` |
| Change to how a developer session takes/finishes a task | `docs/git-workflow.md` + the two skills under `.claude/skills/` — never a second copy of the steps here |

## When in doubt

- **Wails binding signatures / IPC shape** — `docs/architecture.md`'s "Wails Bindings (app.go)" table, then `app.go` itself.
- **Session lifecycle / CLI flags** — "Bidirectional Streaming" and "CLI Launch Command" in `docs/design-decisions.md`, then `internal/session/session.go`.
- **Why a design decision was made** — `docs/design-decisions.md`; if still unclear, `git log -p` on the relevant file.
- **What's left to build** — `TASKS.md` / `HARNESS-TASKS.md` / `MIXED-TASKS.md` / `LEARN-TASKS.md`.
- **How to test without spending API tokens** — `docs/testing-harness.md`.

If none of these answer it — ask the user, don't assume.
