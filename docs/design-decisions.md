# Key Design Decisions

The why behind non-obvious choices in this codebase. See [CLAUDE.md](../CLAUDE.md) for the project index.

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

**Adding a folder that already has an overlay.** "Add project" in Settings
builds the entry from `emptyProject()` (`Sessions: []`), so a naive save would
write `session = []` over a cloned repo's committed `config.toml` and delete its
sessions. `UpdateConfig` therefore treats a project whose path is *not* in the
currently loaded config as new and runs `adoptExistingOverlay` first: every
unset field (sessions, gates, permission mode, opt-ins) is taken from the
folder, anything typed into the form wins. A project already known to the app is
saved verbatim — that is how deleting its last session in Settings still works.

**Privacy rationale.** `mixed_programming = true` is the "my code may leave this
machine" opt-in, so it lives only in the gitignored `config.local.toml` — a
teammate cloning the repo gets sessions+gates but must opt into external workers
themselves. Workers stay global (reusable presets, and the home-dir file is
never in a repo). `MixedProgramming` is a `*bool` in `ProjectOverlay` so an
absent overlay field leaves the global value untouched.

### Project Mirror (a Copy of Each Project in the Manager's Folder)
Settings live in each project's folder (above), so an agent working in the
claude-manager folder could not see how Lumen or any other project is set up,
what its sessions run or what its last runs logged. "Mirror into Claude
Manager" in Settings → Projects sets `mirror = true` on the project and from
then on `internal/mirror` keeps a copy under the mirror folder:

```
projects/                     <checkout>/projects, or [settings] mirror_dir
├── .git/                     own repository, no remote — never pushed
├── README.md                 index: project → folder
└── <name>/
    ├── PROJECT.md            folder path, sessions, list of copied files
    ├── effective-config.toml the ProjectConfig the manager runs (global + overlay)
    └── files/                CLAUDE.md, .mcp.json, .claude/{settings*.json,hooks,skills,
                              agents,commands}, .claude-manager/{config*.toml,journal.md,
                              logs,archived-skills}, each session's task_source
```

- **The project folder stays the source of truth.** Sync only reads from it;
  the copy is overwritten, and files deleted in the project are deleted from
  the copy (their content stays in the mirror's git history). Nothing is ever
  written into the project.
- **Where.** Default is `projects/` in the checkout the running exe was built
  in (`<checkout>/build/bin/claude-manager.exe`, how `run-claude-manager.bat`
  builds it) — the folder agents in claude-manager work in. It is in the
  checkout's `.gitignore` and has its own `.git`, so mirror commits never land
  in claude-manager's history and a developer session's `git push` of master
  cannot carry them anywhere. Elsewhere (no checkout) it falls back to
  `~/.claude-manager/projects`.
- **When.** 10 s after start, after every `UpdateConfig`, every
  `mirror_interval_min` minutes (default 15, 0 = timer off) and from the
  "Sync now" button; one sync covers all mirrored projects and makes at most
  one commit, only when something changed. Syncs are serialized by a mutex.
- **The flag is manager-side.** `mirror` is kept in the global `[[project]]`
  pointer (`UpdateConfig` carries it when shrinking the entry), not in the
  project's overlay — it is about this machine's manager, not the repo.
- **Commits** use a per-command identity (`-c user.name=claude-manager`), so no
  git config is written, and `core.autocrlf=false` so copies are byte-exact.
  Files over 5 MB are skipped.

### Log Shipping (cm-logs)

The manager runs on several machines, each with its own logs: run logs
`<project>/.claude-manager/logs/*.md` (gitignored, so they never travel with the
project) and the manager's `~/.claude-manager/logs/{app,crash,cmdguard}.log`.
To review the manager across all machines, each one pushes its logs to one
private repository (`logs_repo`, e.g. `cm-logs`). Off by default: an empty
`logs_repo` clones and sends nothing.

- **Layout.** `logs/<host>/machine.md` (build, Go version, projects, last push),
  `logs/<host>/manager/<name>-<date>.log` (only the new bytes of each manager
  log per day) and `logs/<host>/<project>/<YYYY-MM>/<run log>.md`. A machine
  only changes its own `logs/<host>/`, so pushes from different machines never
  conflict; `ANALYZED.md` belongs to the review skill and is never touched.
- **Marks live outside the clone.** What was already sent (per-project last log
  stamp, per-file byte offsets) is in `logship.json` in the data dir, not in the
  repository, so a recreated repository does not make the manager resend old
  logs. Marks move only after a successful push.
- **Reset to `origin/main`, not rebase.** Every pass (and every retry after a
  rejected push, up to 3) first resets the clone to the remote branch and then
  re-collects, so a retry cannot duplicate lines. An empty or recreated remote
  is picked up as a fresh repository on branch `main`. No force-push, no
  history rewriting, no `git config` (identity and `core.autocrlf=false` are
  passed with `-c`).
- **Masking.** Known secret shapes and the control-plane token are replaced by
  `***` before a byte is written into the clone, in run logs and manager logs.
- **When.** 30 s after start, every `logs_interval_min` minutes (default 60,
  0 = timer off), after a settings save and from Settings → Global → "Send now"
  (`ShipLogsNow`, MCP `ship_logs_now`; state: `get_log_ship_status`). Passes
  never overlap (`app_logship.go`); a failure is logged as `logship` at Warn and
  shown in the status line.

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
stdin if it receives one. In autonomous mode (`auto_restart`)
the manager closes stdin when the turn's `result` event arrives so the CLI exits
cleanly and the run loop can iterate; real Claude keeps the process alive on open
stdin otherwise.

**One-shot calls get the prompt on stdin.** The analysis helpers
(`internal/analysis`: brief, executor, handoff, journal, preflight, skill,
skillreview) run `claude -p` without a positional prompt and pipe it into stdin
(`setPromptStdin`, `cliprompt.go`) — a Windows command line is capped at 32,767
characters, which a long skill pattern or journal exceeded. The prompt text is
built by a function next to each arg builder (`skillPrompt`, `journalPrompt`, …).
`fakeclaude` mirrors this: in `-p` mode without `--input-format` a non-blank stdin
is the prompt for scenario matching, an empty one falls back to the last argument.

**Environment.** With `settings.python_utf8` (default true) the Claude process
gets `PYTHONUTF8=1` and `PYTHONIOENCODING=utf-8` (`utf8Env`,
`internal/session/launch_env.go`); a variable already set, in any case, is kept.
The agent's Bash tool inherits the CLI's environment, so every Python it runs
prints UTF-8 instead of the console code page — on a Russian-localized Windows
that was the most common source of unreadable tool output (8.6% of Python runs
in Lumen's sessions, `docs/Science_lab/MEASURE.md`; mostly cp1251/cp866 bytes a
Python wrapper re-printed). Hermes sessions are left as they were: Hermes is a
Python program itself and its own stdio encoding was not tested with the flag.

**Command Guard and the environment card.** A study of the saved run logs
(7,010 sessions, 150k tool calls, 2026-10-14) found ~2,100 mechanical slips —
Windows paths broken by bash quoting, a `cd` to a directory that is not under
the current one, tools Git Bash lacks, Read of a file too big for one call —
each costing about one extra model turn. Two measures follow from it, both on
by default for every project:

- `settings.command_guard` passes `--settings` with a PreToolUse hook,
  `"<manager exe>" hook cmdguard` (`internal/cmdguard`). It *rewrites* rather
  than refuses: a refusal costs the model the same round trip as the failure,
  so a deny hook saves nothing. The rewrite is returned as `updatedInput` with
  `permissionDecision: "allow"`, which also skips the permission prompt, so it
  is applied in `bypassPermissions` mode only. A process start costs ~50-100 ms
  on Windows; run before every Bash call that would cost more time than the
  fixes save, so the hook carries `"if": "Bash(cd .*)"` and the CLI starts it
  for 0.6% of calls only (the permission-rule syntax of `if` cannot match a
  backslash or a drive letter, so the Windows-path rules ride along on those
  calls rather than having a trigger of their own). Rules were replayed over
  93k logged Bash calls: none changed a command that worked. Each hook run is
  logged to `~/.claude-manager/logs/cmdguard.log` — the data for judging it.
  A subcommand of the GUI binary rather than a separate exe: a standalone
  binary started only ~15 ms faster, not worth a second build artifact.
  The same settings also guard the manager's own configuration
  (`cmdguard/protect.go`): `permissions.deny` rules `Edit(**/.claude-manager/config.toml)`
  and `…/config.local.toml` (an Edit rule covers Edit/MultiEdit/Write; deny
  holds in `bypassPermissions` too), and a second hook entry with
  `"if": "Bash(*.claude-manager*)"` whose `ProtectedConfigWrite` *denies* a
  Bash command that names such a file and writes it (`sed -i`, `cp`/`mv`,
  redirection, an interpreter's `open(…,'w')`, `Set-Content`, …) — reading
  stays allowed. Here a refusal is the point, unlike the rewrites: the manager
  does not reread a hand-edited config and its Settings window overwrites it,
  so an agent that wired a queue into `.claude-manager/config.toml` itself
  (an ARB Chat did) left the UI showing an empty Task source while the file
  said otherwise. Checked against the real CLI: Bash, Edit and Write are all
  refused and the file stays unchanged.
- `settings.env_card` appends a short card (`envCard`,
  `internal/session/envcard.go`) to the system prompt: OS and shell rules,
  missing tools, the Python command that works (on Windows the PATH `python`
  is often the Store stub, so standard install dirs are searched), the
  project root, the Read-a-large-file rule. On Windows it also sends file
  writes to Write/Edit rather than a heredoc or `python -c` string: source
  code's quotes, `$` and backticks break Git Bash quoting (an ARB developer
  session lost a turn to exactly that). Its text depends only on the
  machine and project, so it is byte-stable across runs and keeps the prompt
  cache. Both flags off leave the CLI arguments byte-identical.
  Among several off-PATH installs the newest wins by numeric version
  (`envcheck.NewestPython`): a string sort put 3.9 above 3.14.
- `settings.manager_card` appends the Claude Manager card (`managerCard`,
  `internal/session/managercard.go`): the project's sessions with their
  queues and role prompts, and the protocol for handing work over — a task is
  a self-contained section of `<BLOCK>-TASKS.md` (Depends on / Files / What
  to do / Done when), sized to one branch and one context window, assigned by
  a `file:line` pointer in the target session's `task_source`. The queue is
  exactly that setting: a session without one gets no invented queue file or
  task folder — the agent asks the user to set its Task source to the next
  free `STATUS-P<N>.md` (`nextQueueName`), the project's established naming,
  and fills it once confirmed; editing the manager's config is forbidden
  (and blocked by the command guard). An earlier wording ("create
  `STATUS-<SESSION>.md`, e.g. `STATUS-DEVELOPER-1.md`") added a second naming
  scheme next to `STATUS-P1.md`. A queue
  session also gets its own queue and the take/close rule (delete the
  pointer when done — that is how the run's outcome is judged). Why: a Chat
  session asked to "assign tasks to Developer 1" took it for a Jira user —
  the manager's sessions existed nowhere in its context, and per-project
  CLAUDE.md notes (claude-manager's own "Roles and session start") do not
  travel to other projects. Built from config only, so byte-stable.

**Environment check** (`internal/envcheck`, Settings → Global →
Environment, `App.CheckEnvironment`). The card tells the *model* what is
missing; this tells the *user* what to install. It looks up claude (the
configured `claude_path`), git, Git Bash on Windows (`CLAUDE_CODE_GIT_BASH_PATH`
or `<Git>inash.exe`), Python (the WindowsApps stub does not count), gh,
and the build tools go/node/wails, runs each found one's version command
(5 s timeout, in parallel), and for a missing one renders the install
command for the package manager the machine has: winget on Windows, brew on
macOS (with a row for Homebrew itself), apt/dnf/pacman/zypper/apk on Linux;
Claude Code uses its official installer script. The Wails CLI is also
flagged when its version differs from the `wails/v2` module the binary was
built with (read from build info). Levels — required / recommended / build /
optional — only colour the row and the summary; nothing is installed by the
manager itself, the user copies the command. The check runs when Settings
opens, not at startup, so a launch never waits on ten version commands.

**Finding the next rule: `go run ./cmd/guardaudit`** (`internal/guardaudit`).
The study is kept as a tool rather than a one-off script, so a new kind of
slip is found and a new rule judged the same way. It reads CLI transcripts
(`~/.claude/projects`, default) and/or saved run logs passed as roots
(`projects/`, `<project>/.claude-manager/logs`; overlapping roots count a run
twice) and prints two things. (1) Per rule: how many Bash calls it would
rewrite, split into failed / live / working. *Working* must be 0 — exit code
2 lists every such rewrite for review. A call counts as failed when the tool
flagged an error **or** its output carries the rule's own failure text
(`failEvidence`): `cd x; …` exits 0 after a failed cd. *Live* is a command
the installed hook already rewrote (`cmdguard.log`): the transcript keeps the
original text and its success, so without the log it would read as a broken
working command. A call whose recorded cwd is gone (a removed worktree) does
not replay the cd rule — its answer depends on the tree. (2) The failures no
rule touches, keyed by tool + `experience.ErrorKey` of the first meaningful
error line (a Bash `Exit code N` line is skipped) and ranked by distinct
sessions — the candidates for the next rule, or for a line in the
environment card when no rewrite can fix them. Projects under `%TEMP%`
(test runs) are excluded by default. A markdown log is lossier than a
transcript: a command is on one line (a heredoc command is not replayed —
its body would read as shell words), there is no cwd (the cd rule is off),
and results pair with calls first-in-first-out, so a lost result shifts
them — the rule's failure text is looked for within 5 steps either side
before a markdown call counts as working. Sessions are counted per file
(one run). Adding a rule: a function in
`cmdguard/fix.go`, a case in `fix_test.go`, its failure text in
`guardaudit.failEvidence`, then the audit over both corpora with `on_working`
0; a rule that needs a command shape other than `cd .…` also needs
`HookCondition` widened, which the `if` syntax limits.

### Stream-JSON Events (stdout)
Key event types to parse:
- `{"type":"system","subtype":"init",...}` — session info, model, tools, version. Its `slash_commands`, `skills` and `terminal_slash_commands` are kept on the `Session` (`initCommands`) for the message box's "/" autocomplete — see "Slash-Command Autocomplete" below
- `{"type":"assistant","message":{"content":[...],"usage":{...}}}` — text/tool_use with per-turn token usage
- `{"type":"result","total_cost_usd":...,"usage":{...},"modelUsage":{...}}` — final metrics. In autonomous runs, its `result` text is also scanned for the ```` ```ask-user ```` marker (see "Ask-User Questions" below) before being treated as a finished turn.
- `{"type":"user","message":{"content":...}}` — a replayed user turn (string → `user` entry; an array of `text`/`image` blocks — a message with a pasted screenshot — is one `user` entry, its text plus `[N image(s) attached]`, unless it carries `tool_result` blocks or `isMeta: true`, i.e. CLI-injected context such as a skill body) or tool results echoed back (`tool_result` blocks → `tool_result`/`error` entries). Exception: a turn whose text starts with `<task-notification>` was injected by the CLI itself when a background task (Bash `run_in_background`, a background agent) finished — nobody typed it, so `tasknotify.go` logs it as a `system` entry `Background task <status>: <summary>` followed by its other fields (`task-id`, `tool-use-id`, `output-file`, …) with entities unescaped, from either a string or a `text` block
- `{"type":"stream_event",...}` — partial-message events (from `--include-partial-messages`). No log entry (the full `assistant` message follows). A `content_block_start` becomes a transient `session:activity` event `{id, kind, tool?, since}` (`thinking` | `tool` | `writing`; `result` → `idle`); deltas are dropped so a long answer does not flood IPC. Hermes maps `text`→`writing` (on change only), `tool_use`→`tool`, `tool_result`→`thinking`, `result`→`idle`. Emitted only on a change of kind/tool (`Session.setActivity`), never stored in SQLite; a non-running status (idle/error/stopping/rate_limited) also resets it to `idle`. UI-09 decision: the session *status* stays `working` after `result` — changing it risks auto-restart and task queues, so "turn over" is expressed only as `activity: idle`.
- `{"type":"control_response",...}` — the CLI's acknowledgement of a `control_request` the manager wrote (see "Interrupting the current action"); recognized, nothing logged
- `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"|"allowed_warning"|"rejected",...}}` — rate limit status. Real Claude emits an informational `status:"allowed"` event on **every** session; only a rejecting status (`rejected`/`exceeded`/…) pauses/restarts the run. `allowed_warning` (with utilization) is surfaced to the UI but does not abort.

**Slash-Command Autocomplete.** Typing `/` as the first character of the
message box (`SessionInput.svelte`) opens a list from
`ListSlashCommands(id)` (`internal/session/slashcmds.go`), filtered by the
session's runtime:

- *Claude Code* gets `/name args` as a plain stream-json user message and
  expands skills, custom commands and headless-capable built-ins itself, so
  the pick is sent verbatim. Once the run's `system/init` arrived, its
  `slash_commands` ∪ `skills` minus `terminal_slash_commands` (and `__…`
  internals) is the authoritative list; before that the catalog is rebuilt
  from the same files Claude reads — `~/.claude` and `<project>/.claude`
  `skills/*/SKILL.md` and `commands/**/*.md`, plus every plugin in
  `~/.claude/plugins/installed_plugins.json` namespaced by its `plugin.json`
  name — plus a fixed list of built-ins. Descriptions come from the
  frontmatter.
- *Hermes* runs `hermes chat -Q --format stream-json` per turn, and that path
  passes the query to the model verbatim — none of Hermes' interactive slash
  commands is dispatched there, so none is offered. Skills still work through
  `-s`: the catalog is `<HERMES_HOME>/skills/**/SKILL.md` (named by Hermes'
  slug rule), and `hermesRunTurn` turns leading known `/skill` tokens (up to
  5, Hermes' own stack limit) into `-s <skill>` for that turn and strips them
  from the query (`expandHermesSkills`). Only names found by the scan are
  taken — Hermes fails the turn on a fully unknown `-s` — so `/usr/bin …`
  passes through as text. The user log entry keeps the original text.

**Structured call arguments (`config.LogEntry.ToolArgs`, UI-07).** `ToolInput`
keeps only one salient field; `BuildToolArgs` (`internal/session/toolargs.go`)
additionally flattens the whole `tool_use` input into `map[string]string` for
both runtimes — strings/numbers/bools as text, arrays/objects as `"N items"` /
`"N fields"`, null skipped. Each value is capped at 300 runes, the map at ~2 KB
(keys taken in sorted order). Live-only like `Diff`: not in `session_logs`.

**Tool call duration (`config.LogEntry.DurationMs`, UI-08).** Set on the
`tool_result`/`error` entry, live-only. Hermes sends `duration_ms` on
`tool_result` and `tool_call_id` on both events (the synthetic `hermes-N` id is
only a fallback when absent). Claude has no duration on the wire, so
`Session.handleLine` runs each event through `toolTimer` (`tooltimer.go`):
`tool_use` entry time → matching `tool_result` entry time; open starts are
dropped on the turn's `result`. The frontend pairs calls with results
(`logGroups.ts` `ToolCall`, state `running|ok|error`); an unanswered call
followed by a turn `result` is closed as `ok` with no duration.

**Linking a tool call to its result (`config.LogEntry.ToolUseID`, UI-01).**
`tool_use`/`tool_result` are separate stream events with no positional
relationship in the log — a `LogEntry` needs to carry the pairing itself so
UI-02's grouping doesn't have to guess from adjacency. Claude CLI already puts
the id on the wire (`tool_use.id`, echoed back as `tool_result.tool_use_id`,
including on the `is_error` block for a failed call): `parser.go` copies it
straight through onto both `LogEntry` sides. Hermes's stream-json
(`hermes_parser.go`) has no id at all, so `hermesStream` synthesizes one — a
monotonic counter per `tool_use`, pushed onto a per-tool-name stack; the
matching `tool_result` pops the most recent unclosed call with that name. This
assumes Hermes's own tool calls for one name don't complete out of FIFO order,
which holds for the current single-turn-per-process model (HR-04) where
results stream back in the same order the calls were issued. Not persisted to
`session_logs` — `History` (the classic view) is unaffected; the id lives only
on the in-memory/live-event `LogEntry` UI-02 groups from.

**Interrupting the current action.** `Session.Interrupt`
(`internal/session/interrupt.go`, `InterruptSession` on `SessionManager`/`App`,
`interrupt_session` in `cm-mcp`) aborts the turn in flight without stopping the
session: it writes the CLI's own control request
`{"type":"control_request","request_id":"interrupt-<uuid>","request":{"subtype":"interrupt"}}`
to stdin. The CLI answers `control_response` (parsed as an empty `EventLog`),
cancels the running tool call or generation (the tool result reads
`[Request interrupted by user for tool use]`) and ends the turn with a `result`
of subtype `error_during_execution`. `interruptPending` routes that one result
to `onInterrupted` instead of the usual path: no failure classification, no
"turn finished" — so even an autonomous run keeps stdin open — and the session
goes to `waiting_for_user` with `interrupted` set, which lets `SendMessage`
accept the next message there and return the session to `working`. The turn
that message starts ends the normal way (an autonomous run then closes stdin
and the queue loop goes on). Interrupt is refused when nothing is in flight
(`activity: idle`, except a pending permission prompt, which it also drops) —
an armed flag with no turn to end would swallow the next turn's result — and
for Hermes, which starts a process per turn and has no control channel.
Because `activity` otherwise stays `idle` from the last `result` until the
CLI's first stream event, queuing a user message (`sendInitialPrompt`,
`SendMessage`, `AnswerQuestion`) marks the session `thinking`
(`markTurnStarted`), as the Hermes runtime already did. UI: a `■ Stop` button
at the end of the live status line under the running action (feed layout,
`LiveStatus.svelte`) and `■ Interrupt` in the control bar (`SessionView.svelte`),
both gated by `lib/interrupt.ts:canInterrupt`.
If the aborted turn's result does not arrive within 1 s (the CLI ignores the control request, e.g. during a long subagent), `escalateInterrupt` kills the process and resumes the same conversation (`--resume`), as Ctrl+C would.

### Permission Handling
When `permission_mode != "bypassPermissions"`, Claude CLI sends permission requests via stdout and blocks waiting for response on stdin. The manager MUST:
1. Parse the permission request from stream-json
2. Check auto-approve rules (TOML config + runtime rules)
3. If no rule matches → emit to UI, set status=WaitingPermission, block goroutine
4. Wait for user response via UI → write response to stdin
5. Never ignore — session hangs forever if no response

Escalation settings (PLAN.md §16.9, `internal/session/permission_timeout.go`)
are read from the live config when a request is queued, so a change applies to
the very next request, also in a running session:
- `permission_native_notification` / `permission_sound` travel as `notify` /
  `sound` on the `session:permission` event; the frontend toasts / beeps
  (`frontend/src/lib/beep.ts`, Web Audio) accordingly. An event without the
  fields keeps the old behaviour (toast, no beep).
- `permission_timeout > 0` arms a `time.AfterFunc`; when it fires and the
  request is still queued, `permission_timeout_action = "deny"` answers deny,
  `"pause_session"` drops the request and hard-stops the session (crash
  recovery can resume it). The timer is never cancelled — it re-checks the queue.

### Session Features Popup and Launch Tuning
The ⚙ icon before each session name in the Sidebar opens
`SessionFeatures.svelte`: every setting that changes a session's behaviour,
ON/OFF, a plain-language description, and an editor. Each edit is saved at
once through `UpdateConfig` (same path as the Settings dialog). The catalog
(`frontend/src/lib/sessionFeatures.ts`) marks which settings the manager
reads live (`LIVE`) — the rest are read at session start, so after changing
one while the session runs the popup offers "Restart now".

Settings that existed in config but were not read anywhere are now wired:
- `exclude_dynamic_system_prompt` → `--exclude-dynamic-system-prompt-sections`
  on Claude launches (`launch_tuning.go`).
- `max_turns_auto_adjust` → `--max-turns` = ceil(1.6 × longest of the last 20
  finished runs), never below `max_turns_default`, only after 5 finished runs.
  A run cut off by it ends as `error_max_turns` → "unfinished", and a queue
  session continues the task in its next run. Autonomous runs only: an
  interactive session (Chat) has the user watching and gets no cap.
- `preflight_analysis` + per-session `preflight` → `SessionPreflight` before a
  UI start (`preflight_gate.go`): only for a session with a prompt and no task
  queue; `auto` + `preflight_auto_approve_single` starts a single-session task
  at once, otherwise `PlanReview` shows the plan with "Start the session as is".
  `StartSession` itself never blocks on analysis (RPC/MCP/StartProject).
- `daily_budget_alert` / `weekly_budget_alert` → `alert:budget` after each
  finished run, deduplicated per day / ISO week (`budget_alerts.go`); the week
  is the last 7 local dates, matching how `daily_metrics` rows are written.

### Auto Model Routing
When `optimization.auto_model_routing = true`, clicking ▶ on a session triggers:
1. Pre-flight analysis (haiku, one-shot) on the session's configured prompt
2. `ModelRouter.Route()` maps `estimated_complexity` → model/effort recommendation
3. Frontend shows `ModelPicker` with the recommendation
4. User can **accept** or **override** model/effort before the session starts —
   or **skip the analysis** ("Skip analysis — choose manually") while it is
   still running, which drops straight into the manual dropdowns. The
   pre-flight call is not cancelled (it is a plain `claude -p` the frontend
   has no handle on), so `skipAnalysis` sets a flag that makes the late
   resolution/rejection a no-op instead of overwriting a choice the user has
   since made by hand. Worth having because the analysis is a full CLI
   round-trip: on a slow model or a rate-limited account the picker would
   otherwise sit on "Analyzing task complexity…" with no way forward.
5. `StartSessionWithOverride(project, name, model, effort)` launches with chosen values

Routing table (`internal/optimization/routing.go`):

| Complexity | Model | Effort |
|---|---|---|
| trivial | haiku | low |
| standard | sonnet | medium |
| complex | sonnet | high |
| architectural | opus | high |

If `auto_model_routing = false` (default), clicking ▶ starts the session immediately with the model from config.

**Routing by measured outcome** (LEARN-TASKS.md LN-13,
`internal/optimization/outcomes.go`). The static table above is a starting
point, not the last word: `ModelRouter.SetOutcomeProvider` (wired in
`GetModelRecommendation`, `app.go`, to `*store.Store` when a store is
configured) lets `Route()` downgrade its table/analyst recommendation to a
cheaper, lower-tier model when *this project's own history* shows that model
completing reliably. The override only fires when the candidate has
`>= MinOutcomeRuns` (5) finished runs, a completed rate `>=
OutcomeCompletedThreshold` (90%), and a measured average cost below the
originally recommended model's own — and never for `ComplexityArchitectural`,
and never to a model the static ladder (`modelTier`: haiku < sonnet < opus)
doesn't already know is strictly lower than the one being replaced. With no
provider wired, or `project == ""`, `Route()` is byte-identical to the
pre-LN-13 table lookup.

`Store.OutcomeStats(project, complexity)` is the real implementation of
`optimization.OutcomeProvider`: it aggregates finished `session_runs` by
`(model, effort)` for the project. `effort` is a new additive column on
`session_runs` (LN-13), filled at run start/finish from the session's own
`SessionConfig.Effort` — effort is fixed at CLI launch (see "CLI Launch
Command" above), so it never changes mid-run. `complexity` is accepted for
interface conformance and echoed onto every returned row as a label, but it
does **not** filter the query: `session_runs` carries no per-run complexity
tag. The analyst's `estimated_complexity` for a prompt is computed ad hoc by
`GetModelRecommendation` and never persisted against the run it eventually
starts, and `task_plans` (the table that does carry `estimated_complexity`)
has no foreign key into `session_runs` — a plan executed via `ExecutePlan`
runs through `analysis.CLIExecutor`'s one-shot `claude -p`, not through
`SessionManager`, so it never produces a `session_runs` row at all
(`plan_subtasks.session_run_id` exists in the schema but is currently always
nil). Aggregating per `(model, effort)` project-wide, complexity-unfiltered,
is the coarser, honest alternative: `Route()`'s own tier-ordering,
minimum-run-count and cost-threshold checks are what keep an override safe
despite the coarser grain, not this query.

### Task Source Check
Unless `run_without_tasks = true`, when `task_source` is set (default since QV-03; the retired `stop_when_no_tasks` key is migrated on load: `true` without `auto_restart` becomes `auto_restart = true` when the session has a `task_source` — without one it stays interactive —, `false` is dropped), `Run()` checks the file before each iteration using `hasTasks()`. Two formats are supported (mirrors `orchestrator.py has_tasks()`):

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
false` regardless of `AutoRestart` (stdin stays open after
the turn's `result`, exactly like a manually-started interactive session), and
`initialPromptText()` sends a dedicated "no pending tasks, wait for
instructions" message instead of the session's configured task-source prompt
(crash-recovery still takes priority over both). After that one `runOnce` call
returns, `Run()` unconditionally goes Idle and exits — it does **not** rejoin
the auto-restart loop, so the user must stop it manually (the Stop button) or
it exits when the CLI process does. Covered by the `no-tasks-interactive` e2e
scenario (`testdata/e2e/`, `testdata/scenarios/`, `testdata/configs/`).

**Task description resolution.** Once `hasTasks()` confirms work
remains, `resolveTaskSourceDescription()` (`internal/session/session.go`)
mirrors `orchestrator.py`'s `resolve_pointer_desc()`: it takes the first bare
pointer line (`ROADMAP.md:92`) and reads the exact text of that line from the
referenced file (relative to `ProjectPath`), truncated to 200 chars — e.g. a
ROADMAP.md table row or a task-file heading. Legacy-format files fall back to
the text following `"In progress:"` or the first `"- ["` item under
`"Next:"`. The result is stored on the session (`setTaskSourceDesc`, emits
`session:task_source` only when it changes) and exposed as
`SessionState.task_source_description`. The frontend no longer shows it as text (the
TaskPanel "Task" tab was removed); a change of it is the signal that a pointer line was
consumed, so `stores/queues.ts` and `RoadmapTree.svelte` re-read the roadmap on it. This only
resolves the *description* of the top task; it does not change which task
`hasTasks()`/Claude picks.

**`session:queue_changed` (QV-02).** The description signal above only fires when a
pointer is consumed; a pointer *appended* to the queue file, or a task file edited, was
invisible until the next task finished. `internal/session/queuewatch.go` polls every
configured session with a `task_source` every 2 s: it reads the (small) queue file and
`os.Stat`s it plus each file its pointers name, comparing `(path, mtime, size)` with the
previous pass (a vanished file counts as a change). The first pass only records state. A
change emits `session:queue_changed {id}`; `stores/sessions.ts` debounces it 300 ms per
session and calls `loadQueue`. Started from `startup` (`StartQueueWatch`), stopped by
`Shutdown`.

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
and `open/planned/queued/ready/opt` → pending), with the *current* task shown as
active regardless of what its column says — the session is on it. Only the first
pointer gets that: a status file may hold the whole backlog (this repo's
`LEARN-STATUS.md` queues all 18 open tasks), and promoting every queued row to
active would paint the entire roadmap in progress; the rest are marked queued
via `InQueue` instead. `wontfix` counts as blocked, not done: those rows are
deferred (`WONTFIX (Phase N+)`) and marking them finished would inflate the
progress bar.

**The status word is not the first token.** A hand-maintained column writes the
state with a marker in front — `✓ DONE (2026-09-08)`, `○ TODO`, `● IN PROGRESS`,
`[x] done` — and splitting on the first space yields the glyph, which matches
nothing and falls through to `pending`. `trimStatusGlyphs` drops leading
non-alphanumeric runes (and a `[x]` checkbox, whose own letter would otherwise
become the keyword) before `statusWord` takes the keyword. Without it this
repo's own `LEARN-TASKS.md` rendered 0/18 done with LN-01 finished.

**A pointer may address the task's heading, not its table row.** A generated
roadmap's queue points straight at ROADMAP.md rows, so the line number matches.
A curated breakdown is the opposite shape: `LEARN-STATUS.md` holds
`LEARN-TASKS.md:292`, which is `## LN-02: …` — the heading of the task's own
section, while the table at the top of that file is the index. `remapPointers`
therefore resolves a pointer that lands on no row by id: the leading token of
the pointed-at line (`LN-02`, `BUG-349`) matched against each row's id or the
first token of its name. An unmatched pointer is left alone — a pointer into a
source file still means nothing. Without this the "← now" mark and the queued
marks never appeared on a curated breakdown at all.

**Tasks that exist only as headings.** A pointer that lands on a `## <ID>: <title>` heading (the id may have several numeric parts: `LN-03.1`, `ARB1026-02`) with no matching table row (or in a file with no task table at all) becomes a task node of its own (`appendHeadingTasks`): `InQueue`, `pending`, `active` for the first pointer, `HasDetail`. In a file with no task table, headings without a pointer are added as `done` — under the pointer model a task is finished once its pointer is gone. With a table present, unpointed headings are left to the table's status. These nodes follow the tabular ones in line order; clicking one returns its section body (to the next heading of the same or higher level, or `---`) via `ReadRoadmapRowDetail`.

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
disallowed `Bash` call — `use_worktree = true` (see "Session work folder"
below: the flag prepares the same slot the protocol occupies),
`task_source = "STATUS-P1.md"`, `auto_restart =
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
reaching `master`, for ~$12. That is also why a bare `--worktree` is never
used: it makes a fresh anonymous worktree from HEAD on every process start,
which is exactly the mechanism that loses the previous attempt. `use_worktree`
for such a session (`upsertP1Session`) is forced **on** and means the permanent
slot instead — see "Session work folder".

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

A Chat is never cut short. `context_handoff` is off in the config it creates
(the context restart would end the user's conversation; the CLI's own
auto-compact handles a full context), and `--max-turns` from
`max_turns_auto_adjust` is passed to autonomous runs only — in a Chat it ended
a long answer mid-task with `error_max_turns` (2026-10-14).

### Crash Recovery
Mirrors `orchestrator.py` session state files (`.session-PN.json`).

**How it works:**
1. Before each `runOnce()`, `StateStore.Save()` writes `~/.claude-manager/state/<project>-<session>.json` with `{started_at, task}` (atomic: tmp → rename). `task` is the resolved task-source description of the run, so the resume prompt below can name what was left unfinished.
2. When the `system/init` event arrives, `StateStore.UpdateSessionID()` patches the file with `session_id` (loads → patches → saves, so `task` survives).
3. On successful task completion, `StateStore.Clear()` deletes the file.
4. On app restart, `Run()` loads the state file. If `session_id` is present, it sets `resumeSessionID` and `buildCLIArgs()` uses `--resume <id>` instead of `--session-id <uuid>`.
5. The recovery prompt is `CrashRecoveryPrompt` from config, or a built-in default that mirrors `orchestrator.py`'s: run `git status`, read the session's `task_source` file, reconcile that with the resumed conversation history to work out what is already done, then continue — and it names the interrupted task when one is known. The model's own last message is not treated as evidence of committed work. Only an unattended (`auto_restart`) session gets it: an interactive session resumes silently and waits for the user's message, unless `crash_recovery_prompt` is set.
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

### Surviving the Manager's Death (Self-Kill Guard + Watchdog)

Every session CLI is assigned to a Windows Job Object created with
`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (`internal/proc/proc_windows.go`). That is
deliberate — it is the only reliable way to reap the whole tree a run spawns
(bash, cargo, node, subagents) — but it has a consequence worth stating
plainly: **when the manager process dies, the OS closes the job handle and
every session, in every project, dies with it instantly.** No clean shutdown,
no auto-saved logs, every task cut mid-step.

Two separate mechanisms address that, because the failure has two shapes.

**1. A session must not kill its own host.** A session running in this repo is
a child of the manager. Asked to rebuild and restart the app, the obvious
sequence — build, find the running instance, `taskkill /F`, relaunch — destroys
the session issuing it *and* every sibling session, and leaves no trace: a
`TerminateProcess` produces no panic, no `shutdown.begin`, and no Windows error
report. This is not hypothetical; it happened on 2026-10-04 11:36:01 and cost
every session then running.

- `internal/session/env.go` puts `CLAUDE_MANAGER_PID` and `CLAUDE_MANAGER_EXE`
  into every session's environment. Inherited values are dropped first, so a
  manager launched *from* a session does not pass on a stale PID.
- It also sets `CM_SESSION_ID=project/session`, which `cm-mcp` forwards so the
  control-plane can refuse a session stopping/restarting/clearing itself
  (MCP-05, docs/mcp.md "Безопасность").
- `.claude/hooks/guard-self-kill.py`, registered as a `PreToolUse` hook in
  `.claude/settings.json`, refuses shell commands aimed at the manager — by
  name, by the advertised PID, or by resolving a numeric PID through the OS
  for ad-hoc sessions that inherit nothing. It fails *open*: anything it
  cannot resolve is allowed, because a false block on every Bash call is worse
  than a missed kill. The kill verb must sit in command position, so a path
  like `guard-self-kill.py` does not read as a kill.
- The refusal tells the session what to do instead: build, then let the user
  restart. There is no in-app restart binding, and deliberately so — a restart
  is a decision about other people's running work.

**2. If the manager dies anyway, it must come back.** `run-claude-manager.bat`
(and its `.sh` counterpart) runs the app in a relaunch loop rather than
launching it once. A crash brings the app straight back, and crash recovery
(above) resumes the sessions with `--resume`.

A deliberate quit must stay quit, so the two are told apart by a marker file:
`App.markCleanExit` writes `~/.claude-manager/state/clean-exit` from Wails'
`OnShutdown`, and startup removes it. Present after the binary exits → the app
meant to go; absent → it was killed or crashed. Exit code is *not* used:
`taskkill /F` reports success, and a dying WebView2 can take the app down with
a zero status.

Crash-loop protection: at most `MAX_RESTARTS` (10) consecutive failures, with a
5-second backoff, and the counter resets once a run survives
`HEALTHY_SECONDS` (60) — an isolated nightly crash does not spend the budget.
The backoff uses `ping -n`, not `timeout`, which refuses to wait when stdin is
redirected — i.e. whenever the launcher is started by another process.

**Update on launch.** Before the relaunch loop, the launcher runs
`git fetch`, fast-forwards (`pull --ff-only --autostash`) if `origin` is ahead,
and rebuilds when `HEAD` differs from the commit recorded in
`build/bin/claude-manager.commit` (written after a successful build). The
rebuild is `wails build -nosyncgomod` — never a bare `go build`, which leaves
the frontend bindings stale and the exe without icon and manifest (see
`docs/architecture.md` "Build & Run"); the bare build remains only as a warned
fallback when no `wails` CLI is on `PATH` or in `GOPATH\bin`. So
"update" is just starting the launcher again — no deleting the exe. It happens
only at launch, never mid-run: the manager's restart kills every session (Job
Object), so the moment is the user's. Not a git checkout, offline, or a failed
pull/build all fall back to the existing binary. Uncommitted local edits are
not detected; delete the stamp to force a rebuild. The step is a single
parenthesised block ending in `goto :launch` because a pull may rewrite the
`.bat` itself and cmd reads batch files by byte offset.

`watchdog_test.go` drives the real `.bat` with a stand-in binary (restart,
clean exit, zero-exit-without-marker, stale marker);
`internal/session/guard_hook_test.go` drives the real hook. Both are part of
`go test ./...` because neither a `.bat` nor a Python hook is otherwise
exercised by anything.

**What this does not fix:** the GUI and the session engine still share one
process, so a WebView2 failure still takes the sessions with it. The real fix
is a headless engine with the GUI as a client over the existing control plane
(`internal/control`, `127.0.0.1:7333`); the watchdog is what keeps the cost of
that failure to a restart until then.

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

**Machine and build line.** Right after `_Saved …_` the md header carries
`_Machine <host> · build <rev>_` (txt: `Machine …`; json: `machine`/`build`).
`internal/buildinfo` supplies both: the Windows host name (not the config, so a
`config.toml` copied to another computer cannot merge two machines into one log
folder) and the 7-char VCS revision (`-dirty` if modified, `unknown` outside
git). A review of logs from several computers needs it to tell a fixed bug from
a live one. `experience.ParseLogFile` reads it into `Trajectory.Machine/Build`;
old logs without the line parse as before. `app.log`'s `startup` line carries
`host` and `build` too.

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

### The Feed — Hermes-Style Log Grouping (VIEW-TASKS.md UI-01..05)

**Why.** The classic `LogStream` is one line per `LogEntry` — a tool call and
its result are two unrelated rows, a run of a dozen `Read`/`Grep` calls reads
as a dozen lines, and an agent's markdown reply looks exactly like `git
status` output. Watching Hermes Desktop for comparison (VIEW-TASKS.md's
screen-recording notes) showed the difference is almost entirely grouping and
collapse, not new information: tool calls in a row collapse to one line, a
file edit becomes a small diff card, thinking collapses to a duration, prose
gets proportional font. `lib/logGroups.ts` (`groupEntries`) turns the flat
`LogEntry[]` into that structure; `LogStream.svelte` renders it when
`stores/logView.ts: logLayout === 'feed'`.

**Grouping rules (`groupEntries`).** A run of `tool`/`tool_result`/`error`
entries collapses into one `'tools'` block, keyed by its first entry's `seq`
(stable across the log's ring-buffer eviction and the search filter — same
reasoning as the classic view's row `overrides`). `system`/`thinking` entries
inside an open run pass through without closing it; anything else (`text`,
`user`, a new file-editing call) flushes the run and starts its own block. A
file-editing call (`Edit`/`Write`/`MultiEdit`, `patch`/`write_file` — any
`LogEntry.Diff` from the backend) always gets its own `'edit'` block instead
of joining a `'tools'` run: a diff card reads nothing like "+3 more
commands". The collapsed `'tools'` header shows the first call's label plus
"+N commands", or — when every call in the run is `READ_TOOLS`
(`Read`/`Grep`/`Glob`/`LS`/`NotebookRead`) — a "Просмотрено: a.go, b.go" file
list instead, matching how Hermes shows a read-only scan. An `error` anywhere
in a collapsed run surfaces as a `✖` on the header itself — collapsing a
group must never hide that something inside it failed.

**Classic view is the invariant, not a fallback.** `logLayout` defaults to
`'feed'` as of UI-05 (was `'classic'` through UI-02..04, opt-in while the
feed was still being built out); an existing `localStorage` choice always
wins over the default either way, and unchecking "Feed" in the toolbar
returns to exactly today's row-per-entry `LogStream`, still covered by its
own untouched spec files. `LogEntryRow.svelte` — one log row's collapse/
markdown/raw rendering — is shared by both views (the classic `{#each}` and
an expanded feed group), so a row looks identical wherever it is shown; only
the framing (group header, diff card, thinking line, user bubble) differs
between the two.

**Search behaves the same in both views.** `LogStream` groups the *filtered*
entries, not the other way around, so a search hit inside a collapsed `tools`
run auto-expands that one group (`matchingSeqs`) instead of the row simply
not being visible. The `N/M` counter above the log still counts entries, not
groups — a collapsed group with 3 of its 4 rows matching should not read as
"1 match".

**Why `log-markdown.spec.ts` forces classic.** That spec pushes an orphan
`tool_result` with no preceding `tool` call to test per-entry markdown
rendering in isolation; in the feed that adjacency-only push starts its own
collapsed `'tools'` group (correct per the grouping rule above), which would
hide the assertion behind an unrelated expand click. It sets the `Feed`
checkbox off before each test so the assertions stay about markdown, not
grouping — the feed's own markdown behaviour (prose renders proportional, a
markdown message stays uncollapsed) is covered separately in
`log-feed.spec.ts`.

Covered by `frontend/tests/log-groups.spec.ts` (pure `groupEntries` unit
tests), `frontend/tests/log-feed.spec.ts` (thinking/prose/edit-card DOM,
light+dark) and `frontend/tests/log-feed-default.spec.ts` (default-on,
localStorage override, Ctrl+F into a collapsed group, an error on a
collapsed header, autoscroll) — GUI-TESTS.md LS-19..25.

### The Live Turn Block (VIEW-TASKS.md UI-07..13)

What makes the feed *live* rather than a static transcript. Everything here is
transient: never stored in SQLite, so a reloaded history shows tool rows and
prose but no spinners, durations or summary lines. Fields are live-only on
purpose — the data exists only while the CLI streams it, and persisting it
would need a schema change for a cosmetic feature.

- **Tool call fields (UI-07/08).** `tool_args` on the `tool` entry and
  `duration_ms` + `tool_use_id` pairing on the `tool_result`/`error` entry.
  Claude has no per-call duration on the wire, so `tooltimer.go` measures it;
  Hermes reports `duration_ms` itself and `hermesStream` synthesizes ids.
- **Activity (UI-09).** `session:activity {kind: thinking|tool|writing|idle,
  tool?, since}`. Claude source: `stream_event` `content_block_start`
  (`--include-partial-messages`). Hermes source: `text`→writing,
  `tool_use`→tool, `tool_result`→thinking, `result`→idle. Emitted on change
  only; session *status* stays `working` after `result`, "turn over" is
  `activity: idle`.
- **`toolDisplay` (UI-10).** One table in `lib/toolDisplay.ts` maps a tool name
  to emoji, a "running" phrase (`💻 Running npm test`) and a done description;
  unknown tools fall back to the name. Shared by `ToolCallRow` (UI-11) and
  `LiveStatus` (UI-12), so a call reads the same in the expanded group and in
  the status line.
- **Live status line (UI-12).** `LiveStatus.svelte` + pure `lib/liveStatus.ts`
  under the last block while activity ≠ idle; one timer, only while visible.
- **Turn summary (UI-13).** After a prose block whose entry is the turn's
  `result`, a muted line `✓ Done in 42s · 12 turns · 35.0k tokens · $0.18`
  (`turnSummaryData` in `logGroups.ts`). Data comes from `session:result`
  (`duration_ms`, `num_turns`, `usage`, `total_cost_usd`, `subtype`) and is
  attached to the `result` log entry as `LogEntry.turn` by the store
  (`attachTurn`). The two events have no guaranteed order, so whichever
  arrives first is parked (`pendingTurn`) until the other shows up. Unknown
  values are omitted, not zeroed: Hermes has no turns or cost, so its line is
  duration + tokens. A non-`success` subtype or `stop_reason: error` renders a
  red `✖ Failed: <subtype|text>`. Tokens = input + output + cache read + cache
  creation, same total the rate limit meters.
- **Run boundary (UI-14).** The first manager line of every CLI launch
  ("Готовлю промпт… · модель <model>", `emitRunStartLog`) carries live-only
  `LogEntry.run_start`. `groupEntries` flushes the open series on it and emits a
  `run` block (`RunBoundary.svelte`: rule + "New run · time"); a call before the
  last `run_start` without a result counts as ended (like `processEnded`), and
  `lastOpenCall` stops at it, so a dead call never spins. The classic view draws
  the same rule above a `run_start` row: the feed buffer is not cleared when a
  session restarts, and without the rule (and the model in the line) a new
  run's lines passed for the old run's. The saved log of the previous run
  loaded after an app restart (`source: "history"`) is headed by a
  "Previous run" rule and dimmed in both views.

Covered by `live-status.spec.ts`, `tool-call-row.spec.ts`,
`tool-display.spec.ts`, `turn-summary.spec.ts` (GUI-TESTS.md LS-26..29).

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

### Hermes CLI Runtime (experimental, HERMES-TASKS.md)

`SessionConfig.Runtime = "hermes"` makes a session drive `hermes chat
--query-file - --format stream-json` instead of `claude`. Empty/`"claude"`
leaves the Claude launch path byte-identical. `runOnce` branches to
`runOnceHermes` (`internal/session/hermes_runtime.go`) right after the
crash-recovery state save; everything after parsing goes through the shared
`handleEvent(ParsedEvent)`, which `handleLine` now delegates to.

**One process per turn.** A Hermes process answers one query and exits
after its `result` line — there is no long-lived stdin conversation. The
conversation id comes from the `system/init` line (stored as
`CLISessionID`, so crash recovery's `resumeSessionID` works unchanged), and
every later turn is a new process with `--resume <id>`. For an interactive
session `runOnceHermes` loops: turn → wait on `inputCh` (the same channel
`SendMessage`/`AnswerQuestion` already write the Claude user envelope into;
`decodeInputLine` unwraps it) → next turn. A live model switch therefore
never restarts anything: `SetSessionModel` returns early for a Hermes
session and the next turn's `-m` picks it up.

**Stream mapping** (`hermes_parser.go`): `text` deltas are buffered and
flushed as one `text` log entry at the next non-text event; `tool_use`/
`tool_result` become `tool`/`tool_result` entries (`todo_list`'s *result*
feeds the TaskPanel); `result.tokens` becomes one `EvtUsage` plus the
`EvtResult`; `exit_code != 0` or `error` marks the turn failed, and its text
is run through `detectRateLimitText`/`isAuthError`. Real recordings live in
`testdata/hermes-stream/`. There is no cost in the stream yet (HR-06).

**Not mapped:** Hermes has no `--append-system-prompt`, so the session's
`SystemPromptAppend` and, for autonomous runs, the ask-user/background
prompts are prepended to the first turn of a fresh conversation.
`max_budget_usd`, `fallback_model`, allowed/disallowed tools and `add_dirs`
are ignored with a system log line. `bypassPermissions` → `--yolo`; any
other mode leaves Hermes' own approval policy in charge (a non-TTY run
blocks dangerous commands rather than asking).

Tests: `hermes_test.go` (parser on real recordings, args), 
`hermes_runtime_test.go` (full Run loop against `cmd/fakehermes`),
`hermes_real_test.go` (real `hermes`, opt-in via `CM_REAL_HERMES=1`).

**Rate limit behind a 401.** `hermes chat` reports only a turn's *last*
error, and Hermes rotates credentials on a 429 — so a spent usage limit can
surface as another credential's `401 … token has been revoked`. A failed
turn is therefore checked against `<HERMES_HOME>/logs/agent.log` (last 4 MB):
a `RateLimitError` / `Credential 429` line tagged with this conversation id
and dated after the turn started makes it a rate limit (`EvtRateLimit`, wait
`rate_limit_pause`, retry). Hermes gives no reset time, so it simply retries
every pause. After a rate limit or an error the next attempt `--resume`s the
same conversation instead of restarting the task.

**Soft stop only at a task boundary** (both runtimes): `Stop(soft)` is
honoured before the first run, after a closed task, or after a finished
slice — never on an error, rate limit or unfinished turn, which used to end
a session mid-task when the limit ran out after the user had asked it to
stop *after* the task. `TestHermesRuntime_RateLimitBehind401_ResumesDespiteSoftStop`
replays the incident via `FAKEHERMES_FAIL_429_THEN_401=1`.

**`clarify` reaches the user.** A headless `hermes chat --query-file -`
answers the agent's `clarify` tool itself (`[single-query mode: no user
available … Pick the best option … and continue.]`), so the agent used to
carry on with its own guess while the question never showed in the UI.
`hermesStream` now attaches the call's questions (`{"questions":[{question,
choices, multi_select}]}` or the legacy top-level form) to the matching
`tool_result` as `ParsedEvent.Questions` (each tagged `Source:
hermes_clarify` — RT-03's shared ask-user-question shape, docs/runtimes.md
§9). On it `hermesRunTurn` stops reading and kills the process — Hermes has
already persisted the call and its auto-answer before emitting `tool_result`,
so the conversation stays resumable. The questions then go through the
ordinary pending-question banner one at a time (`(i/n)` prefix, choices as
buttons, free text allowed); after the last answer all of them are sent as
one `--resume` turn that tells the agent to disregard the automatic reply.
The 5-minute first-option fallback applies only to autonomous runs. Test:
`TestHermesRuntime_ClarifyAsksTheUserAndResumes` (fakehermes `ASK_CLARIFY`).

### Why a Common Event Format (RUNTIME-TASKS.md RT-01..08)

On 2026-09-28, in `Lumen browser/Chat` (Hermes runtime), the agent called
`clarify` with three questions. Headless `hermes chat` has no attached user,
so it answered its own tool call ("no user available… pick the best
option") and the agent carried on with that guess — the user never saw the
question at all. Fixed by commit `2064fe0`, but the deeper problem was
structural: Claude's and Hermes' stdout streams were each handled by
ad-hoc, adapter-specific logic, so a class of bug ("a question needs a
human, but the two runtimes disagree on what a question even looks like on
the wire") had nowhere shared to be caught.

RUNTIME-TASKS.md (RT-01 through RT-08) is the fix at the format level, not
just the one incident: both runtimes' stdout is translated into one
`ParsedEvent` shape (`internal/session/parser.go`) before `Session.handleEvent`
ever sees it, so the core (statuses, ask-user questions, failure
classification, the log feed, token metrics) is written once and runs
identically regardless of which CLI produced the bytes. Concretely:

- `ParsedEvent.Questions`/`Question.Source` (RT-03) gives every
  "this needs a human" signal — Claude's `ask-user` marker, Hermes' same
  marker, Hermes' `clarify` tool — one shared pending-question/answer path,
  so the exact incident above (a question silently answered by the runtime
  itself before the user sees it) has a single place — `Question.Source`'s
  per-source stop-and-resume rule — where a new runtime's own quirks are
  handled, instead of a bespoke fix bolted onto one adapter.
- `TurnFailure`/`ParsedEvent.Failure` (RT-04) does the same for rate-limit,
  auth, session-not-found, context-restart and step-limit outcomes: one
  classified value and one consolidation point (`Session.applyFailure`)
  instead of six independent `atomic.Bool` writers scattered across two
  files.
- `Runtime`/`Parser` (RT-05) makes the seam explicit as a Go interface
  (`internal/session/runtime.go`) instead of an `if Config.IsHermes()`
  sprinkled through the run loop; RT-06 finishes the job by routing the two
  process loops' shared bookkeeping (argv building, per-run atomics reset,
  outcome classification) through it.
- `internal/session/runtime_contract_test.go` (RT-07) is the resulting
  payoff: one table-driven suite asserts the same behavioral contract
  (questions reach the user, failures classify correctly, tool
  calls/results share an id, usage arrives once) against *every* `Runtime`
  implementation, so a third runtime is "connected" exactly when it passes
  this suite unedited — not when someone eyeballs its adapter code.

`docs/runtimes.md` is the living map of this format: §§1-6 describe the
current pipeline and the two adapters' known differences, §7 is the
step-by-step guide for plugging in a third runtime, §§9-11a document each
RT task's contribution. Any future runtime-visible bug of this shape (a
signal one adapter produces that the shared core doesn't know how to route)
should be fixed by extending `ParsedEvent`'s existing fields/enums and this
document, the same pattern RT-03/04 used — not by adding another
adapter-local workaround like the one `2064fe0` had to patch over.

### Auth Error Handling (403)
`drainStderr()` detects lines containing `"403"` + `"forbidden"` / `"authenticate"` / `"unauthorized"`.
On detection, `authErrorHit` atomic is set → `runOnce()` returns `errAuthError` → `Run()` pauses 60 seconds and retries.
The crash-recovery state file is cleared on auth errors (not resumable).

### Transient API Errors (network, 5xx, overloaded)
When the API is unreachable the CLI ends the turn with a clean `result` whose text is e.g.
`API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)` and exits 0.
`handleResult` classifies such text (`API Error` prefix + a network/5xx marker, `isTransientAPIError`) as
`transient_api` → `runOnce()` returns `errTransientAPI` → `Run()` resumes the same conversation
(`--resume <CLISessionID>`) after `retry_delay` doubled per consecutive failure (capped at 10 min).
It never counts toward the 3-unfinished-runs stop, so an outage of any length does not end an autonomous loop.

### Token Optimization
- Context grows with every turn (all messages re-sent). Monitor `usage` in each `assistant` event.
- Auto-restart session when context > 75% of `contextWindow` — opt-in per session via `context_handoff`, since `contextWindow` is only ever known from a `result` event's `modelUsage`, not the init event (see "Context Handoff on Restart" LN-15 below for exactly how/when this fires).
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

### Cache-Affinity Launch Order (LEARN-TASKS.md LN-14)

`StartProject`'s plain-config-order start (the bullet above, "stagger session
starts... for cache warming") staggers *when* sessions start but says nothing
about *which order* — and order matters for two independent reasons: a model
switch resets the shared system-prompt cache
(`--exclude-dynamic-system-prompt-sections` only helps across starts of the
*same* model), and two sessions about to re-read/edit the same files benefit
more from starting back to back than staggered apart by an unrelated session.

**Ordering** (`internal/experience/affinity.go`, `OrderByCacheAffinity`):
groups a project's sessions by launch model, preserving each model's
first-appearance order in config; within a group, a greedy chain starts at
the group's first session (input order) and repeatedly appends whichever
remaining session shares the most files with the one just picked (ties go to
input order). `LastRunFiles(store, project, session)` supplies the per-session
file list — Read/Edit/Write args from that session's most recent completed
run (`action_signatures`, the same table LN-02/LN-03 populate) — unlike
`primer.go`'s `previousRunFilesSection`, Read counts too: cache affinity cares
about what a session is likely to touch again, not just what changed.

**No history → unchanged order.** A session with no previous run has an empty
file list, which has zero overlap with everything; the greedy tie-break then
always falls back to input order, so a fresh project (or one with
`experience_tracking` just turned on) reproduces plain config order
byte-for-byte within each model group — the LN-14 "Готово когда" invariant.

**Wiring** (`app.go:StartProject`/`projectStartOrder`): computes the order
and, when non-empty, starts sessions one by one via `SessionManager.
StartSession` (each call already staggered by `reserveStartSlot`/
`session_start_delay`) instead of delegating to `SessionManager.StartProject`.
`projectStartOrder` returns `nil` — falling back to the unordered delegate —
whenever there's nothing to order by: `[optimization] experience_tracking`
off (default), no store configured (e.g. `cmd/playwright-server`), or an
unknown project. `internal/optimization/cache.go` gained the analogous
`CacheAffinityStart`/`StartProjectOptimizedOrdered` (an ID-aware sibling of
the existing `StartProjectOptimized`) for a caller with an already-computed
order; `internal/optimization` cannot import `internal/experience` itself
(`internal/store` already imports `internal/optimization` for
`OutcomeProvider`, LN-13, so the reverse import would cycle), which is why
the actual grouping/chaining algorithm lives in `internal/experience` and
`app.go` is the seam between the two, same pattern as `ActionIndexFunc`/
`PrimerFunc`.

**Measurement** (LEARN-TASKS.md invariant 5): `CostDashboard.svelte`'s
per-project row now shows a `cache NN%` figure — `cache_read / (input +
cache_read + cache_creation)` from the same `GetProjectTokens` call the row
already made, just reading the `cache_read`/`cache_creation`/`input_tokens`
fields it previously discarded — next to the existing (all-projects) "Cache
efficiency" KPI tile. There is no separate before/after toggle: switching the
dashboard's existing period picker (today/week/this month) is the comparison,
the same way it already is for cost and token totals.

### Context Handoff on Restart (LEARN-TASKS.md LN-15)

The "Token Optimization" bullet above ("Auto-restart session when context >
75% of `contextWindow`") described intended behavior that had never actually
been wired into the run loop — `optimization.ContextMonitor` existed only as
a standalone, unit-tested utility. LN-15 is the first thing to actually act
on a context-restart threshold crossing, and it does so with a **handoff**
instead of the `--resume` a naive implementation would reach for: `--resume`
reloads the CLI's full conversation history server-side, which re-inflates
exactly the expensive prefix that caused the restart in the first place. A
handoff is a ~paragraph distilled recap (done / remaining / decisions /
files touched) sent as the first user turn of a fresh, non-resumed process
instead.

Context fill (the card's bar and this threshold) is the usage of the *last* model call of a turn, not the sum over its calls (`result.usage` sums them); the summed counters are spend only (CTX-01).

**Flag:** `SessionConfig.ContextHandoff` (`context_handoff`, default false).
With it off, `checkContextRestart` never touches `contextMonitor`/
`pendingHandoff`/`contextRestartHit` at all — byte-identical to before this
feature existed, for every session that hasn't opted in.

**Why detection can only happen at a turn boundary, never truly mid-turn.**
`ModelUsage.ContextWindow` (the model's actual context size) is only ever
present on a `result` event's `modelUsage` map — no per-turn `assistant`
message usage carries it (see "Stream-JSON Events" above), so there is no
signal to check against *during* a turn, only once it has already finished.
`Session.checkContextRestart` (`internal/session/session.go`) therefore runs
in `handleLine`'s `EventResult` case, right before the turn is reported
finished: it feeds the turn's own usage into a per-`Session` (not
per-run — it must survive to the *next* process) `optimization.ContextMonitor`
via `Params.Optimization` (`&AppConfig.Optimization`, threaded through
`SessionManager`'s three session constructors), and reacts only to
`ContextEventRestart`. The turn that pushed utilization over the threshold
still completes normally; it is the *next* turn — the next human message for
an interactive session, or the next task-source attempt for an autonomous
one — that gets diverted through a handoff instead of continuing/repeating
blind.

**Forcing the restart, for both session shapes.** An autonomous session
(`task_source`/`auto_restart`) already closes stdin on every finished turn
and starts a fresh process for the next task, so nothing extra is needed
there. An **interactive** session does not — stdin stays open indefinitely
for the next human message, which is exactly the case a growing, never-reset
conversation needs this most. `runOnce`'s scanner loop therefore closes
stdin on `s.contextRestartHit.Load()` in addition to the existing
`finished && autonomous` condition, so a context-restart forces the same
clean-EOF process exit either way — no hard kill, no `cancelRun()`, just the
same `closeInput()` real Claude CLI already treats as "end this process
cleanly" (see "Bidirectional Streaming").

**`errContextRestart` is its own `runOnce` outcome**, checked before
`waitErr` (same precedence as `authErrorHit`/`rateLimited`). `Run()`'s switch
treats it as neither a completed task (no `tasksDone++`, no post-task hook,
no `EvtTaskDone`) nor a retryable error (no sleep): it clears any
crash-recovery state file (a handoff-driven restart must never accidentally
`--resume` on a later app restart either), rotates `CLISessionID` to a fresh
UUID, and `continue`s the loop immediately — bypassing the trailing
`if !s.Config.AutoRestart { return }` check that would otherwise end an
interactive session's run loop right there, discarding the handoff before it
could ever be sent.

**Distillation** (`internal/analysis/handoff.go`, `GenerateHandoff` — haiku,
mirrors `GenerateJournalEntry`/LN-06's shape exactly: `HandoffInput`/
`HandoffResult`, `BuildHandoffArgs`, `ParseHandoffOutput`,
`HandoffJSONSchema`/`HandoffSystemPrompt` in `internal/analysis/schema.go`).
Input is the task pointer, the live TodoWrite state (`formatTodoLines`,
`[x]`/`[~]`/`[ ]` checklist lines) and the tail of the *interrupted* CLI
session's own transcript — `internal/experience/handoff.go`'s
`BuildHandoffInput` locates it via `FindTranscript`/`Read` (LN-01) and keeps
the last `MaxHandoffSteps` (40) steps via `recentStepLines`. Best-effort: a
transcript that can't be found (fakeclaude in tests never writes one) simply
yields no `RecentSteps` rather than failing the whole restart —
`RenderHandoffPrompt` still produces a usable recap from `done`/`remaining`
alone, and even with no `HandoffFn` wired at all the restart still happens,
just without a distilled recap (`initialPromptText` falls through to the
session's normal prompt).

**Wired like `PrimerFunc`/`JournalWriteFunc`, for the same reason.**
`internal/experience` already imports `internal/session` (for `Step`), so
`session.go` cannot call `experience.BuildHandoffInput` directly.
`session.HandoffFunc` is the indirection (`SessionManager.handoffFn`,
`SetHandoffBuilder`, threaded into every `New(Params{...})` call site);
`app.go` wires it to a closure over `experience.BuildHandoffInput` +
`analysis.GenerateHandoff` + `experience.RenderHandoffPrompt`, called
synchronously inside `checkContextRestart` (unlike LN-06's fire-and-forget
journal goroutine, the very next prompt depends on this result, so it must
block).

**`initialPromptText` priority order:** crash recovery (`resumeSessionID`,
unchanged — LN-15's invariant is that this path never uses a handoff) →
pending handoff (consumed and cleared on read) → the "no pending tasks"
interactive fallback → the normal prompt (+ context primer, LN-05). A
handoff and crash recovery never actually coincide in practice (the
context-restart path never sets `resumeSessionID`), but the ordering is
still enforced explicitly.

Covered by `internal/session/session_contexthandoff_test.go` (the trigger
logic, the priority ordering, the flag-off no-op), `internal/analysis/
handoff_test.go` and `internal/experience/handoff_test.go` (the distillation
pipeline pieces), and the `context-handoff`/`context-handoff-off` e2e
scenarios (`testdata/e2e/`, reusing the existing `context-growth.json`
fakeclaude scenario) — with the flag on, `StartSession` produces two
`session:init` events (a genuine second CLI invocation, each with its own
fresh `--session-id`, never `--resume`) before settling at `idle`; with it
off, exactly one. The e2e config pins `context_restart_mode = "fresh"`.

**`context_restart_mode` picks the reaction** (`checkContextRestart`); the
per-session `context_handoff` flag still gates all of it, and the UI shows
the three keys as one "Features" card:

- `fresh` — the recap restart described above (`restartWithHandoff`).
- `resume` — keep the live process and conversation and send the CLI's own
  `/compact` as the next user turn (`compactInPlace`); the CLI summarizes the
  history in place. No new process, no recap call. With no live stdin it
  falls back to `fresh`.
- `ask` — a manager-side question (`PendingQuestion.onAnswer`, so
  `AnswerQuestion` handles the choice instead of sending it to the model):
  compact, restart with a recap, or continue as is. A message typed instead
  counts as "continue"; the monitor stays fired, so that conversation is not
  asked again. Choosing a restart closes the idle process's stdin through
  `Session.endInput`.

`resume` and `ask` need a process idling between human turns, so an
autonomous run (its process ends with the turn anyway) and a per-turn
runtime (Hermes) always take `fresh`.

**Re-arming.** `ContextMonitor` fires once; after a restart or `/compact`
the session sets `contextRearm`, and `ContextMonitor.RearmBelow` re-arms it
only on the first turn back under the threshold. Re-arming right after the
restart would loop: a fresh process whose first turn is already over the
threshold (or the `/compact` call itself, which reads the whole history)
would trigger again and again. Before this, the monitor was never reset at
all, so a session restarted at most once in its lifetime.

### Context-Budget Handoff Mid-Turn (LEARN-TASKS.md LN-28)

LN-15 reacts only at a turn boundary and only to a share of the model's
context window. An autonomous task is usually **one** turn of 50–450 model
calls, so by the time its `result` arrives the expensive part is already paid
— and with a 1M window, 75% is far past where the money goes. Measured on
Lumen's transcripts (`docs/Science_lab/MEASURE.md`): sessions over 100 calls
were 23% of sessions and 56% of spend; context per call grows from ~59k
tokens in short sessions to ~231k in the longest, and every call re-reads it.

**Flag:** `SessionConfig.HandoffContextTokens` (`handoff_context_tokens`,
0 = off — prompt byte-identical, nothing written to stdin). Autonomous Claude
runs only for the note: an interactive session has no slice to hand off (see
"Interactive run" below), and Hermes runs one process per turn with no
mid-turn stdin.

**Trigger** (`checkHandoffBudget`, `internal/session/handoff_budget.go`):
every `assistant` line's `usage` (input + cache read + cache creation = the
context that call sent). All lines of one call repeat its usage, so a repeat
of the previous total is skipped as the same call. The first call at or over
the budget sends a note; a reminder follows every `handoffRepeatEvery` (20)
over-budget calls, at most `handoffMaxNotes` (3) per run — reaching a
committable state can take a few calls, past that a reminder only adds tokens.
Calibration on Lumen: 150k is reached by a third of sessions at about their
78th call (close to the ~90 calls the study recommended), 120k by more than
half at about the 56th.

**Channel: a user message on stdin, announced in the system prompt.** The
CLI delivers a message written to stdin mid-turn attached to the next tool
result. Probed with the real CLI: a bare note was ignored by haiku as a
prompt injection ("the tool result contains text claiming to be a manager
instruction"); with `handoffBudgetPrompt` appended to the system prompt
haiku, sonnet and opus all stopped after the current call and followed it.
The note starts with `[session-manager <tag>]`, the tag minted per process
by `buildCLIArgs` (`handoffTag`), and the prompt says text without the exact
tag is not from the manager — tool output cannot impersonate it. A
PostToolUse hook (via `--settings`) was the alternative: it depends on the
shell the CLI runs hook commands in and was ignored in one of two probes.

**Outcome:** the note asks the agent to finish the step, commit work in
progress with a handoff note in the commit body (done / next / how to
verify) and end the reply with the `continue_session` block. The run then
classifies as a finished slice (`classifyTaskOutcome`: pointer still queued
+ marker), the next process starts fresh — no `--resume` — and the
developer protocol's "an existing `p<N>-…` branch is your interrupted task"
rule makes it continue from the note. Nothing is killed: an agent that
finishes the task instead simply closes it. Each note is a `system` entry
in the session feed and a `session.handoff_budget.note` line in the app log.

**Interactive run (COST-03).** A non-autonomous Claude run gets no note — the
user decides. At the end of a turn (`checkInteractiveBudget`), if the turn's
last model call (CTX-01's `lastCallUsage`) carried at least the budget, the
existing `askContextQuestion` asks: compact / restart with a recap / continue,
worded in tokens ("160k tokens, budget 150k"). Independent of
`context_handoff` and `context_restart_mode`; skipped when `checkContextRestart`
asked in the same turn. Once per process; after "continue" it asks again only
when the context has grown by half a budget (150k → 225k). A new process
resets it. Nothing reaches the agent's prompt or stdin.

Covered by `internal/session/handoff_budget_test.go` (off = byte-identical
prompt and no input, the per-run tag, one note per crossing call, the
repeat/cap schedule, interactive runs untouched, `utf8Env`).

### Loop Detection in Sessions (PLAN.md 20.3.8)

`optimization.LoopDetector` existed with tests long before any session called
it, so `loop_detection` was a toggle that changed nothing. It is wired in
`internal/session/loop_guard.go`: `handleEvent` passes every `tool` log entry
(both runtimes produce them) to `observeLoops`, keyed by
`NormalizeInput(ToolName, ToolArgs)`. On a hit the detector's window is
cleared and `loop_action` runs:

- `warn` — a manager line in the session log, `EvtLoop` → `alert:loop`
  (`{id, tool, input, count, window, action}`) → a toast in the frontend.
- `send_hint` — the same, plus `loop_hint` sent into the live stdin. Hermes
  (`PerTurnProcess`) has no live stdin, so there it degrades to `warn`.
- `restart` — the same, then `cancelRun()` stops the process with
  `loopRestartHit` set; `classifySentinelError` returns `errContextRestart`,
  so `Run` takes the context-restart branch (fresh process, crash state
  cleared). `pendingLoopNote` is put in front of the session's normal prompt —
  unlike a context handoff, the prompt itself (task protocol) is kept.

**No endless restarts.** `resetRunAtomics` moves `loopRestartHit` into
`afterLoopRestart`: the run a loop restart started only warns on a loop, so
a task that loops again is not restarted forever; the run after it may
restart again.

**Keys, against false alarms.** Read keys carry the range (`path @offset:limit`)
— the kit's "read large files by range" rule would otherwise read one file in
parts and trip the detector; Edit keys carry the start of `old_string`, so
several different edits of one file are not a loop. Read-after-edit
suppression compares paths (`pathOf`). The fallback key for unknown tools
joins values in key order (it used map order before — nondeterministic).

`loop_detection` defaults to true with `loop_action = "warn"`, so wiring it
turned on warnings for every session; `send_hint`/`restart` stay opt-in.
Covered by `internal/session/loop_guard_test.go` and the `loop-detected`
(warn) / `loop-restart` e2e scenarios.

### UI Localization (EN/RU)

The frontend renders in English or Russian; nothing else (prompts sent to
Claude, log/task/git data from the CLI, model/tool names, code and comments)
is ever translated — this is a display-only concern, unlike the "Reply
language: Russian" rule for chat replies elsewhere in this file.

**No dependency, same shape as every other reading preference.**
`frontend/src/lib/i18n.ts` exports `locale` (a `writable<'en'|'ru'>`,
persisted to `localStorage` under `cm.locale`, default `'en'`) and `t` — a
`derived(locale, ...)` store holding a `(key, params?) => string` lookup
function, used in components as `$t('namespace.key')` /
`$t('namespace.key', { count })` (`{count}` placeholders in the dictionary
string). A plain `.ts` module (not a Svelte component — e.g.
`stores/sessions.ts`'s toast notifications) reads the same store via
`get(t)('key')`. Mirrors `stores/theme.ts`'s own
localStorage-writable-subscribe pattern exactly, deliberately — this app
ships zero frontend dependencies (see "Markdown in the Log" above for the
same reasoning applied to the log renderer).

**Dictionaries are per-component fragments, not one giant file.**
`frontend/src/lib/locales/en/<component>.ts` /
`frontend/src/lib/locales/ru/<component>.ts` each export a flat
`Record<string, string>` whose keys are namespaced `<component>.<key>` (e.g.
`sidebar.startAll`); `frontend/src/lib/locales/en.ts` / `ru.ts` import and
spread every fragment into the one dictionary `i18n.ts` actually reads. A key
missing from the active locale falls back to English, then to the bare key
itself — a missing translation renders as (at worst) English text, never a
blank string or a crash.

**English text is byte-identical to what it replaced, on purpose.** The
default locale is English specifically so that turning this feature on
changes nothing for `frontend/tests/*.spec.ts`, which assert on visible
English text throughout (`Playwright`'s default browser locale is not
pinned, so `testdata/configs/playwright.toml` sets `language = "en"`
explicitly for determinism). Every English dictionary value must therefore
match the string it replaced exactly — this is a mechanical extraction, not
a rewrite — while the Russian value is a real translation with no such
constraint.

**Persisted like the theme, in the same two places.** `GlobalSettings.Language`
(`language`, TOML, default `"en"`) round-trips through the same
`GetConfig`→mutate→`UpdateConfig` path as `Theme`; `Settings.svelte`'s
"Global" tab has a `Language` `<select>` next to `Theme`'s, and `load()`
calls `setLocale(cfg.Settings.Language === 'ru' ? 'ru' : 'en')` right after
`setTheme(...)`, so the two settings apply identically on dialog open. The
`localStorage` copy in `i18n.ts` is what applies instantly on app boot,
before `GetConfig()` has resolved — same rationale as `theme.ts`.

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

- **Session/task boundary** ("should I keep working in this same
  session?") has a deterministic answer per the one-session-per-task rule —
  always no — so it is not asked at all: the model is told to end its last
  reply with a "done" marker instead, resolved instantly.
- **Genuine external decisions** (an ambiguous requirement, a stuck
  investigation) still pause the run for a human, but with a 5-minute timeout
  that auto-picks the first listed option so the run is never stuck forever.

**Marker convention.** Every autonomous run's system prompt gets
`askUserProtocolPrompt` (`internal/session/session.go`) appended via
`--append-system-prompt`, alongside any configured `SystemPromptAppend` —
never replacing it. It teaches Claude both forms of the fenced block:
````
```ask-user
{"question": "<one-line summary of what was done>", "options": [], "kind": "continue_session"}
```
````
as the end-of-session "done" marker (end the turn immediately after emitting
it — never keep working in the same reply), and the same block without `"kind"`
for a genuine decision, with the instruction that unanswered options are
tried in listed order — so list them by actual preference.

**Detection** (`ParseAskUserQuestion`, `internal/session/parser.go`): a plain
regex extracts the fenced block from the `result` event's text and decodes
the JSON into `AskUserQuestion{Question, Options, Kind}` (prose fallback
below). A missing or unrecognizable marker returns `nil` — silently falling back to normal turn
completion, since a false positive must never hang the session forever. Autonomous
runs (`(AutoRestart || StopWhenNoTasks) && !forceInteractive`, the same flag
that gates closing stdin) get the timeout handling below; interactive runs
are covered under "Interactive sessions".

**Interactive sessions.** Recognizing the marker is the manager's job, not the
asker's: in an interactive run (Chat) `handleEvent` turns any `ask-user`
marker into a `PendingQuestion` + `session:question` with status
`WaitingForUser` and **no timeout** (the user is there) — regardless of
`ask_user_marker`, since a project `CLAUDE.md` can ask for the block on its
own. The result still reports a finished turn; the process stays alive. The
answer is either `AnswerQuestion` or any message typed in the box
(`SendMessageWithImages` accepts `WaitingForUser` while a question is pending
and clears it). `continue_session` is ignored here. `SessionConfig.AskUserMarker`
(default off, prompt byte-identical when off) only adds
`askUserInteractivePrompt` — the JSON schema, no one-task rule, no timeout
wording (Hermes via `hermesPreamble`).

**Prose bodies.** A model never taught the schema writes prose inside the
fence ("question line, then `- option` lines"). `ParseAskUserQuestion` falls
back to `parseProseAskUser` when the body is not JSON: list items (`-`, `*`,
`•`, `1.`, `1)`) become options, the remaining lines the question; accepted
only with at least one option or a trailing `?`. With several blocks in one
turn the last recognizable one wins (a model often re-asks after a broken one).

**Why a "done" marker, not a question.** The marker used to be phrased as a
question ("Continue in this session" / "Stop" options, always auto-answered
"no"). In the log it then read as a question nobody answered, and it invited
the model to propose the next task at all. The prompt now says outright *do
not ask whether to continue* and the block carries only a summary of what was
done; the kind name `continue_session` stays for compatibility (the parser,
`classifyTaskOutcome`'s slice detection and Hermes' `continueSeen` key off
it). The log view (`LogEntryRow.svelte` via `lib/askUserMarker.ts`) folds the
block into one "✓ Session done: …" line; copy still yields the raw text.

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
`continue_session` marker never reaches the frontend as a question — it's
fully resolved on the backend; only the reply text carrying it is displayed,
folded to one line.

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
current turn and wait for it in one blocking call (a large Bash `timeout`, or
`timeout 590 bash -c 'until grep -q … <log>; do sleep 10; done'`) — never in
`sleep N; cat log` polls, since every poll re-reads the whole conversation
(Lumen 14–23.10.2026: 540 polls, ~12% of spend) — or, if it genuinely can't finish in one turn,
persist progress to a file the *next* session can pick up — never end a
turn assuming background work will still be running or will be reported
back later.

Hermes sessions get `hermesBackgroundTaskWarningPrompt`
(`internal/session/hermes_runtime.go`) instead, sent in `hermesPreamble`: the
Claude premise is false there. A one-shot `hermes chat` *lingers* after the
final reply for every `notify_on_complete` process still running (Hermes's
`terminal.oneshot_completion_wait_seconds`, 600s by default) and feeds each
completion back as one more turn. Observed on Lumen S4: a baseline run left
in the background held the process ~20 minutes past the agent's
`continue_session` marker, then produced an extra reply without the marker
(which `hermesStream.continueSeen` now carries over, so the run still counts
as a finished slice). The Hermes text tells the agent to `wait` for or `kill`
its background processes before ending the turn, and to persist unfinished
work to a file rather than leave it running.

**Forced exit after the done marker (EX-01).** A turn that ended on the `continue_session` marker means the task is closed, so waiting for a background command the CLI still holds (observed: ~30 min on Lumen browser/S6, blocking the next queued task) is pointless. `runOnce` arms a second timer, `turnExitGrace` (60 s, after `turnExitNotice`) — when it fires the manager logs `session.exit_force`, cancels the run context (kills `claude.exe`, the job object reaps its children) and treats the run as a normal finish. A plain `result` without the marker is never force-killed: the CLI may legitimately continue on its own.

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

### Experience Layer (LEARN-TASKS.md, LN-01..18)

Mines the app's own operational history — CLI JSONL transcripts and auto-saved
markdown logs (LN-17) — into things that save the *next* session tokens and
time: permission allowlists, a warm context primer, distilled skills. Off by
default; every feature is additive and stdlib-only (no external workers,
unlike Mixed Programming above).

**Ingest backends produce one common shape.** `experience.Trajectory`/`Step`
(`internal/experience/transcript.go`, LN-01) is backend-agnostic: a JSONL
transcript backend and a markdown-log backend (LN-17) both stitch
`tool_use`↔`tool_result` into the same `Step` fields. Consumers must not
assume every field is populated — `Input`/`ToolUseID`/`Usage` are simply zero
on a backend that never had raw JSON to begin with.
Two JSONL quirks the reader handles: a failed call's row carries
`toolUseResult` as a bare string (`"Error: Exit code 1…"`), not an object —
until 2026-10-14 that failed the row's decode and silently dropped every
failed call's result; and a `tool_use` row is written after the call ran, so
its `cwd` already reflects the call's own `cd` — `Step.Cwd` takes the cwd of
the last non-assistant row before it instead.

**Signatures turn a raw call into something aggregable** (LN-02,
`internal/experience/signature.go`). `Signature(tool, inputText, projectPath)`
takes `Step.InputText` — never the raw tool_use JSON, so it works identically
on either backend — and returns a normalized `sig` (literals masked to
`<ARG>`, Bash reduced to its argv shape per five measured rules: keep the
subcommand of a multi-command utility like `git`/`cargo` literal, strip a
`cd`/`export` navigation prefix, keep an interpreter's script basename
literal, mask everything else, cap at 6 tokens) plus the original `arg`
verbatim. Read/Edit/Write reduce to `<first two dir segments>/*<ext>`;
Grep/Glob keep the pattern (their whole meaning) truncated to 40 chars; every
other tool falls back to its first input token, truncated to 30. Without the
masking rules the top of any aggregate degenerates to noise — measured
directly: `Bash:cd <ARG>` was the single most common raw signature (20 388
calls) before rule 2, and `git <ARG>` swallowed every subcommand into one
bucket before rule 1.

**Storage** (`action_signatures`/`ingest_state`, `internal/store/migrations.go`):
one row per tool call (project, session, run_id, sig, arg, is_error,
out_tokens, result_chars, ts) plus a per-CLI-session ingest checkpoint
(`GetIngestOffset`/`SetIngestOffset`) so re-indexing a transcript never
re-inserts rows already seen — mirrors `StateStore`'s tmp→rename-free
upsert-by-primary-key approach rather than a file. `Store.InsertActions`
batch-inserts per ingest pass (mirrors `InsertLogs`); `Store.TopSignatures`
aggregates by `(project, sig)` over a trailing window (count, distinct runs,
error rate, sample args) — the input the "Actions" tab (LN-03) and downstream
candidate mining (LN-04 permission rules, LN-07/08 skill promotion) both read.

**A second backend: auto-saved markdown logs** (LN-17, `internal/experience/mdlog.go`).
JSONL transcripts (LN-01) live only on the machine a session ran on and get
cleaned up; the auto-saved per-run markdown logs (`SaveSessionLogFile`, see
"Automatic Log Saving" above) accumulate in the project and travel between
machines — measured against a real 6207-run/231 MB corpus, they are the only
practically available history at any scale. `ParseLogFile(path)` parses
`store.RenderExport`'s `"md"` format into the same `Trajectory` type LN-01
produces, so every downstream consumer (signatures, LN-03's Actions tab,
candidate mining) is backend-agnostic already.

The one hard part is rebuilding the tool_use↔tool_result binding the log line
sequence itself doesn't guarantee: a naive "the next line is the result" rule
covers only 87.4% of calls in the measured corpus. `ParseLogFile` keeps a FIFO
queue of pending tool_use steps instead — each `tool` entry enqueues, each
`tool_result`/`error` entry dequeues the *oldest* pending call, not "the next
line" — which correctly handles parallel tool_use batches (several `tool`
entries land back to back, then their results, in the same order). Two more
things must not break that queue: `tool_progress` heartbeats (long commands
log raw `{"type":"tool_progress",...}` JSON at `system` level — 9.2% of one
measured corpus; `internal/session/parser.go` doesn't parse this event type
yet, so it falls through to a plain `system` log entry — a defect for that
parser to fix separately, not this ingest layer, which must simply tolerate
the noise) are skipped without touching the queue, and a failed call is
logged at `error` level instead of `tool_result` but still closes the queue
the same way, additionally marking the step's `ResultIsError`. Everything else
(`text`, `user`, `result`) flushes the queue — those mark a turn boundary, not
a call boundary.

**Bulk import** (`internal/experience/indexer.go`, `IngestDir(store, root,
project, opts)`) recursively imports a whole log directory — `.jsonl` via
`transcript.Read`, `.md` via `ParseLogFile` — into `action_signatures`, so a
corpus that predates this app's own live indexing (LN-03, below) is not lost.
Rows are written with no `session_runs` row to point at (`RunID = nil`) and
`CLISessionID` set to the file's own name, since a markdown log carries no
CLI session UUID. Idempotent by design: `store.IsLogFileImported`/
`MarkLogFileImported` (`imported_logfiles` table) dedup by `(project, name,
size, mtime)` — a closed, complete log file either was imported or wasn't,
unlike a live JSONL transcript's byte-offset checkpoint (`ingest_state`) — so
re-running `IngestDir` over a directory it already covered inserts nothing
twice. `ImportOpts.OnProgress(processed, total)` reports as it walks, since an
import of thousands of files with no feedback reads as a hung process. Wired
into `app.go`/a frontend button as `ImportProjectLogs` (LN-20, below) — bulk
re-indexing an old corpus is an occasional maintenance action, not part of the
per-run flow LN-03 wires up next.

**"Import logs" button** (LN-20, `internal/session/manager.go`
`SessionManager.ImportProjectLogs`, `app.go` `App.ImportProjectLogs`). LN-17
shipped `IngestDir` with no UI to call it — `grep -rn IngestDir` found only
its own definition and tests, so a project's accumulated `.claude-manager/logs/`
corpus (measured on Lumen: 5791 files, 245 MB, 946 importable runs) sat
invisible to every downstream consumer (Actions tab, candidate mining) until
this. `App.ImportProjectLogs(project, dir string) (session.ImportStats,
error)`: an empty `dir` imports the project's own
`<project>/.claude-manager/logs/`; otherwise `dir` is expected to come from
`PickDirectory` (a corpus brought from another machine). Gated on the same
`[optimization] experience_tracking` flag as the rest of the experience layer
— off, or no store configured, is an error rather than a silent no-op, since a
click that does nothing must still be visible to whoever clicked it.

`SessionManager` cannot import `internal/experience` directly (the same cycle
`ActionIndexFunc`/`PrimerFunc` avoid — `internal/experience` already imports
`internal/session` for `Step`), so `session.ImportStats` mirrors
`experience.ImportStats` field-for-field and `session.ImportLogsFunc` is the
function type `SessionManager.importLogsFn` holds (`SetLogImporter`); `app.go`
wires it to a closure over `experience.IngestDir`. Progress streams through
the same `Emitter` every other long-running call uses
(`plan:roadmap_progress`, `skill:progress`) as `experience:import`
(`{project, processed, total}`) — importing thousands of files with no signal
otherwise reads as a hung UI.

**UI** (`ExperiencePanel.svelte`): an "Import logs" button next to "Refresh"
in the header opens a two-item menu — "This project's logs" (empty `dir`) or
"Choose folder…" (`PickDirectory`, then the chosen path) — followed by a
progress line while the import runs and a one-line `ImportStats` summary
(files/runs/actions/skipped/errors) once it finishes. A repeat import of the
same directory is a cheap no-op by construction (`IsLogFileImported` dedup,
LN-17) — the summary's `skipped` count is what makes that visible rather than
looking like nothing happened.

**Why live-ingest saw ~0.2% coverage** (LN-21, `internal/session/session.go`,
`internal/experience/indexer.go`, `internal/session/manager.go`). A real-DB
measurement (2026-09-15) found only 21 of 9933 `session_runs` with any
`action_signatures` row — not a transcript-lookup or missing-`cli_session_id`
problem (checked directly: 100% of the sampled runs carried a `cli_session_id`,
and `FindTranscript` located the file for 99.6% of them). The actual cause was
an operation-ordering bug in `Session.Run()`'s `default:` (successful task
completion) case: `s.CLISessionID` was rotated to the **next** task's fresh
UUID *before* `s.emit(SessionEvent{Type: EvtTaskDone, ...})`, and
`SessionManager.finishRun` read `ms.session.CLISessionID` after that emit —
so `IngestRun` was handed the next, not-yet-launched process's id on every
completed autonomous task, and `FindTranscript` correctly failed to find a
transcript that didn't exist yet. `error`/`stopped` runs were unaffected
(their own event fires before any rotation happens on those paths).

Fixed by moving the rotation to *after* the emit and carrying the just-finished
run's own id on the event instead of reading it back off the (long-lived,
mutated-in-place) `Session`: `SessionEvent.CLISessionID`, populated with a
value captured before rotation. `finishRun(ms, status, errMsg,
finishedCLISessionID string)` takes that id explicitly for the `completed`
path (from `ev.CLISessionID`); the `error`/`stopped` paths still pass `""` and
fall back to `ms.session.CLISessionID`, which remains correct there.
`TestRun_EvtTaskDone_CarriesFinishedRunsOwnCLISessionID`
(`internal/session`) drives two real `fakeclaude` processes through an
autonomous loop and asserts each `EvtTaskDone.CLISessionID` matches the id
*that task's own* process launched with — verified to fail against the old
ordering and pass against the fix.

**Markdown-log fallback.** `IngestRun` gained an `mdLogPath` parameter — this
run's own auto-saved markdown log (`store.SaveSessionLogFile`, written by the
same `finishRun` call) — used when `FindTranscript` can't locate the CLI's own
JSONL transcript (cleaned up, or relocated under a worktree-specific project
slug). `finishRun` now runs the log autosave and the indexer call
sequentially in one goroutine (previously two independent fire-and-forget
goroutines with no ordering guarantee) so the autosaved path is available for
the fallback. Since a markdown log carries no byte offset to resume from
(`ParseLogFile` always parses the whole file, LN-17), the fallback's own
idempotency is tracked by recording the md file's own path and size in
`ingest_state` instead of a transcript byte offset — a repeat call for the
same run recognizes "already consumed this exact file"
(`IngestResult.Reason == ReasonAlreadyIngestedMD`) and is a no-op rather than
re-parsing and duplicating rows.

**Outcome is now distinguishable, not just discarded.** `IngestRun` returns
`(IngestResult{Rows, Reason}, error)` instead of a bare `error` —
`ReasonNoCLISessionID` (no `system/init` ever arrived, nothing to index, not
an error), `ReasonFallbackMD`, `ReasonAlreadyIngestedMD`, or `""` for an
ordinary transcript-backed ingest. `finishRun` logs every outcome as
`experience.ingest_run` (`project`, `cli_session_id`, `rows`, `reason`) so
"nothing to index" and "failed to index" no longer look identical in
`app.log`.

**Live indexing + the "Actions" tab** (LN-03, `internal/experience/indexer.go`
`IngestRun`, `internal/store/store.go` `TopSignatures`/`ActionSamples`).
`IngestRun(st, project, sessionName, runID, cliSessionID, projectPath,
taskPtr, mdLogPath)` is the per-run twin of `IngestDir` above: it locates the CLI's own
JSONL transcript (`FindTranscript`), resumes from the byte offset
`GetIngestOffset` last left off (LN-02's `ingest_state`), and inserts the
newly-appended `tool_use` steps with a real `run_id` — unlike a bulk-imported
row, a live run always has a `session_runs` row to point at. Called from
`SessionManager.finishRun` (the same choke point the log autosave and daily
metrics update already use), in the same fire-and-forget goroutine as the log
autosave (LN-21: sequential, so the autosaved path is ready as this call's
`mdLogPath` fallback), gated on `[optimization] experience_tracking` (default off) so the flag being off
means a transcript is never opened at all, not just that the result is
discarded — `SessionManager.SetActionIndexer` takes the indexing function as
an `ActionIndexFunc`, wired from `app.go` to `experience.IngestRun`, rather
than `internal/session` importing `internal/experience` directly: the latter
already imports `internal/session` (for `Step`/`TokenUsage`), so a direct
import back would cycle — the same `Emitter`-interface trick the control-plane
integration uses (see "Testing & Control Harness" below).

`Store.TopSignatures` aggregates rows into the `SignatureStat` list the
"Actions" tab (`ExperiencePanel.svelte`) renders — count, distinct runs, error
rate, summed output tokens, sample args, most frequent signature first.
`DistinctRuns` counts by `COALESCE(run_id, cli_session_id)`, not bare
`run_id`: a bulk-imported row's `run_id` is `NULL`, and plain `COUNT(DISTINCT
run_id)` silently drops every `NULL` — a signature that only appears in
imported history would otherwise report zero distinct runs instead of falling
back to counting by the file-derived `cli_session_id`. `Store.ActionSamples`
returns full rows for one `(project, sig)` pair, most recent first — the
click-through from an aggregated signature to concrete examples. Both are
exposed as `App.GetTopActions`/`App.GetActionSamples`; like
`ApproveRoadmap`/`GenerateClaudeMdSession` above, they are Wails-only, not on
`control.AppAPI` — `cmd/playwright-server` runs `SessionManager` with a `nil`
store (see its own doc comment), so there is nothing for a control-plane RPC
to read here regardless, the same reason `History.svelte`/`CostDashboard.svelte`
have no real-data Playwright coverage (GUI-TESTS.md HI-01, CD-01 are still
"○"). `frontend/tests/experience.spec.ts` covers what the harness actually
supports: the modal opening, the project/period pickers, and the panel's own
empty-state message when `GetTopActions` returns nothing (stubbed in
`helpers/bridge.ts`) — plus a real backend round-trip for the Settings
"Experience layer" checkbox through `UpdateConfig`/`GetConfig`.

**Permission-rule suggestions** (LN-04, `internal/experience/allowlist.go`).
The cheapest win in the whole experience layer: a command the agent runs in
every run and that waits on `waiting_permission` every single time, because no
rule covers it yet. `permission_events` (`internal/store/migrations.go`) is a
new table, not a repurposing of `action_signatures` — it records the
*resolution* of a permission_request (tool, pattern, decision, who decided
it), which `action_signatures` has no room for. `SessionManager.
recordPermissionEvent` writes a row from both call sites that already resolve
a request: `handlePermission` (a config rule, a runtime rule, or
`bypassPermissions` — `Auto=true`) and `RespondPermission` (a human answer —
`Auto=false`). Both are recorded, not just the human ones, so a later query
can tell "this still needs asking" (`auto=0`) apart from "a rule already
covers this" (`auto=1`) and never re-suggests a rule that already exists.
Gated on the same `[optimization] experience_tracking` flag LN-03 uses (off by
default) — permission decisions carry far less of a privacy risk than a full
transcript, but the invariant in LEARN-TASKS.md is that *every* new piece of
history collection is opt-in, and reusing the existing flag needed no new
config surface.

`Store.TopPermissionEvents` aggregates rows by `(project, tool, pattern)` over
a trailing window, counting `allow`-ish vs `deny`-ish decisions — the
precondition for a suggestion is that the human has been saying yes
consistently, not just often. Only `auto=0` rows are counted: an `auto=1` row
means a rule already resolves that pair automatically, so it must not inflate
`Count` into looking like it's still an open ask. `experience.Candidates`
turns those stats into `PermissionCandidate`s, dropping anything with fewer
than `MinPermissionCount` (2) occurrences or with as many/more denials than
allows — this feature only ever proposes *allow* rules, never a deny.

**The classifier is a hard whitelist, never a heuristic score**
(`ClassifyPermission`): `Read`/`Grep`/`Glob` are always safe (nothing they do
can mutate anything, whatever the pattern); every other tool but `Bash`
(`Edit`, `Write`, `McpTool`, ...) always requires a human, because there is no
whitelist here for anything that writes. A `Bash` command is safe only when
it contains none of a fixed list of dangerous substrings (`rm`, `mv`, `>`/`>>`,
`curl`, `wget`, `ssh`, `sudo`, `git push`/`reset`/`checkout --`, `--force` —
matched with word boundaries, so "term" doesn't trip the `rm` check) *and*
every chained/piped segment matches one of the explicit read-only prefixes
LEARN-TASKS.md LN-04 lists (`ls`, `cat`, `head`, `tail`, `sed -n`, `grep`,
`rg`, `find`, `git status`/`log`/`diff`/`show`, `go build`/`test`/`vet`,
`cargo check`/`build`/`test`/`clippy`, `npm test`/`run build`). An unmatched
command is unsafe by default — the classifier never guesses, and a command
that merely *looks* safe (e.g. `git log && rm -rf /`) is rejected because the
dangerous-substring check runs on the whole line before it is ever split into
segments.

**Never auto-applied.** `Candidates` splits its output into `Safe` (cleared by
the classifier, offered an "Add rule" button) and `NeedsReview` (frequent and
consistently allowed, but not whitelisted — Edit/Write, or a Bash command
outside the list) purely for display; nothing in this path writes a rule on
its own. `App.AddPermissionRule` → `addPermissionRuleInConfig` is the actual
write, and it goes through the exact `GetConfig`→mutate→`UpdateConfig`
round-trip every other config edit in this app uses, appending to the target
session's own `PermissionRules` (a duplicate `{tool, pattern, decision}`
triple is a no-op). `ExperiencePanel.svelte`'s "Permissions" tab renders both
lists, with a per-row session `<select>` (defaulting to the project's first
configured session) next to each `Safe` row's "Add rule" button.

**Context primer** (LN-05, `internal/experience/primer.go`). The main token
sink in an `auto_restart` loop: every fresh CLI process re-discovers from
scratch what the manager already knows — the current task, the repo's git
state, what the previous run just touched. `SessionConfig.ContextPrimer`
(`context_primer`, default false) prepends a `BuildPrimer(PrimerInput)` block
to the plain-prompt branch of `Session.initialPromptText`, capped at
`MaxPrimerChars` (2000): current task (`taskSourceDesc`), git state (main
branch, remote presence/name, current branch, `git log --oneline -3` — direct
`exec` with a 3s timeout; a failed or missing `.git` just omits the section,
never blocks the start), files the session's previous run touched
(`store.ListRuns(project,session,1)` → `ActionsForRun`, Edit/Write args), and
the project's `Gates` commands. `truncateSections` drops whole trailing
sections — lowest priority first — rather than mid-section, so a huge gate
list can never crowd out the current task. Sections 5 (files re-read 3+
times) and 6 (journal `avoid` lines) are not wired in yet: LN-08's
`experience.MineCandidates` (see "Candidate mining" below) mines re-read
patterns project-wide for skill distillation, but nothing yet turns that into
a *this-session's-previous-run* signal for the primer, and LN-06 (see
"Project journal" below) deliberately doesn't touch this file — feeding
`LastEntries`/re-read files into `BuildPrimer` is left to whichever task
explicitly takes it on.

**Wired like `ActionIndexFunc`, for the same reason.** `internal/experience`
already imports `internal/session` (for `Step`/`TokenUsage`), so
`session.go` cannot call `experience.BuildPrimer` directly. `session.PrimerFunc`
is a plain-argument function type (`project, sessionName, projectPath,
taskDesc string, gates []string) string`) stored on `Session` (via
`Params.PrimerFn`) and on `SessionManager` (`SetPrimerBuilder`, mirroring
`SetActionIndexer`); `app.go` wires it to a closure over
`experience.BuildPrimer` plus the store. Crash recovery and the "no pending
tasks" interactive fallback (see "Task Source Check" above) are never
primed — they already have their own dedicated prompt text, and the primer
would be redundant with (or contradict) the crash-recovery reconciliation
step. With the flag off or no `PrimerFn` wired, `initialPromptText` is
byte-identical to before this feature existed.

**Project journal** (LN-06, `internal/experience/journal.go`,
`internal/analysis/journal.go`). Episodic memory between sessions: one short
distilled entry per completed task, so the next run inherits "what got done,
what was surprising, what to avoid" instead of re-deriving it from a diff and
a task pointer. Per-project opt-in — `ProjectOverlay.Journal`/`JournalCommit`
(`journal`/`journal_commit`, private `config.local.toml` layer like
`mixed_programming` — see "Config Layering" above): enabling means an extra
haiku CLI call after every completed task, which a teammate cloning the repo
should opt into themselves, even though (unlike a mixed-programming brief) the
entry itself never leaves the machine.

**Trigger and distillation.** `SessionManager.finishRun`, gated on
`status == "completed"` and the project's own `Journal` flag, spawns a
fire-and-forget goroutine (`logger.Recover`-guarded, same pattern as the LN-03
indexer above) that calls `analysis.GenerateJournalEntry` (haiku — cheap,
called after every task) with the run's task pointer
(`Session.taskSourceDesc`), the Edit/Write file paths from *this run's own*
buffered log entries (`filesChangedFromLogs` — the in-memory equivalent of
`primer.go`'s `previousRunFilesSection`, read directly off `finishRun`'s
`logs` slice rather than `action_signatures`, since LN-03's ingest of this
same run is a separate async goroutine that may not have completed yet), and
the turn's final `result` text (`managedSession.lastResultText`, set in
`handleResult`). The distillation schema (`JournalJSONSchema`/
`JournalSystemPrompt`) asks for `{done, surprises, avoid}` — `surprises`/
`avoid` are meant to stay empty on an unremarkable run rather than be padded
out, so a "nothing to report" task doesn't manufacture noise.

**Format and rotation** (`AppendEntry`, `internal/experience/journal.go`):
appends one markdown section to `<project>/.claude-manager/journal.md`,
atomically (tmp → rename):
```markdown
## 2026-09-08 — ROADMAP.md:92
**Сделано:** …
**Неожиданно:** …
**Не делать:** …
```
`surprises`/`avoid` join multiple items with `"; "` on their one line; empty
renders as `—`, never a blank line. Past `MaxJournalEntries` (50) sections,
the oldest overflow is rotated out into `journal-archive-<YYYY-MM>.md` —
grouped by each archived section's own month, not the month rotation happens
to run in, so a journal that has been rotating for a year still reads as one
file per month of history. Gitignored via `config.EnsureGitignore` unless
`JournalCommit` is set, mirroring the auto-saved log files' own
gitignore-by-default convention (see "Automatic Log Saving" above) —
`LastEntries(projectPath, n)` reads the tail back for a future consumer (the
primer's deferred "avoid" section, LN-08's re-read signal) without needing to
know the rotation boundary.

**Wired like `ActionIndexFunc`, for the same reason.** `finishRun` cannot call
`experience.AppendEntry` directly (same import-cycle constraint as
`ActionIndexFunc`/`PrimerFunc` above), so `session.JournalEntry` mirrors
`experience.Entry` field-for-field and `session.JournalWriteFunc` is the
function type `SessionManager.journalFn` holds; `app.go` wires it to a closure
over `experience.AppendEntry`. Unlike `indexRun`/`primerFn`, the distillation
call itself (`journalAnalyzeFn`) defaults to the real
`analysis.GenerateJournalEntry` in `NewSessionManager` rather than starting
nil — gating is entirely the project's `Journal` flag plus `journalFn` being
wired, checked in `finishRun` before the goroutine is even spawned, so a
disabled flag means the analyst is never invoked, not just that its result is
discarded.

**Failure clusters** (LN-07, `internal/experience/failures.go`). The
highest-quality signal in the whole experience layer: a documented "it failed
→ the very next attempt fixed it" pair is exactly the knowledge a fresh
session is missing, ranked above any frequency count. Two sources feed the
same `[]FixPair`: source A (`ExtractFromMixedTasks`) reads
`worker.MixedTask.Rounds` — a round whose gate failed followed by one that
passed, pairing the gate's own captured output (`GateResult.FailedCommand()`,
never the model's own claim) with the files the very next round's patches
touched; source B (`ExtractFromActionRows`/`ExtractFromRun`) reads
`action_signatures`, grouped by run (`COALESCE(run_id, cli_session_id)`, the
same fallback `TopSignatures.DistinctRuns` uses) and scanned for a row with
`IsError` followed within `FailurePairWindow` (5) steps by a row sharing the
same `Sig` with no error — evidence the agent adjusted a flag or environment,
with both verbatim `arg` strings kept since their diff *is* the rule. Source B
has no raw output text (`action_signatures` stores only the normalized
signature and arg), so `Cluster` falls back to keying on `FailedArg` whenever
a pair's `Output` is empty.

`ErrorKey` normalizes one line of failure text into a comparable cluster key:
first line only, absolute paths masked (`<PATH>`, both Windows drive-letter
and POSIX forms), digit runs masked to `N`, capped at 80 runes — without this
the same `fatal:` reported from two machines/worktrees/files never merges
(measured on the corpus: `File content (N tokens) exceeds maximum allowed
tokens` collapses 197+42 raw occurrences into one cluster). `Cluster` groups
`FixPair`s by `ErrorKey`, ranked by `DistinctRuns` (not raw `Count` — a
cluster hit many times in one run is not corroborated the way one hit across
three runs is), capped at `maxClusterExamples` (5) samples per cluster. Output
feeds LN-09's distillation prompt (a high-priority input: "fact in CLAUDE.md"
beats a skill for a one-line remedy) and the planned read-only "Failures" tab
in `ExperiencePanel` — this task does not wire either consumer.

**Failed-gate output is a supported but currently unpopulated input.**
`analysis.JournalInput.FailedGateOutput` exists for a caller that runs its own
gates (see MIXED-TASKS.md's `worker.GateResult`), but a manager-driven
`task_source` session runs its own gates *inside* the CLI conversation per
`docs/git-workflow.md` — the manager has no structured signal of a gate
failure to pass here, so `finishRun`'s wiring leaves it empty rather than
scraping log text for a heuristic that would be unreliable either way.

**Candidate mining** (LN-08, `internal/experience/candidate.go`). Picks out
which recurring tool-call sequences are actually worth distilling into a
skill (LN-09) — naive n-gram mining over the whole corpus is mostly noise, so
`MineCandidates` slides contiguous n-grams (length 1..`maxNGram`=4) over each
run's ordered `store.ActionRow`s and keeps a sequence only when it recurs in
at least `MinCandidateRuns` (3) runs *and* at least `DefaultMinRunShare` (5%)
of the project's total runs — a relative bar, not an absolute one: a fixed
`min_runs=3` measured 719 signatures on a 5654-run corpus, two orders of
magnitude past what's worth dictating a prompt over, while 5% keeps the
shortlist to ~30-40 candidates on a large project and ~15 on a small one.

**A loop is a symptom, not a habit, and must not inflate the candidate.**
`loopSignatures` finds, per run, any signature whose underlying (tool, arg)
pair repeats `loopThreshold` (3, matching `optimization.LoopDetector`'s own
threshold) times *identically* within that run; a candidate built from such a
uniform, self-repeating n-gram is flagged `ContextLossSuspect` rather than
promoted quietly — a repeated `Read` of the same file is context loss, not a
workflow worth a skill. Repeating the same signature with *different* args
(reading three different files) is not a loop and never sets the flag. Either
way a run only ever contributes 1 to `DistinctRuns` no matter how many times a
sequence repeats inside it — MineCandidates only asks "did this run see the
sequence at all", not "how many times".

**Score cannot be pulled up by a failed run.** Each contributing run has an
`outcomeWeight` (LN-08: 1.0 completed, 0.3 stopped/rate_limited, 0.0 error,
0.6 neutral for a run with no resolvable status — a bulk-imported row, LN-17,
has no `run_id` to look up). The spec's formula is `distinctRuns *
log(1+rediscoveryChars) * outcomeWeight`, where `outcomeWeight` there is the
*average* per-run weight — since `distinctRuns * average = weightSum`
algebraically, `candidate.go` computes `Score` directly as `weightSum *
log(1+rediscoveryChars)`, with both `weightSum` and `rediscoveryChars`
already accumulating each run's own weight. That makes "a failed run cannot
teach a good pattern" exact rather than approximate: an `outcomeWeight=0` run
contributes literally nothing to either term, so adding one to a candidate's
evidence changes `DistinctRuns` (an honest, unweighted occurrence count) but
never `Score` — the number that ranks candidates for the distiller.
`rediscoveryChars` itself is the `result_chars` a session would have to read
through, from the start of the run to the sequence's first occurrence, to
rediscover the pattern on its own — summed once per contributing run, not per
occurrence, matching `result_chars`' own role as the primer's (LN-05) and the
duration profile's (LN-18) real cost proxy.

**Nested n-grams dedup to the longest.** If a 2-gram `[A B]` never occurs
without also being part of a 3-gram `[A B C]` — same `DistinctRuns` on both —
the shorter one is dropped: wherever it fires, the longer one already covers
it, so keeping both would just double-count the same evidence in the
shortlist. A shorter sequence survives whenever its own `DistinctRuns` is
*not* matched by any longer sequence that contains it — it is genuinely more
frequent on its own.

**Frequency alone cannot say what a candidate is *for*.** A 1-gram occurs in
at least as many runs as every n-gram containing it, so it necessarily
outscores the sequences it is part of — measured on a real corpus, the whole
head of the ranked list was `Bash:sed -n <ARG>`, `Bash:git status --short`,
`Bash:grep -n <ARG>`, `Bash:ls <ARG>`, none of which can become a procedure:
a skill teaches an *order* of steps, and one command has no order.
`classifyCandidate` therefore labels every candidate with a
`CandidateKind` — a hard rule set, never a heuristic score, the same stance
`ClassifyPermission` (LN-04) takes: a uniform gram flagged as a loop is
`KindNoise` (the context-loss symptom `ContextLossSuspect` already names —
distilling it would teach the symptom back); any 2+ step sequence is
`KindSkill`; a single step with a `RelatedFailures` cluster is `KindSkill`
too (a documented "fails like this → fixed like that" needs no second step
and is the highest-value output of this whole pipeline); a single step whose
*every* sample is cleared by `ClassifyPermission` is `KindPermission` —
belongs in the Permissions tab, where one fewer prompt per run is a win a
skill cannot deliver; anything else single-step stays `KindSkill`, ranked
below the sequences. Rule 4 classifies the samples' **verbatim args**, never
the signature: a masked signature's own `<ARG>` angle brackets trip
`ClassifyPermission`'s redirection check, so a signature-based check would
call every candidate unsafe.

1-grams are deliberately still mined rather than dropped (`for n := 1`) —
they carry the frequency evidence the run-share threshold is built on, and
the failure-attached ones are the best skills there are. `Score` is also left
untouched (LN-22's weighting invariants and LN-23's threshold are defined on
it); what changes is the *sort*, which now puts `kindRank` ahead of `Score`,
and `ResolveSkillMinScore`, which computes its top-N% cutoff over the
`KindSkill` candidates only — a percentile of a population that is mostly
one-liners no one would distill is not a calibration (it falls back to the
full list when no candidate carries a Kind, e.g. a direct/test call).
`SkillReview.svelte` renders the verdict as a "Worth as" column (the reason
code localized into a tooltip) plus a "Skill candidates only" filter; nothing
refuses to distill a `KindPermission`/`KindNoise` row — the label is advisory.

**RelatedFailures is a best-effort hint, not a guarantee.** A `FailureCluster`
(LN-07) is attached to a candidate when one of the cluster's own kept
`Examples` (capped at `maxClusterExamples`=5) has a non-empty `FixPair.Sig` (the
failed call's signature, source B only) that is one of the candidate's own steps
(LN-32) — sharing a run is not enough, a frequent command would collect dozens of
unrelated failures. A large cluster whose matching example got capped away is
silently missed; acceptable for a pointer in LN-09's prompt, not a correctness
requirement. A candidate also keeps its best occurrences (`Occurrences`: every row
of the n-gram, heaviest by `stepOccurrenceWeight` then newest, at most
`maxCandidateSamples`); `Samples` is their first rows, and the distiller gets one
multi-line sample per occurrence.

**Skill distillation** (LN-09, `internal/analysis/skill.go`). Turns one LN-08
`SkillCandidate` into a draft `SKILL.md` — the payoff the whole mining chain
(LN-02 → LN-07/08) has been building toward: a recurring pattern becomes a
procedure a future session loads on demand instead of rediscovering.

**`GetSkillCandidates(project)` is what actually produces a `SkillCandidate`
to feed `DistillSkill` — until this was wired, LN-08's `MineCandidates` was
reachable from nothing.** `App.GetSkillCandidates` →
`experience.MineProjectCandidates` (`internal/experience/candidate.go`) loads
a project's last `candidateWindowDays` (90) days of `action_signatures` via
`Store.ActionRowsForCandidates` (a `LEFT JOIN session_runs` so each row
carries its own run's `status`, empty for a bulk-imported row exactly as
`outcomeWeight`'s default case expects), folds them into `CandidateRun`s via
`BuildCandidateRuns` (grouping by the same `COALESCE(run_id,
cli_session_id)` key LN-07's `actionRunKey` already uses), mines LN-07
failure clusters from the same rows for `RelatedFailures`, and calls
`MineCandidates`. The "Candidates" section of `SkillReview.svelte` (above the
draft/approved list) renders the result with a per-row "Distill" button and a
model `<select>` — clicking it calls `DistillSkill` with that exact candidate,
the project's own `Gates` (read via `GetConfig`), and `minScore=0` (the
backend's own `DefaultSkillMinScore` default). A `below distillation
threshold` error is shown inline on that row rather than as a generic
failure. Nothing here persists the mined list — it is recomputed on every
Skills-tab load, so a candidate stays offered until either enough runs erode
its score/share below threshold or someone distills it (which does not
remove it from the list — the recurring pattern may still be worth
re-mining once more history accumulates).

**Decoupled from `internal/experience` by construction, not by convention.**
`internal/experience` already imports `internal/session` (for `Step`/
`TokenUsage`), so `SessionManager.DistillSkill` — which must exist to stream
`skill:progress` the same way `GenerateRoadmap` streams
`plan:roadmap_progress` — cannot take `experience.SkillCandidate`/
`FailureCluster` as parameters: naming those types in `internal/session`
would import `internal/experience` back, a straight cycle.
`analysis.SkillDistillInput`/`SkillSample`/`SkillFailureSummary` are `internal/
analysis`'s own plain shapes instead; `app.go` (which already imports both
`analysis` and `experience`) is the only place that translates a
`SkillCandidate` into one (`skillDistillInputFromCandidate`), and its own
`App.DistillSkill(project, candidate experience.SkillCandidate, gates
[]string, model string, minScore float64)` is the actual Wails entry point.

**`SkillSample.Output` is a documented gap, not an oversight.** LN-09's spec
asks for "up to 5 real examples (command + first 20 lines of output)", but
`action_signatures` (LN-02) stores only the normalized signature and the
verbatim `arg` — never raw tool output, by design, to avoid duplicating a
transcript's full result text into SQLite. Every sample built from an
`ActionRow` therefore carries only its command; `Output` stays as a distinct
field so a future caller with real output text (e.g. a source that re-reads
the original transcript for its samples) can populate it without a schema
change, and `buildSkillTaskText` already renders it, truncated to 20 lines,
whenever it is non-empty.

**Threshold, not a heuristic score.** `DistillSkill` refuses to invoke the CLI
at all — not just skip acting on a low result — when `SkillCandidate.Score`
is below `minScore` (`analysis.ErrBelowThreshold`, wrapped with the actual
score and threshold so the caller/UI can act on it): distillation is a paid
sonnet call. `analysis.DistillSkill`'s own `minScore <= 0` fallback,
`DefaultSkillMinScore`, is a fixed absolute constant kept only as a
last-resort default for a direct/test call with no candidate distribution to
work from — it is not what production wiring uses (see LN-23 below).

**Relative threshold, calibrated to the project's own distribution
(LEARN-TASKS.md LN-23).** `DefaultSkillMinScore`'s fixed `10.0` cannot work
across corpus sizes, because `Score` scales with
`weightSum*log(1+rediscoveryChars)`: on this app's own small DB it cut
*everything* (max score 6.9 of 24 candidates), and on an imported corpus two
orders of magnitude larger it let *almost everything* through (378 of 379,
p50=782). `App.DistillSkill` therefore resolves `minScore` itself
(`resolveSkillMinScore`) instead of forwarding a caller's `<= 0` straight to
`analysis.DistillSkill`: an explicit `minScore > 0` (a UI-entered override)
always wins outright; `<= 0` re-mines the project's current candidates
(`experience.MineProjectCandidates`) and calls
`experience.ResolveSkillMinScore`, which sets the cutoff to the top
`DefaultSkillTopFraction` (3%) of their scores via
`RelativeScoreThreshold` — a share of the *current* distribution, not an
absolute number, mirroring why `DefaultMinRunShare` (LN-08) is a share
rather than a count. `RelativeScoreThreshold` always keeps the
highest-scoring candidate regardless of how small
`topFraction*len(scores)` rounds down to, so a project with only one or two
candidates never loses all of them to rounding. `SkillReview.svelte`'s
"Min score" field next to the model picker defaults to 0 (automatic); a
non-zero value is the explicit override.

**Persistence** (`skills` table, `internal/store/migrations.go`): one row per
distillation, `status="draft"` — `internal/store.Skill` holds `DraftJSON` (the
`SkillDraft`), `MD` (`RenderSkillMarkdown`'s rendered body, kept alongside the
JSON so LN-10 can offer the exact review text without re-rendering) and
`SourceJSON` (the candidate's own `Sig` sequence — what LN-11 checks against
later runs to tell whether the skill actually got used). LN-09 only ever
inserts; approving/archiving a row (writing it into
`<project>/.claude/skills/<name>/SKILL.md`, updating `status`) is LN-10's job.

**Rendering** (`RenderSkillMarkdown`) follows the same minimal frontmatter
convention this app's own `.claude/skills/*/SKILL.md` files use — `name` +
`description` only, since `description` is the one line that stays
permanently in context and everything else loads on demand — with each body
section (`When to use`, `Gotchas`, `Files touched`) omitted entirely when the
draft left it empty, rather than rendered as an empty heading. A hard
`maxSkillBodyLines` (120) cap on the rendered output enforces the "≤120 lines"
invariant regardless of whether the model actually honored
`SkillSystemPrompt`'s instruction to stay under it, always preserving the
frontmatter block even if the truncation point would otherwise land inside it
— a truncated frontmatter is a broken skill file, a truncated body is merely
an incomplete one.

**Skill review/approval** (LN-10, `internal/experience/skillfiles.go`,
`SkillReview.svelte`). Closes the loop LN-09 leaves open: a `status="draft"`
row sits in SQLite until a human reviews it — nothing between distillation
and disk is automatic. The "Skills" tab in `ExperiencePanel.svelte`
(`GetSkills(project)`) lists every row for a project; clicking one opens
`SkillReview.svelte`'s review/edit panel — a markdown textarea (the same
Edit/Preview split as `PlanReview.svelte`, rendered through the shared
`renderMarkdown` + `.md-body`) seeded from the row's own `MD`, since a
reviewer may fix something before accepting.

**Writing the file** (`experience.WriteSkillFile`) is the mirror of
`analysis.WriteRoadmapFiles`: atomic (tmp → rename) write to
`<project>/.claude/skills/<name>/SKILL.md`, never gitignored — unlike the
auto-saved logs/journal, a skill is meant to be committed and shared. `name`
is not a parameter of `App.ApproveSkill(id, md, overwrite)` — it is always
the row's own `Skill.Name`, so an unsafe name (however a draft got one) can
never be smuggled in through the caller. `experience.ValidSkillName` enforces
`[a-z0-9-]` and `WriteSkillFile` additionally runs the resolved path through
a `confinedPath`-style check (mirroring `internal/analysis/roadmapview.go`'s),
defense in depth even though the name charset alone already excludes `.`/`/`.
Refuses to overwrite an existing file unless `overwrite` is true
(`experience.ErrSkillFileExists`), surfaced in `SkillReview.svelte` as the
same inline "already exists — overwrite?" banner `PlanReview.svelte` uses for
`ErrRoadmapFilesExist` — never `window.confirm()`.

**Archiving is metadata-only.** `App.ArchiveSkill(id)` sets `status=archived`
and never touches a file already written into the project — "В архив"
(LEARN-TASKS.md's own wording) removes the row from the Skills tab's active
list (`SkillReview.svelte` filters `Status !== 'archived'` client-side over
the same `ListSkills` rows the tab already fetches, rather than a second
query shape), it does not delete anything on disk. This is also the landing
spot for LN-11's "protuhla" suggestion below — proposing archival, never
auto-archiving.

**Skill effect measurement and staleness** (LN-11,
`internal/experience/skillquality.go`). The library of distilled skills only
grows; without a measured effect, nobody can tell whether a given skill is
actually saving tokens or just permanently taxing every future run's context.
`BuildSkillQualityReport(store, project)` — structured like
`worker/quality.go`'s `ModelQuality`, one row per skill — compares runs
**before** and **after** the skill's `approved_at`, restricted to *comparable*
runs: those with at least one step whose signature is in the skill's own
`source_json` (the candidate's mined `Sig` sequence, LN-09). Per side it
reports the **median** (not mean — LEARN-TASKS.md LN-11 calls out that per-run
cost is long-tailed, so one outlier run must not dominate the summary) of
`input_tokens` and `num_turns`, the completed-run rate, and the run count
itself.

**Imported runs never enter the measurement.** A bulk-imported row (LN-17) has
`run_id = NULL` and so no `session_runs` row to pull tokens/status from —
`store.RunsWithSignature` filters on `run_id IS NOT NULL` at the SQL level
(never relying on a `JOIN` that would silently drop them the same way), so a
skill whose only pre-approval evidence is imported history reports
`Before.Runs = 0` rather than a fabricated "before" baseline.

**Explicit "not enough data".** Fewer than `MinSkillEffectRuns` (3) comparable
runs on either side sets `InsufficientData = true`, and the UI draws no
conclusion for such a row — no median is meaningful over one or two runs, and
a false "improved"/"worse" verdict would be worse than no verdict.

**Staleness is a suggestion, never an automatic archive** — `ArchiveSkill`
(LN-10) stays a human's explicit click either way.
`evaluateStale` flags a skill in either of two cases: its signatures never
occurred at all among the project's last `StaleRunWindow` (20) runs
(`stale_reason = "unused"`), or it has at least `StaleMinRunsAfter` (5)
post-approval comparable runs whose median input-token cost did not drop
below the pre-approval median (`stale_reason = "no_improvement"` — requires a
non-empty "before" side, since there is nothing to say "did not decrease"
relative to with no baseline at all). "Unused" is checked first: a skill with
too little data to measure an effect *and* no recent occurrence at all still
reads as "propose archiving it", the case LN-11's own worked example (a skill
that never fired) calls out explicitly.

**Surfaced** as `App.GetSkillQuality(project)` → `[]experience.SkillEffect`,
rendered by `SkillReview.svelte` as a table above the drafts/approved list —
per-row before/after (runs / median tokens / median turns / completed %) plus
a verdict cell ("OK", "Suggest archiving (unused)"/"(no token improvement)",
or "Not enough data" — which always wins over a stale flag, mirroring the
backend's "no conclusion" rule).

**Token attribution by tool** (LN-12, `internal/experience/attribution.go`).
"How much did the session cost" is visible; "which call ate the context" was
not. Measured against the lumen/tbank corpora: `Read` alone accounts for
52-62% of all tool-output volume (lumen: 64.7M of 124.4M chars, 11 136 calls,
~5809 chars ≈ 1450 est. tokens per call on average), with `sed -n` and `grep`
next — and 197 times in the corpus the model reads a file whole, gets "File
content exceeds maximum allowed tokens. Use offset and limit", and reads it
again, which is the first candidate this report makes obvious enough to turn
into a skill.

`EstimateTokens(chars int) int` is `chars/CharsPerToken` (4) — a documented
order-of-magnitude approximation, not a precise count (there is no tokenizer
without hitting the API); `chars<=0` returns 0 so a call whose
`result_chars` was never captured contributes nothing rather than a garbage
value. `store.ActionResultChars(project, days)` returns every
`action_signatures` row's `(sig, tool, result_chars)` for the window,
*including* the zero-result rows — unlike `ActionDurations`, they still
happened and must count toward `Count`, they simply add 0 to the token sum
(the explicit "zero must not break Share" test case LEARN-TASKS.md LN-12
calls out).

`BuildAttributionReport(st, project, topN)` aggregates those rows into two
cuts: `BySignature` (most expensive first, capped at `topN`) and `ByTool`
(never capped — a project only ever uses a handful of tools).
`TotalEstTokens` — and therefore every row's `Share` — is computed from
*all* signatures seen in the window before `topN` truncates the reported
list, so a truncated report's percentages still describe the whole
project's volume, not just what's shown. Surfaced as
`App.GetTokenAttribution(project, topN)` → `experience.AttributionReport`,
rendered by `ExperiencePanel.svelte`'s "Cost by tool" tab (a by-tool summary
table above the by-signature list), same fixed 90-day window and empty-state
convention as the "Timing" tab (LN-18) next to it.

Feeds LN-08's `rediscoveryChars` (a candidate's evidence is weighted by how
many result chars a session would read through to rediscover the pattern on
its own) and is raw material for LN-16's cost-regression alerts — this task
does not wire either consumer.

**Re-read price and session length** (LN-29, same file). Volume is not cost:
a result is written to the prompt cache once and read back on every later
model call of its session, and re-reading was 65% of spend in the study
behind `docs/Science_lab`. `store.ActionResultChars` therefore also returns
`Remaining` — steps of the same CLI session after the row (a window
`MAX(step_index) OVER (PARTITION BY cli_session_id | run_id | session)`
minus its own `step_index`; another CLI session of the same manager session
is a fresh conversation and never counts). Each row is priced
`EstimateTokens × (cacheWriteWeight 2.0 + cacheReadWeight 0.1 × Remaining)`
in uncached-input-token units (a 1-hour cache write is 2× the input price, a
read 0.1×; steps stand in for model calls, each tool result is followed by
one) — `LifetimeTokens`/`LifetimeShare` on both cuts, which are now sorted by
it (volume breaks ties, so a report without step spread keeps the old
order). On Lumen's history it moves `Bash` from 37% of volume to 26% of
cost and `read_file` from 8% to 11%. `ByLength` buckets the project's runs
with a known cost (`store.RunLengthCosts`: `session_runs.num_turns`,
`total_cost_usd > 0`) into the study's bands (1–20 … 251+ turns), so the
in-app numbers compare directly with `docs/Science_lab/MEASURE.md` and show
whether `handoff_context_tokens` (LN-28) moved spend out of the long bands.
The tab adds two columns and the length table; a report from before LN-29
(fields absent) renders "—".

**Duration profile** (LN-18, `internal/experience/duration.go`). A fresh
session has no idea how long this project's own slow commands take, and
finds out the only way it can — by hitting a timeout. The manager already
knows: every ingested tool call's `ts` (LN-01/17) brackets its own
tool_use→tool_result gap. `Step.ResultTime` (set by both ingest backends —
`processUser` for JSONL, the `tool_result`/`error` case for markdown logs)
captures the result's own timestamp alongside the existing tool_use `Time`;
`stepDurSec` rounds their difference to whole seconds, or reports 0 when
either timestamp is missing (no result ever arrived within the ingested
window) — 0 means unknown, never "instant", and `store.ActionDurations`
filters on `dur_sec > 0` for exactly that reason so an unknown call can never
drag a median toward zero. The column is additive
(`action_signatures.dur_sec`, same tolerate-duplicate-column `ALTER TABLE`
pattern as `task_plans.kind`) and filled at ingest time for both the bulk
import (LN-17) and the live per-run indexer (LN-03) — one code path
(`actionRows`) feeds both.

`DurationProfile(st, project)` groups `store.ActionDurations`' rows by
signature and reports median/p90/max/total/fail-rate, dropping any signature
under `MinDurationSamples` (10) — a median of two calls is noise, not a
profile. Measured against a real corpus (lumen, 79k calls): `git worktree add
-b` averaged 120s with 31 failures out of 90 calls, `sleep <ARG>` alone
accounted for ~28 hours of total wait across 953 calls — the kind of thing a
session has no way to know without this.

`durationSection` renders the primer's (LN-05) "Command timing" block: one
line per signature whose median clears `durationThresholdSec` (60s), capped
at `maxDurationLines` (5) so the primer's own token budget is never crowded
out by a long tail of slow commands. `sleep` gets a dedicated line instead of
the generic timeout tip — a high `sleep` median is not "this command is slow
and needs a bigger timeout", it's the "wait for a background task"
anti-pattern that cannot work in this session model at all (see
"Background-Task Warning" above: stdin closes at turn end, so nothing is left
to wait out the sleep). `App.GetDurationProfile` exposes the same data to
`ExperiencePanel.svelte`'s "Timing" tab — the human-readable twin of the
primer section, gated on the same `[optimization] experience_tracking` flag
LN-03/04 use (no new config surface needed: `dur_sec` is derived from
timestamps ingestion already collects when that flag is on).

**Cost-regression alerts** (LN-16, `internal/experience/regression.go`).
Invariant 5 ("any feature that changes the prompt must be measurable") is
only honored if a regression is actually surfaced somewhere — otherwise a
protuhla'd skill (LN-11), a bloated primer (LN-05) or a growing journal tail
(LN-06) just quietly taxes every future run.

`DetectRegression(st, tracker, project, sessionName, projectPath, taskDesc,
gates, runID)` compares a just-finished run's `total_cost_usd`/`input_tokens`
against the trailing median of the *same session's* last `RegressionWindow`
(10) finished runs (any status — the baseline is cost history, not just
completed runs). `RegressionFactor` (2.0) crossed by either metric — "или
входной контекст на старте вырос вдвое" is the input-tokens leg of the same
check, not a separate rule — fires a regression; fewer than
`MinRegressionHistory` (3) prior runs never fires, however large the jump
(LEARN-TASKS.md LN-16's own "мало данных" case) — a median over one or two
runs is noise, not a trend, same reasoning as `MinSkillEffectRuns` (LN-11) and
`MinDurationSamples` (LN-18).

**The hint is a diff against the manager's own last measurement, not a
recomputation from scratch.** `measureOverhead` reads `PromptOverhead`: the
rendered length of `BuildPrimer`'s output (LN-05), the count and total
character length of every *approved* skill's description (LN-10 — a skill's
description is what stays permanently in a session's context, per
`RenderSkillMarkdown`'s own doc comment), and the length of the project
journal's last `journalTailEntries` (3) sections (LN-06). `OverheadTracker`
keeps the last measurement per `project/session` key **in memory only** — no
new SQLite column, no state file, since the hint only needs to survive
within one running app instance — and `Hint` names which of the three grew
since that prior observation. Computing this is deliberately lazy: only a
detected regression pays for a `BuildPrimer` call (a git exec), so a project
that never regresses never pays for it at all; the tradeoff is that the
"since the previous run" comparison is really "since the last time a
regression fired here", not every run — acceptable for explanatory text on a
dismissible banner, not a correctness-critical measurement.

**Wired like `PrimerFunc`, for the same reason.** `internal/experience`
already imports `internal/session` (for `Step`/`TokenUsage`), so
`finishRun` cannot call `experience.DetectRegression` directly.
`session.RegressionFunc` is the indirection (`SessionManager.regressionFn`,
`SetRegressionDetector`); `app.go` wires it to a closure over
`experience.DetectRegression` plus one `experience.NewOverheadTracker()`
instance shared across the app's lifetime. Called from `finishRun` in its
own fire-and-forget goroutine (same pattern as the LN-03 indexer and LN-06
journal above) and gated on the same `[optimization] experience_tracking`
flag LN-03/04/12/18 reuse: `session_runs` itself is always collected, but
the Hint component reads skill/journal data the same privacy gate already
covers, and reusing the flag needed no new config surface.

**Emitted, not persisted.** A detected regression fires `session:*`-style
event `experience:regression` (`{project, session, run_id, factor, hint}`)
through the same `Emitter` every other session event uses — there is no new
Wails-bound getter, and nothing is written to SQLite. `StatusBar.svelte`
shows a "⚠ Regressions: N" counter (click opens the dashboard);
`CostDashboard.svelte` renders one dismissible banner per alert above the
KPI row. Both read `stores/sessions.ts`'s `regressionAlerts` array, appended
to on the event and filtered by `dismissRegression(seq)` on click — client-side
only, matching every other "closes on click, not persisted" convention in
this app (no `window.confirm()`, no re-fetch).

## Waste report (WS-01)

The loop detector catches the same call N times in a row and the failure
rules catch calls that failed. What neither sees is work that *succeeded*,
did not repeat back-to-back and still bought nothing — most of what a long
session spends (the re-read of every result on every later step is 65% of
spend, `docs/Science_lab/MEASURE.md`). `experience.AnalyzeWaste`
(`internal/experience/waste.go`) mines `action_signatures` for four kinds,
per conversation (the same grouping key as `ActionResultChars`: CLI session
id, else run id, else session name), judging only `is_error = 0` rows:

| Category | Rule |
|---|---|
| `repeat_read` | Read/Grep/Glob that repeats an earlier call with no edit in between — an edit of *the same file* for a Read, *any* edit for a search (its recorded argument is only the pattern, not the searched path) |
| `empty_search` | ≥2 adjacent Grep/Glob calls with ≥2 distinct patterns, each result under `waste_empty_search_chars` (default 100; a miss is ~20 chars, a real hit starts well above) |
| `oversized_read` | Read whose result is over `waste_oversized_read_tokens` (default 8000 ≈ an 800-line file); only the excess counts |
| `rerun_cmd` | Same Bash command again with no Edit/Write/MultiEdit/NotebookEdit between |

Deviations from the first sketch, both forced by what the table records:
`oversized_read` uses the *result* size (`result_chars`), because
`out_tokens` is the tokens of the assistant message that issued the call,
not of what came back; and a repeated Read counts only when it returned the
same number of characters, because the recorded Read argument is just the
path — reading one file in slices (offset/limit) would otherwise look like
reading it again.

Weights follow `attribution.go` exactly (`tokens × (2 + 0.1 × later steps)`),
so the numbers compare with the Cost tab. A call can fall into two
categories (a repeated read of a huge file): per-category counts may overlap,
while `WastedCalls`/`WastedLifetimeTokens` count each call once, at its larger
loss. `Categories` lists only kinds that occurred, so a clean window is an
empty list, not a row of zeros. Surfaced as `App.GetWasteReport(project)`,
the `get_waste_report` MCP tool and the "Waste" tab of the Experience panel.
The report only reads; acting on it is WS-03 (next section).

## Prohibitions instead of persuasion: hook rules (WS-03)

A learned rule in the system prompt costs tokens on every step and wears off
by the end of a long session. A `PreToolUse` hook costs nothing and works the
same on step 1 and step 200 — the project's `guard-self-kill.py` already proved
the mechanism. `[optimization] hook_rules` (default **off**) turns the Waste
tab's "Hook rules" block on; with it off no row is written, no file is touched,
and `GetHookRules` returns `Enabled=false`.

Rule kinds (`hook_rules.kind`, table keyed `UNIQUE(project, kind, param)` so a
refused or taken-out rule is never proposed again):

| Kind | Stops | Source |
|---|---|---|
| `repeat_read` | a Read of the same path/offset/limit, or the same Grep/Glob, with no edit in between (an Edit/Write of that file re-allows the read; any edit re-allows searches — the detector's own definition) | Waste report found `repeat_read` |
| `empty_search` | a Grep/Glob right after two searches whose results were under 100 chars (needs the PostToolUse half of the same script); it blocks once and resets, so it cannot lock the model out | `empty_search` |
| `oversized_read` | a Read with no offset/limit of a file over `param` tokens (×4 chars, by `os.path.getsize`) | `oversized_read` |
| `failure_cmd` | a Bash/PowerShell command containing `param` (whitespace-squashed); the refusal carries `hint`, the form that worked | a Bash command that failed in ≥`MinCandidateRuns` runs and was each time fixed by a different one |

One script, `.claude/hooks/cm-waste-guard.py`, is rendered from the embedded
template `internal/experience/hookguard.py` with the active rules as a JSON
literal, and registered in `.claude/settings.json` (a PreToolUse entry, plus a
PostToolUse one on `Grep|Glob`). Session state (what was read, the empty
streak) lives in `<tmp>/cm-guard-<session_id>.json`. A refusal is exit code 2
and states why, what to do instead and the escape hatch: create the empty file
`<tmp>/cm-guard-<session_id>.allow` and repeat the call — the marker lets the
next *blocked* call through once and is consumed. The hook fails open: any
exception exits 0, because a false block on every call is worse than a miss.
Known limit: after `/compact` the earlier reads are no longer in the model's
context but the hook still remembers them — the marker covers that.

Approval and trial. `ProposeHookRules` only inserts `draft` rows.
`PreviewHookRule` returns `experience.PlanHook`'s result — the new text of each
file and a line diff — and the UI shows it before "Install" even appears;
`ApplyHookRule` writes it (invariant 2 of the block) and sets `trial`. The
settings merge keeps every other key and its order, and returns the file
untouched when the guard is already registered as wanted. A trial is measured
like failure rules: the last 20 runs before the install against the runs
after, as steps per run and errors per run. After `HookRuleTrialRuns` (10)
runs `EvaluateHookTrials` keeps the rule, or — steps per run up more than 10%,
or errors per run up more than 25% *and* by at least one — marks it `reverted`
and rewrites the hook without it; the last rule out deletes the script, the
registration and the folders the feature created. That removal only undoes
what the user approved. It runs from `SkillPilot.Tick` (when autopilot is on
for the project) and lazily from `GetHookRules`. One trial at a time, as for
failure rules. The five `App` methods are in `parityExcluded`: an agent has no
business approving a write into the user's repository.

## Loops that are remembered beyond one session (WS-04)

`LoopDetector` looks at a ring buffer of the last `loop_window` calls and lives
inside one run: a read repeated every fifty steps, or a loop that happened
yesterday, passes it. `[optimization] loop_memory` (default **off** — with it
off no tally is kept, no row is written and the hint is byte-identical to
before) adds three things:

- **Far repeats.** The detector keeps a per-run tally of every call and fires
  when one reaches `loop_far_threshold` (6) occurrences although the window
  never held `loop_threshold` of them. Such a hit is flagged `Far`, and a
  configured `restart` is lowered to `warn`: a repeat spread over a whole run
  is a weaker sign than a tight circle. The spec asks for this check to read
  `action_signatures`, but those rows are written when a run *finishes* (the
  indexer reads the transcript then), so mid-run the tally stands in for them;
  the finished run's rows are what resolution uses.
- **`loop_events`.** Every detection (in-window or far) is stored with the
  signature of the call (`experience.LoopSig` — the same `Signature` the
  indexer applies, minus a Read's range and an Edit's old text). After the run
  is indexed `ResolveRunLoops` sets `resolved_by` to `<tool> <arg>` of the
  first *successful* action after the loop whose signature differs from the
  loop's. A loop the run never got out of stays unresolved and is never offered.
- **The hint.** When a loop is detected, the session asks for the latest
  *resolved* event of the project with the same signature *before* storing the
  new one (a loop never answers itself) and adds "You have been stuck on this
  exact step before (… ago). What got past it last time: …" to what the loop
  action already says: the manager log line and toast, the `send_hint` stdin
  message, or the `restart` note. An empty history adds nothing.

The session package cannot import `experience`, so `app.go` supplies a
`LoopMemoryFactory` and a `LoopResolveFunc` (`SessionManager.SetLoopMemory`).

## Strict gates for Claude sessions (WS-05)

Mixed programming runs the project's `gates` as a blocking check and sends a
red result back to the worker; a Claude session only had `post_task_hook`,
which logs and blocks nothing. `gate_rounds = N` on a session (default `0` =
off; also needs a non-empty project `gates`, and a Claude runtime — Hermes
keeps its own turn loop) closes that gap. The dialog of a session with the flag
off is untouched: no gate runs, no log line, no prompt change.

- **When.** Run's clean-exit branch, once the task is classified `closed`
  (the queue pointer left, or there is no queue to consult) and before
  `tasks_done` is bumped. `worker.RunGates` runs in the session's work folder
  (`workDir`: the pool slot or the project root).
- **Red → rework round.** `GateResult.Feedback()` — the same text mixed
  programming sends, not a paraphrase — behind a one-line frame ("rework round
  K of N") becomes `pendingGateFeedback`. The next iteration resumes the
  conversation (`resumeSessionID = CLISessionID`, as for a transient API
  error) and `initialPromptBody` sends the feedback first, ahead of the
  crash-recovery prompt. A rework iteration neither re-reads the queue (its
  task already left it) nor falls into the "no pending tasks" interactive
  branch, and nothing is emitted between rounds: the rounds are one run, one
  `session_runs` row.
- **Green → done.** The usual `task_done` path.
- **Limit.** After `gate_rounds` rework rounds the gates run once more; still
  red → the session stops with status `error`, an explicit message, the red
  output in the log and a run ending `unfinished`. The task does not count,
  and nothing releases the pool slot — the work folder stays as it was.
- **Why the done marker is not the trigger.** The spec says "on the marker";
  the closed-task classification is a superset (the pointer leaving the queue
  is the durable fact) and the marker alone would skip queue-less sessions.
  A `slice` (marker, pointer still queued) is not gated: a half-done task is
  allowed to be red.

## Reference cases from the project's own runs (WS-06)

Comparing models and settings fairly needs one task run twice, and there is no
ready set of such tasks - but every task that finished well already is one: it
has a text, a commit it started from, the gates it passed and a known good
outcome. `[optimization] reference_cases = true` (default off; also needs the
project's `gates` and a Claude runtime) keeps them as a by-product of work. With
the flag off the session makes no git call and `task_done` carries nothing extra.

- **Capture.** On each task start the session notes the queue pointer and reads
  its task section (the `## ` heading at `<file>:<line>` down to the next heading
  of the same or a higher level; `#` lines inside code fences do not end it) from
  the integration branch, as the queue itself is read. Right before the first
  launch of the task it notes `HEAD` of the work folder - the base commit. When
  the task closes (and strict gates, if on, are green) it reports
  `RefCaseRecord{ptr, text, base, result = HEAD of the work folder, gates,
  rounds}` on `EvtTaskDone`; the manager adds what it measured - input+output
  tokens (cache excluded: that is all a one-shot replay reports, so original
  and replay compare), the run's wall-clock seconds, model, effort - and stores
  it. Snapshot first, then `finishRun`.
- **Only good tasks.** A task that did not close, closed red, or changed nothing
  in the repository is not a case; neither is one without gates (nothing to check
  a replay with), without a readable text, or already stored (same project, task
  pointer and base commit). A task that spans several runs in one session - a
  slice, an unfinished retry, a crash/rate-limit retry - is dropped as well: its
  tokens and start would be partial. (A slice continued by a *different* session
  cannot be seen from here; its case covers the last session only.)
- **Replay.** `git worktree add --detach <tmp> <base_commit>` of the project's
  repository, the task text behind a framing line ("measured replay, edit files
  only, do not commit/branch/push/touch the queue") as a one-shot `claude -p`
  (`analysis.CLIExecutor`, `--permission-mode acceptEdits`) with the chosen
  model/effort, then the case's own gates. Red gates -> `Feedback()` goes back in
  the next attempt's prompt, up to 2 rework rounds. The outcome row sits next to
  the original; the resulting tree is pinned with plumbing only (`write-tree`,
  `commit-tree`, `update-ref refs/cm-replay/case-N-ts`) - no hooks, no config, no
  branch - and the worktree is removed. `refs/heads` and `HEAD` of the project
  are never touched; one replay runs at a time. A fresh checkout has no
  `node_modules`/build caches, so a gate that needs them is red for every
  configuration alike.
- **Vanished base.** The report checks `base_commit` with `rev-parse --verify`;
  exit 1 (the commit is not there) marks the case `invalid_reason` and the report
  lists it anyway. A git failure (not a repository, git missing) proves nothing
  and marks nothing; replay refuses an invalid case.
- **Report.** `GetReferenceReport` / MCP `get_reference_report`: per task the
  original and every replay - gates green or not, rework rounds, tokens, seconds.
  `replay_reference_case` is bounded by the MCP long-tool timeout (15 minutes).

## Benchmark runs (WS-12)

What surrounds the model - effort, skills, bans, how strict the check is - moves
a result more than swapping the model, and only models get measured.
`[optimization] benchmark = true` (default off; `run_benchmark` is refused with
it off, the report stays readable) lets a project run a **suite x configurations
x repeats** matrix and compare the cells.

- **Suite.** The reference cases of WS-06 (all usable ones, or `case_ids`) or an
  external list of tasks (`id`, text, `base_commit`, gates). A task needs text, a
  commit that still exists and gates; anything else - or an empty suite, no
  configuration, a duplicate name, more than `benchmark_max_trials` (60) trials -
  is refused by `PrepareBenchmark` **before** anything is stored or spent.
- **Configuration axes.** `model`, `effort`, `no_skills` (the replay folder loses
  `.claude/skills`), `no_bans` (the PreToolUse ban hook of WS-03 is removed;
  otherwise the project's active hook rules are installed), `no_gates` (one
  attempt, no rework rounds - the gates still decide passed or not). The zero
  value is the project as it is. The setup is applied to the throwaway
  worktree and committed into *its* detached HEAD with plumbing, so the pinned
  result shows only what the agent did and "the agent changed nothing" stays
  detectable. Nothing is written into the user's repository (invariant 2).
- **Trials** use the same machinery as a WS-06 replay (`withDetachedWorktree` +
  `runInFolder`, one code path): detached worktree at the task's base commit, the
  task text behind the replay framing line, the task's gates, up to `rounds`
  (2) rework rounds, tree pinned under `refs/cm-replay/bench-<run>-<ts>`. No
  project branch is touched. Order is repeat -> task -> configuration, so an
  interrupted run leaves whole repeats. A trial whose model could not be run at
  all is stored `errored`: counted per configuration (`Errors`) and left out of
  every figure - it says nothing about the configuration.
- **Repeats are mandatory.** Default 3. The unit of spread is a *repeat*: per
  configuration and repeat, the pass rate and the mean rounds-to-green, tokens
  and seconds over the suite. A configuration is compared with the baseline
  (the first) metric by metric: `differs` when the per-repeat ranges
  (min..max) do not touch, `within_noise` when they overlap, `indistinguishable`
  when either side has fewer than two repeats with a value - a one-repeat run
  carries a warning and no difference is ever claimed. A deliberately plain rule,
  not a significance test: with 2-3 repeats nothing finer is honest.
- **No single score.** `BenchmarkReport` has per-configuration `Trials, Errors,
  Passed, PassRate` and `Rounds` (to green, over passed trials), `Tokens`,
  `DurationSec` each as `N/Mean/Min/Max/StdDev`, plus the per-repeat rows and the
  raw trials. How much a round, a token or a failed gate weighs depends on the
  task; that is the reader's choice.
- **Frozen inputs.** `benchmark_runs` keeps its own JSON copy of the suite and the
  configurations; the report is built only from `benchmark_runs` +
  `benchmark_trials`, never from the live `reference_cases`. Editing, invalidating
  or deleting a case changes no past run.
- **Running.** `RunBenchmark` validates and freezes synchronously, then runs the
  trials in a background goroutine and returns the just-started run's report;
  `GetBenchmarkReport` (poll until `Status` is `done`) reads partial figures
  meanwhile and says so in `Warnings`. One benchmark at a time (`TryLock`). A run
  still `running` at the next start-up (crash, quit) is closed as `interrupted`
  by `InterruptRunningBenchmarks`; trials already stored are kept.
- **Cost.** Every trial is a paid model run and spends the limit window; that is
  why the cap and the explicit flag exist. MCP: `run_benchmark` (operate),
  `get_benchmark_report` (read). There is no GUI panel yet.

## Quality labels (WS-11)

Gates and rounds say whether a task worked, not what went wrong. A label names
the cause: `tests_fitted_to_code` (tests bent to pass instead of fixing the
code), `protocol_broken`, `garbage_left`, `wrong_task_done`. A label, not a
score: a number invents precision, a label points at a fix.
`[optimization] quality_labels = true` (default off; Claude runtime) turns it
on; with the flag off the session makes no git call and no model is called.

- **Only bad endings.** Two events count, both final: gates still red after the
  last rework round (WS-05), and `maxUnfinishedStreak` unfinished runs in a row
  that stop the loop. One unfinished run that the next session retries is not a
  verdict. A task that ends well is never looked at, so judging it costs nothing.
- **Evidence.** The session keeps the task's text and the work folder's start
  commit across its retries (unlike the reference-case capture, which drops a
  task that spans runs). At the bad ending `EvtRunEnd` carries a `LabelRecord`:
  task text, the last red gate output (else the agent's last words, filled by the
  manager), `git status --short` - leftovers are untracked - and `git diff
  <start commit>`, capped.
- **Judge.** One `claude -p` call (`analysis.GenerateTaskLabel`), `haiku` and low
  effort by default (`quality_label_model`), plan mode, structured output whose
  `label` is an enum of the list plus `none`. `none` stores nothing: a guess would
  be a false claim. A label outside the list is an error in `LabelTask` and
  `Store.InsertTaskLabel` alike, never a quiet write. The call runs in a
  goroutine after `finishRun` so the run row exists.
- **Cost.** The judge's input+output tokens and cost are added to the judged
  run (`AddRunUsage`) - also for `none` - and stored in `task_labels.tokens`; the
  measurement does not hide from the spend it measures. A run is judged once.
- **Report.** `GetTaskLabels` / MCP `get_task_labels`: counts per label, the
  judgements' total tokens, the newest labels with reasons.

## Repository suggestions (WS-07)

A queue is a text file, so adding a line costs nothing; what is missing is the
list of things worth adding. `[optimization] repo_suggestions` (default **off**)
enables a local scanner, `internal/suggest`, that needs no model: it reads git,
the project's files and the recorded outcome of its last gate run. The Waste tab
shows the result as a checklist ("Suggestions"); `GetSuggestions` only reads.

- **Six kinds**, each with a key that does not contain a line number or a date
  (`kind:` + a hash of what identifies the finding), so a rescan can tell what
  was accepted. Stale `TODO`/`FIXME`: `git grep` outside markdown/testdata/vendor,
  the line's `git blame` author time older than `suggest_stale_days` (90 by
  default). Red gates: the latest `gate_runs` row is red, its commit still exists
  and is still the tip of a local branch (nothing was committed on top since; a
  later green run supersedes the row). Doc drift: `doc_sync = ["code_glob =>
  doc"]` per project, commits that touched the code after the document's last
  commit (`git log doc_commit..HEAD -- :(glob)code`). Broken queue pointers: a
  `file:N` line of a session's `task_source` whose line N is not a heading (or the
  file is gone). Abandoned branches: local, not merged into the main branch, last
  commit older than the threshold (7 days). Abandoned folders: a linked worktree
  with uncommitted changes none of which was touched within the threshold (a live
  pool slot has fresh mtimes and is left alone).
- **Gate runs are recorded, not rerun.** `checkStrictGates` writes the outcome and
  `HEAD` of the gate root into `gate_runs`, only while `repo_suggestions` is on
  (the session gets `GateRunFn` from `gateRunFnFor`, nil otherwise). A project
  that never ran strict gates simply has no red-gates kind.
- **Nothing reaches a queue without a tick.** `AcceptSuggestions(project,
  queueFile, keys)` re-scans and takes the text from its own scan, never from the
  caller; the queue must be one of the project's task sources. A pointer needs a
  heading to point at, so each accepted suggestion becomes a self-contained
  `## SG-NN: title` section (Depends on / What to do / Done when, plus an
  `<!-- suggest:KEY -->` line) appended to `SUGGESTED-TASKS.md` in the project
  root, and `SUGGESTED-TASKS.md:N` is appended to the queue. The key line is what
  keeps a rescan from offering the same thing again. Both methods are excluded
  from MCP parity (invariant 2 of the WASTE block: writes only on approval in
  the UI).
- **Failure of one detector is not a failure of the scan.** Not a git repository,
  git missing, a rule on a path with no history: that kind yields nothing, and a
  clean repository is an empty list, not an error.

## Nightly gate run (WS-10)

Nobody watches a session at night, and what comes back in the morning should be
a decision, not a result of unattended work. A project with `gate_schedule =
"HH:MM"` (private layer, `config.local.toml`; empty = off) has its gates run by
`internal/nightly` at that local time, in the project folder as it stands.
Gates are `gate_schedule_gates`, else the project's `gates`.

- **A red run files a task; it never starts a session.** The failed command and
  the tail of its output become a `## SG-NN:` section of `SUGGESTED-TASKS.md`
  (the same file and format WS-07 writes) and its pointer is appended to
  `gate_schedule_queue`, else the first session's `task_source`. The queue stays
  the only way work begins. Setting a schedule is the approval for these writes
  (invariant 2 of the WASTE block): the user switched the job on, and no other
  file is touched. A project with no gates or no queue is skipped with one log
  line, not scheduled half-way.
- **The same failure is one task.** The key is the failed command plus the tail
  of its output with timings, timestamps and addresses removed, so a rerun that
  only differs in `0.01s` vs `0.07s` is the same key. A section is skipped while
  its pointer is still in a queue (`AcceptUnlessOpen`); once the pointer is gone
  the task was taken, so the failure coming back is filed again — unlike WS-07,
  where an accepted key is never offered twice.
- **Restarts neither repeat nor lose a night.** The last run of each project is
  in `nightly_runs`. A project that ran before is due when its latest slot is
  newer than that run, so a night missed with the app closed runs once on the
  next start; one that never ran waits for its first slot after the app started,
  so enabling a schedule at noon does not run at noon. The loop looks at the
  clock every minute, runs projects in parallel, never one project twice at
  once, and bounds a run at 30 minutes.
- **Gates run in the working folder, not in a clean checkout.** A session
  editing the project at that moment can make them red for a reason that is not a
  defect; the task text says what ran and when, and the same failure the next night
  is not filed again while the task is open.
- **No bound method, no GUI.** The schedule is configuration; the result is a
  task in the queue the Queue panel already shows. Hence nothing in the Wails
  table and no MCP tool.

## Learned files (WS-09)

Skills, rules and bans live in the database, so a bad lesson could not be undone
the way everything else is — with git — nor travel with the project to another
machine. `[optimization] learned_files = true` (default off) adds a block to the
Waste tab and keeps them also as plain files in
`<project>/.claude-manager/learned/`, one JSON file per lesson
(`internal/learned`). The database stays the working source of truth; the files
are history, transfer and a way to roll back by commit.

- **What is a lesson.** Only what is in force: a skill that is approved or on
  trial (by its newest row), a rule (`failure_rules`) or ban (`hook_rules`) that
  is on trial or kept. Rejected, dismissed, reverted and archived ones are not
  exported. A file holds the kind, key (skill name, error key, `<hook kind>|<param>`),
  status, origin, body (SKILL.md / rule text / hint), the date it appeared and was
  decided, and the **measured effect** (the stored decision reason, verbatim).
  Provenance (draft, source signatures, error examples) is carried so an import can
  rebuild the row, but is not compared. A file is deterministic — no export
  timestamp — so a commit shows only real changes. The folder is not gitignored.
- **A hash, not a timestamp, decides who is newer.** `Lesson.Hash` covers
  everything but provenance and layout; `learned_sync` remembers the hash both
  sides had when they last agreed. Then: only the file changed → `import_upd`
  (this is also what a `git pull` or a reverted commit looks like); only the
  database changed → `export_upd`; file gone and database unchanged → `retire`
  (a reverted commit that *added* lessons takes them out of force: skill
  archived, rule/ban reverted, so session behaviour returns to what it was); the
  reverse → `remove_file`; **both changed, or never compared and different →
  `conflict`**. A conflict is never settled by a default: `ApplyLearnedFiles`
  refuses it without an explicit `file` or `db` resolution, and the UI keeps
  "Apply" disabled until one is chosen.
- **Imports keep the status as exported**, so the state after import equals the
  state before export (`TestExportThenImportGivesTheSameState`). A rule on trial
  is therefore on trial on the new machine too and is measured there by the same
  rule autopilot; imported rules and bans reach a session only through the
  existing paths (`InjectedFailureRules`, the hook the user installs with WS-03's
  diff). A skill's `SKILL.md` is written only when missing, never over a copy that
  is there (it normally arrives with the repository).
- **Nothing is written into the project without a tick** (invariant 2).
  `GetLearnedFiles` reads files and the database and records only the
  agreement of equal pairs in `learned_sync`; `ApplyLearnedFiles` re-plans (the
  picks name items, never content) and touches exactly the picked ones. With the
  flag off neither is allowed to do anything: no folder is read or created.
  Unreadable files (broken JSON, a newer `format`, an unknown kind, an invalid
  skill name, a second file for the same key) are listed as problems and never
  imported; file removal is confined to the lessons folder.
- **Where it is offered.** The block opens with the Waste tab and, when files
  carry lessons the database lacks, says so first. There is no MCP tool for the
  same reason as WS-07's accept.

## The "waiting for you" screen (WS-08)

With several sessions in several projects, finding the one that is stuck meant
opening each. `[optimization] waiting_screen = true` (default off) adds a
status-bar button "Waiting for you: N"; it opens one overlay over the main area
with a row per session that needs a person, the reason, how long, and a jump.
With the flag off the button is absent and the status bar is unchanged.

- **A view over existing state, no backend method.** The flag reaches the UI
  through `GetConfig` (`Optimization.WaitingScreen`), so there is no new Wails
  binding and no MCP tool. `lib/waiting.ts:buildWaiting` is a pure function of
  the session list, the queue snapshots (`stores/queues.ts`), the last
  `session:error` text per session and the `needs_human` mixed tasks.
- **Kinds, most urgent first:** `permission` (status `waiting_permission` /
  pending request), `question` (`waiting_for_user`), `gates_exhausted` (status
  `error` whose message is the strict-gates stop, WS-05 — matched by
  `GATES_EXHAUSTED_RE`), `error`, `needs_human` (a mixed task that ended
  `needs_human`, listed per project via `GetMixedRounds`), `rate_limit`,
  `stopped` (idle with `stopped_after_task_at`), `queue_empty` (idle, tasks done,
  the roadmap has finished rows and nothing queued ahead). Within a kind the
  longest wait is first. A session is at most one row; a stop beats an empty queue.
- **The permission queue stays.** It keeps its own "Waiting: N" button and its
  modal; the permission rows of the new screen have an "Answer…" button that
  opens that modal, so the answer path is the same one.
- **Overlay mechanics** are the permission queue's: a modal layer in
  `App.svelte`, closed by the backdrop, ✕ and Escape (a window-level handler,
  since the dialog div is rarely focused). The left session panel is untouched.
- **A `needs_human` mixed task stays listed** until it is resolved in the mixed
  run panel; the screen does not dismiss anything itself.

## Billing mode: the limit window and time, not dollars (WS-02)

On a subscription a dollar is a ruler, not a bill: what runs out is the
rate-limit window, and then the work stops. `[settings] billing =
"subscription" | "api"` (default `subscription`) says which unit the
readouts lead with; `limit_window_tokens` is the size of the plan's window
(`0` = unknown). Both are editable in Settings → Global → Billing and through
`update_config`; `GlobalSettings.Billing` is validated on load (unknown value
or a negative window is refused).

What a run took out of the window is `limitTokens` = input + cache write +
cache read (`frontend/src/lib/billing.ts`). Output is left out on purpose: the
window is metered on what is sent to the model. The share of the window is
`tokens / window`, and an unknown window is `null`, never 0 — the UI says
"share unknown" instead of printing `0%`, and nothing divides by zero.

In `subscription` mode the session card (row 3), the status bar's "Today",
the Dashboard's KPI row and the Waste tab put tokens-to-limit, share of the
window and time first; the dollar figure stays, labelled "conv. units"
(conventional units: a list-price estimate for comparing runs, not money
spent). The old tokens/USD switch keeps working in the Dashboard's charts and
by-model bars, where the USD button reads "Conv. units". In `api` mode none
of it renders and every readout is exactly what it was before the setting.

The frontend learns the mode from `GetConfig` at boot (`App.svelte` →
`stores/billing.ts`, persisted in `localStorage` like the language so the
first paint is already right) and again whenever Settings loads or saves.
Time of the day comes from a new `daily_metrics.total_duration_ms` column
(additive migration), filled with the wall-clock time of each finished run and
exposed as `DailyTokens.duration_ms`; days recorded before the column existed
read as 0 time. `WasteReport` gained `LimitWindowTokens`, `WindowKnown` and
`WindowShare` (`ApplyWindow`; waste lifetime tokens / window — it can exceed
1 because the report spans days while a window is hours), shown as a line
under the Waste headline in subscription mode.

## Session work folder (slot)

`use_worktree` no longer passes `--worktree` to the CLI. Both runtimes launch the
process with `cmd.Dir` = the session's persistent pool slot
(`<project>/.claude/worktrees/<slot>`, `internal/worktree`: `SlotName`,
`EnsureSlot`; wiring in `internal/session/workdir.go:prepareWorkDir`).

- **Why not `--worktree`:** it creates a nameless worktree from HEAD on every
  process start — slow (checkout before the first line), and an interrupted task's
  work is left in a worktree nobody returns to (the 2026-09-08 LN-01 incident:
  seven sessions, seven throwaway worktrees). The slot keeps the branch, the
  uncommitted work and `node_modules` between runs.
- **Invariants:** the manager only guarantees the folder exists and is sound; it
  never commits/resets the slot's branch and switches it only in one case — the
  release below. `s.ProjectPath` stays the
  project root for everything the manager itself does (task queue, logs, hooks,
  primer). A slot failure never fails the session: it logs
  `Рабочая папка не готова…` (+ `session.slot_failed`) and runs in the project root.
- **One slot, one running session:** `SessionManager.slotConflictLocked` rejects a
  start when another non-idle session of the project has the same `SlotName`.
- **Start stages in the feed** (`emitManagerLog`): `Готовлю промпт…`, `Рабочая папка: …`,
  `CLI запущен (pid N)…`, then `CLI ещё инициализируется (15 с)…` (repeating every
  30 s) until the `init` event, which prints `CLI готов за X с` only if X ≥ 3 s.
- **Resume:** the CLI's transcript is tied to its cwd, so `--resume` runs in the
  same slot. After switching `use_worktree` on, the old transcript (root cwd) is
  not found: Claude's `No conversation found with session ID` stderr is classified
  `KindSessionNotFound`, and the run loop drops the id and starts fresh.
- **Switching `use_worktree` moves the work, not only the cwd**
  (`workDirSwitchNote`, `worktree.Release`). Changing the cwd alone was not
  enough (ARB, 2026-10-15): a resumed conversation remembered the slot and `cd`'d
  back into it, and the slot kept the task branch so the root could not check it
  out. Now each run records its folder (`PersistedState.WorkDir`, plus
  `s.workDir` in memory); with `use_worktree` off the session's own slots
  (`SlotName` of the current config, the name-based slot, the previous folder)
  are released — a slot on a branch with no tracked changes is switched to a
  detached HEAD at the same commit, a dirty one is left alone. When the folder
  changed or a slot held a branch, the next prompt (recovery or regular, never an
  empty interactive one) starts with `Work folder for this run: …` naming the
  folder, the old one not to `cd` into, and the released branch or the
  uncommitted changes still to move. With nothing changed the prompt is
  byte-identical.

### Checkbox ↔ protocol, `CLAUDE.md`

- **The flag and the protocol meet in one folder.** The templates take `Slot` /
  `BranchPrefix` (`ProtocolParams`, derived from the queue file through
  `worktree.SlotName`: `STATUS-P3.md` → `p3-work` / `p3-`), so the slot the app
  prepares for `use_worktree` is the slot `/cm-task-start` occupies. A queue not
  named `STATUS-P<N>.md` falls back to `p1-work`; P1 output is byte-identical to
  the pre-parameter templates.
- **Defaults:** a session added in Settings (and the auto-created Chat) has
  `use_worktree = false`; the manager-created P1 has it `true`.
- **Banner:** enabling the checkbox in a project with neither
  `scripts/worktree-pool.sh` nor `docs/git-workflow.md` (`HasSessionProtocol`)
  shows an inline banner with an "Install session protocol" button — without a
  protocol the slot works but nobody manages task branches in it.
- **`CLAUDE.md` section.** `WriteProtocolFiles` appends a short
  `## Developer sessions` section (creating the file if absent) only when
  `CLAUDE.md` does not already contain `docs/git-workflow.md`. A session reads
  `CLAUDE.md` first, so this is where it learns about its slot and the two
  skills; a project that already links the workflow doc (Lumen) has its own
  wording and is never touched, and the check also makes a second call a no-op.

## Why cm-mcp is a public interface with full GUI parity

*Decided in MCP-01 (2026-10-15). Tables and the parity map: [mcp.md](./mcp.md).*

- **Why.** `cm-mcp` started as a test bridge (19 tools over 33 RPC methods,
  while `App` exports 87). Third-party agents (Claude Code/Desktop, Cursor,
  Codex, Hermes) should do everything the GUI does without reading our code,
  so it becomes a public MCP server driven by the same `App` methods the
  buttons call.
- **Spec target `2026-07-28`, dual-era.** The current revision removed
  `initialize`, `ping` and `logging/setLevel` and added `server/discover` and
  per-request `_meta`; clients in the field still speak `2025-11-25`, so the
  server answers both. Not doing: Streamable HTTP (local control-plane, every
  target client has stdio, HTTP adds auth surface), sampling/roots/
  elicitation/MRTR (the server asks nothing of the client; roots, sampling and
  logging are deprecated), the tasks extension (`wait_for_*` + progress do).
- **Official Go SDK (`go-sdk` v1.8.0+), not a hand-rolled `main.go`.** Pure Go
  (no CGO), supports both eras, derives schemas from Go structs. MCP-02 must
  prove dual-era over stdio and measure start time before committing to it.
- **One session address, `session_id = "project/session"`**; the old
  `project`+`session` pairs stay as aliases; removal is MCP-18 (after MCP-17 closed the block).
- **Access levels `read` < `operate` (default) < `admin`.** Anything that
  approves in place of a human, writes into the user's repository, changes
  config, deletes data or stops work in bulk is `admin`; the human sets the
  level in the client config, the agent cannot raise it, and the control-plane
  re-checks it.
- **Parity is a test, not a promise.** Every exported `App` method has an MCP
  tool or an `excluded` entry with a reason (OS window actions, native
  dialogs); the list lives in one Go registry (MCP-03) and the test stays in
  the gate. Frontend-only state (`localStorage` view prefs) is purely visual
  and excluded. No tool may kill or restart the manager process.

### Autopilot Applies Failure Rules via the System Prompt (LEARN-TASKS.md LN-40)

With `auto_skills` on, the skill autopilot's tick also runs `rulesStep`
(`internal/experience/rulepilot.go`): it decides on a rule under trial, then
applies at most one more — a ready `draft` first, else a freshly drafted rule
for an error that still hit ≥ 2 of the last 20 runs. The rule is **not** written
to `CLAUDE.md`: that file belongs to the repository, developer sessions edit it
during doc-sync, and an uncommitted change in the main checkout (or in a pool
worktree) breaks their `merge`. An applied rule lives in `failure_rules`
(`origin = auto`, status `trial` → `kept` | `reverted`) and
`SessionManager.learnedRulesFor` renders the `trial`/`kept` ones as a
`## Learned from failures (Claude Manager)` block appended to
`--append-system-prompt` of Claude sessions at process launch (Hermes is not
touched; mid-session changes would break the prompt cache). Switching a rule off
is a status change; the next session no longer sees it. `accepted` rules are
already in `CLAUDE.md` and are not injected twice. A trial ends after 10 runs:
kept when the error's per-run rate fell to half or less and the `completed`
share did not drop by 0.2, else reverted (`no_effect` / `worse`). The daily
budget is shared with skills (`autoSpentToday`).
