# Runtimes: how Claude Code and Hermes map to a common event format

RT-01 of [RUNTIME-TASKS.md](../RUNTIME-TASKS.md). Describes the **current**
code, as of `d88f196`/`2064fe0` — nothing here changes code, it's the map
RT-02..08 work from. Every claim below is checked against a `file:function`;
when RT-03/04/05/06/07 land, update the affected sections in the same commit
(see `RUNTIME-TASKS.md` §Инварианты — a task without an updated section here
is not done).

## 1. The pipeline, end to end

```
claude stdout ──ParseLine (parser.go)────────────────┐
                                                       ├─> []ParsedEvent ─> Session.handleEvent (session.go) ─> SessionEvent ─> SessionManager.onSessionEvent (manager.go) ─> Emitter.Emit ─> Wails event ─> Svelte stores
hermes stdout ──hermesStream.Parse (hermes_parser.go)─┘
```

- **Claude** (`parser.go:ParseLine`): one call per stdout line, stateless
  except for timestamp injection (`parseLineAt`). Returns exactly one
  `ParsedEvent`.
- **Hermes** (`hermes_parser.go:(*hermesStream).Parse`): one call per stdout
  line, but the receiver (`hermesStream`) is stateful — it buffers streamed
  `text` deltas, tracks open tool calls without a real id, and holds a
  `clarify` call's questions until its `tool_result`. Returns `[]ParsedEvent`
  (zero, one, or two — e.g. a flushed buffered-text entry plus the new
  event).
- Both feed the **same** `Session.handleEvent(ev ParsedEvent, autonomous bool) bool`
  (`session.go:1489`) — this is the runtime-agnostic core the RUNTIME-TASKS.md
  intro calls out as already shared. `handleLine` (`session.go:1480`) is the
  thin Claude-only wrapper that also stamps tool-call durations
  (`toolTimer.stamp`, Claude has no wire duration); Hermes calls
  `handleEvent` directly from `hermesRunTurn`'s `dispatch` closure
  (`hermes_runtime.go:304`).
- `handleEvent` turns `ParsedEvent` into zero or more `SessionEvent`s via
  `s.emit` (`session.go:1821`), which calls the manager's `EventCallback`.
- `SessionManager.onSessionEvent` (`manager.go:1429`) is the single switch
  over `SessionEvent.Type` that updates `managedSession` state, writes to
  SQLite (`session_logs`/`session_runs`/`daily_metrics`), and calls
  `m.emit(EventName…, payload)` — the Wails event names are the constants at
  `manager.go:111`.
- The frontend subscribes via `runtime.EventsOn()` in `frontend/src/stores/*.ts`
  (see `docs/architecture.md`'s package tree) and never sees `ParsedEvent` —
  only the Wails-level JSON the manager chose to emit.

## 2. `ParsedEvent` field table

Defined in `parser.go:166`. "Filled by" says which adapter(s) populate the
field; "Consumed by" is where `handleEvent`/downstream reads it. Fields not
listed here (the raw JSON structs `rawStreamEvent`, `hermesRawEvent`, etc.)
are adapter-internal and never leave `parser.go`/`hermes_parser.go`.

| Field | Meaning | Filled by | Consumed by → `SessionEvent` |
|---|---|---|---|
| `EventType` | One of `EventLog/EventResult/EventRateLimit/EventInit/EventPermission/EventUnknown` (`parser.go:156`) | both | selects the `switch` arm in `handleEvent` |
| `Entries []config.LogEntry` | Log rows to append to the session log (see §3 for the LogEntry sub-fields) | both | `EvtLog` (0..n), also carried on `EventInit`/`EventResult` |
| `Result *SessionResult` | Final metrics of a turn (cost, tokens, stop reason, result text) | both | `EvtResult`; also drives `s.stepLimitHit`, `s.authErrorHit`, `checkContextRestart`, the ask-user-marker check |
| `RateLimit *RateLimitInfo` | A `rate_limit_event`'s payload (Claude only on the wire; Hermes synthesizes one, see §6) | Claude: `handleRateLimit`; Hermes: `onRateLimit` called directly from `hermes_runtime.go`, not through this field | `EvtRateLimit` via `Session.onRateLimit` (`ratelimit.go:78`) |
| `Init *InitInfo` | Session id, model, tools, cwd from the `system/init` line | both (Hermes only fills `SessionID`/`Model`) | `EvtInit`; `s.CLISessionID` rotation; crash-recovery state save |
| `Permission *PermissionRequest` | A `permission_request` line | Claude only — Hermes has no such event (one process per turn, no mid-turn approval protocol) | `EvtPermission` |
| `Usage *TokenUsage` | Per-turn token counts | Claude: from `assistant.message.usage`; Hermes: synthesized once from the `result` line's `tokens` (§6) | `EvtUsage` |
| `Todos []TodoItem` | Claude's `TodoWrite` tool_use input, or Hermes's `todo_list` tool_result output | both, different tool names (see §3) | `s.updateTodos` → `EvtTodo` → TaskPanel |
| `Activity *Activity` | Transient "what's happening now" (`thinking`\|`tool`\|`writing`\|`idle`) | both — Claude from `stream_event.content_block_start`, Hermes from its own event types directly (§3) | `s.setActivity` → `EvtActivity` (never persisted) |
| `Questions []Question` | Ask-user question(s) pending, unified across sources (RT-03) | Claude: parsed from the ```` ```ask-user ```` marker in `Result.ResultText` (`parser.go`); Hermes: from the marker too, or from a `clarify` call's questions attached to its `tool_result` | `session.go`'s `handleEvent`/`hermesRunTurn` dispatch (§6 "known differences") route it into `pendingQuestionSet`/`PendingQuestion` |

`SessionEvent` (`session.go:54`) is the next layer down — one `ParsedEvent`
can produce several `SessionEvent`s (e.g. `EventLog` with both `Entries` and
`Usage` set emits one `EvtLog` per entry plus one `EvtUsage`). Its own fields
mirror `ParsedEvent`'s 1:1 except `RunStatus`/`CLISessionID`/`TasksDone`,
which are `Session.Run`-loop bookkeeping with no `ParsedEvent` equivalent —
see §4 and `internal/session/task_outcome.go`.

## 3. Line/event correspondence table

### Claude CLI (`parser.go`)

| Claude stdout line (`type`) | Handler | `ParsedEvent` |
|---|---|---|
| `{"type":"system","subtype":"init",...}` | `handleSystem` | `EventInit`, `Init` filled, one `EventLog` entry ("Session initialized: model vX") |
| `{"type":"system","subtype":≠"init"}` | `handleSystem` | `EventUnknown`, no entries |
| `{"type":"assistant",...}` | `handleAssistant` | `EventLog`; one `LogEntry` per content block (`text`→`level:"text"`, `tool_use`→`level:"tool"` + `Diff`/`ToolArgs`/`ToolUseID`, `thinking`→`level:"thinking"`); `TodoWrite` tool_use also fills `Todos`; `Usage` from `message.usage` |
| `{"type":"user",...}` (string content) | `handleUser` (shape 1) | `EventLog`, one entry `level:"user"` — a replayed prompt (`--replay-user-messages`) |
| `{"type":"user",...}` (array content) | `handleUser` (shape 2) | `EventLog`; one entry per `tool_result` block, `level:"tool_result"` or `"error"` if `is_error`, `ToolUseID` from `tool_use_id` |
| `{"type":"result",...}` | `handleResult` | `EventResult`, `Result` filled, `Activity:{Kind:idle}`, one entry `level:"result"` (subtype prefixed if not `"success"`) |
| `{"type":"rate_limit_event",...}` | `handleRateLimit` | `EventRateLimit`, `RateLimit` filled (nil → `EventUnknown`) |
| `{"type":"permission_request",...}` | `handlePermission` | `EventPermission`, `Permission` filled |
| `{"type":"stream_event",...}` | inline in `ParseLine` | `EventUnknown`, `Activity` from `streamActivity` (only `content_block_start`; deltas dropped) |
| anything else / malformed JSON | fallback in `ParseLine` | `EventLog`/`EventUnknown`, one raw `level:"system"` entry (line preserved verbatim so nothing silently vanishes) |

### Hermes CLI (`hermes_parser.go`)

| Hermes stdout line (`type`) | Handler | `ParsedEvent`(s) |
|---|---|---|
| `{"type":"system","subtype":"init",...}` | `Parse` case `"system"` | flushed buffered text (if any) + `EventInit` |
| `{"type":"text","text":"…"}` | `Parse` case `"text"` | buffered into `h.text`, **not emitted per line**; only an `Activity:writing` event on the *first* delta of a run of them (state `h.activity`), the accumulated text is emitted as one `EventLog` entry (`level:"text"`) on the next non-text event or `Flush()` at stream end |
| `{"type":"tool_use",...}` | `Parse` case `"tool_use"` | flushed text + `EventLog` with one entry (`level:"tool"`), `Activity:{tool}`; if `name=="clarify"` its questions are parsed and stashed in `h.clarifyByID`, not emitted yet |
| `{"type":"tool_result",...}` | `Parse` case `"tool_result"` | flushed text + `EventLog` with one entry (`level:"tool_result"`/`"error"`), `Activity:thinking`; `name=="todo_list"` fills `Todos` from the *output*; if this call's id matches a pending `clarify`, `Questions` is filled here (`Source: hermes_clarify`) |
| `{"type":"result",...}` | `Parse` case `"result"` | flushed text + (if `Tokens != nil`) a bare `EventLog{Usage}` + (if `ExitCode!=0 \|\| Error!=""`) an error `EventLog` entry + `EventResult` with `Activity:idle` |
| anything else / malformed JSON | fallback in `Parse` | flushed text + `EventLog`, one raw `level:"system"` entry |

Hermes-specific mechanics not present in Claude's format:

- **Synthetic tool-call ids** (`h.toolSeq`, `h.pendingByName`): Hermes's wire
  carries no `tool_use_id`. Each `tool_use` pushes an id (`ev.ToolCallID` if
  present, else `hermes-N`) onto a per-tool-name stack; the matching
  `tool_result` pops the most recent unclosed entry for that name. Assumes
  FIFO completion per tool name — holds only because HR-04's model is one
  process per turn (§4), so calls for one turn can't interleave across
  processes.
- **Text-delta buffering** (`h.text`, `h.flush`): avoids one log row per
  streamed token; `strings.Builder` accumulated, flushed as a single entry.
- **`clarify` → `ParsedEvent.Questions`** (`Source: hermes_clarify`): see §6,
  "known differences".
- **`todo_list` → TaskPanel**: unlike Claude's `TodoWrite` (an *input*, i.e.
  what the model is about to set), Hermes's `todo_list` tool is read from its
  *output* (`ev.Output`, the authoritative merged list after the call) since
  the input may be a partial merge or absent for a read-only call.

## 4. Turn/task lifecycle per runtime

| | Claude (`session.go`) | Hermes (`hermes_runtime.go`) |
|---|---|---|
| Process granularity | **One process per task** (autonomous) or one long-lived process for the whole interactive session. `runOnce` spawns it once; multiple turns are pumped through the same open stdin/stdout. | **One process per turn.** `runOnceHermes` loops, calling `hermesRunTurn` once per turn; each turn is a fresh `hermes chat` invocation that exits after its `result` line. |
| How a turn is sent | Stdin, `{"type":"user","message":{...}}` envelope (`userMessage`/`userMessageWithImages`), written by the `inputWriter` goroutine reading `inputCh`. | `--query-file -`: the prompt is piped via `cmd.Stdin` at process launch (`hermesRunTurn`), not through `inputCh` mid-process — `inputCh` is only used *between* processes (`runOnceHermes`'s wait-for-next-turn `select`). |
| How the conversation continues | The **same process** stays alive (stdin open) between turns of an interactive session; `--resume <uuid>` only matters across process restarts (crash recovery, rate-limit retry, context handoff). | Every turn after the first passes `--resume <hermes-session-id>` (`convID`, captured from the previous turn's `init` line) — there is no long-lived process to keep a conversation "open" in. |
| End of turn | `result` event on stdout. In autonomous mode the manager closes stdin right after (`runOnce`'s scanner loop, `closeInput()`) so the CLI exits cleanly; interactive sessions leave stdin open. | The process exits on its own after emitting `result` — always. `runOnceHermes` never explicitly closes anything; it just doesn't start a new process until the next prompt is ready. |
| End of task (autonomous) | `classifyTaskOutcome` (`task_outcome.go:56`) compares the task-queue's top pointer before/after the run plus the continue-session marker; see HR-04a/`RUNTIME-TASKS.md` reference in RT-01 item 4 → this is the shared logic both runtimes funnel into from `Session.Run`'s `default:` case (`session.go:958`). | Same `classifyTaskOutcome` call, same place — `runOnceHermes` returning `nil` from an autonomous run (`turn.finished`) is what lets `Session.Run`'s loop reach that `default:` branch, identically to `runOnce` returning `nil`. |
| Step/turn limit | `error_max_turns` result subtype → `s.stepLimitHit` (`handleEvent`, `session.go:1537`). | No such subtype on the wire — `hermesTurnHitStepLimit` (`task_outcome.go:161`) instead queries Hermes's own `state.db` after the turn, looking for the fixed "reached the maximum number of tool-calling iterations" message in the tail of the conversation. |

RT-06 unified everything ABOVE this row across the two loops without
collapsing the loops themselves — `PerTurnProcess()` is still the one
property `runOnce` branches on directly to pick `runOnceHermes` (never
`Config.IsHermes()` again, matching §11's invariant):

- **Dispatch.** `runOnce`'s Hermes branch (`session.go`) now reads
  `s.runtime.PerTurnProcess()` instead of `s.Config.IsHermes()` — the
  `Runtime` value already encodes which process model a session needs, so
  this is the only call site that still needs to know.
- **Argv building.** Both loops call `s.runtime.Args(s, autonomous, convID,
  imagePath)` instead of `s.buildCLIArgs`/`s.buildHermesArgs` directly —
  `claudeRuntime`/`hermesRuntime` (`runtime_claude.go`/`runtime_hermes.go`)
  still delegate to those same functions unchanged, so this is a
  redirection, not a rewrite (RT-05's "готово когда" — those functions'
  existing tests pass unmodified — still holds).
- **Per-run atomics reset.** `resetRunAtomics()` (`session.go`) is the one
  place `rateLimited`/`rateLimitInf`/`authErrorHit`/`sessionNotFoundHit`/
  `contextRestartHit`/`continueMarkerHit`/`stepLimitHit` are zeroed at the
  top of a fresh process — both `runOnce` and `runOnceHermes` call it
  instead of repeating the same seven-line block.
- **Outcome classification.** `classifySentinelError()` (`session.go`) reads
  those same atomics back (auth > session_not_found > rate_limit >
  context_restart priority) and returns the matching sentinel error, or nil
  if none fired. Both loops call it first; each then falls back to its own
  process-exit wrapping (`"claude exited: %w"` / `"hermes exited: %w"`,
  which differ in wording and are NOT shared) only when it returns nil.
- **What stayed loop-specific, deliberately:** `handleLine`'s signature
  (`line string, autonomous bool`, `session.go`) is unchanged — every
  existing test in `internal/session` calls it directly, and RT-06's
  "готово когда" requires the whole suite green *without edits*. Claude's
  scanner loop still calls `s.handleLine` per line (which internally uses
  the package-level `ParseLine`, exactly what `claudeRuntime.NewParser()`
  wraps — functionally identical, no behavior change). Hermes's
  `hermesRunTurn` still builds its own `newHermesStream()`/dispatches
  through `handleEvent` directly, since a `PerTurnProcess` runtime's
  mid-turn cutoff (`clarify`, §6) has no equivalent in Claude's loop to
  generalize into. `Parser`/`Runtime.NewParser()` (RT-05) exist as the
  seam for a *future* caller (RT-07's contract suite) to drive either
  runtime's stdout through one shared scanning loop; RT-06 does not need
  that generalization to unify the two loops' shared *state machine*
  (reset/dispatch/classify), only the argv-building and per-run bookkeeping
  above.

## 5. Where turn outcome is decided today (pre-RT-04 inventory)

This is the "list of debt" RT-01 item 5 asks for — every place a run's
success/failure/rate-limit/auth state is set, so RT-03/RT-04 know what they
are consolidating. All are `atomic.Bool`/`atomic.Pointer` fields on `Session`
(`session.go:297-311`), reset at the top of each run (`runOnce:1300`,
`runOnceHermes:146`) and read back after the process exits.

| Outcome | Atomic | Set from | File:function |
|---|---|---|---|
| Rate limit | `rateLimited`, `rateLimitInf` | structured `rate_limit_event` (Claude, via `handleEvent`→`EventRateLimit`) | `ratelimit.go:onRateLimit`, called from `handleEvent` (`session.go:1593`) |
| Rate limit (textual fallback) | same | a stderr line matching "rate limit"/"usage limit"/etc. | `ratelimit.go:detectRateLimitText`, called from `drainStderr` (`session.go:1758`) and `drainHermesStderr` (`hermes_runtime.go:491`) |
| Rate limit (Hermes 429-behind-401) | same | a failed turn's `agent.log` tail shows `RateLimitError`/`Credential 429` for this conversation, even though the *reported* error was something else (a rotated stale credential's 401) | `task_outcome.go:hermesTurnHitRateLimit`, called from `hermes_runtime.go:355` |
| Auth error (403) | `authErrorHit` | stderr line containing "403" + forbidden/authenticate/unauthorized | `session.go:isAuthError`, called from `drainStderr` (`session.go:1761`), `drainHermesStderr` (`hermes_runtime.go:494`), and the result-text check in `handleEvent` (`session.go:1542`, since Claude can report a 403 *on stdout* in `result.result` instead of stderr) |
| Session not found (Hermes `--resume` target gone) | `sessionNotFoundHit` | stderr line `"Session not found: …"` | `session.go:isSessionNotFoundError`, called only from `drainHermesStderr` (`hermes_runtime.go:497`) — Claude has no equivalent, it owns the whole conversation itself |
| Context restart (LN-15 handoff) | `contextRestartHit` | a finished turn's `modelUsage.contextWindow` crosses the configured threshold | `session.go:checkContextRestart` (`session.go:1634`), called from `handleEvent`'s `EventResult` arm for both runtimes (it's runtime-agnostic — reads `SessionResult.ModelUsage`) |
| Step/turn limit | `stepLimitHit` | Claude: `result.subtype == "error_max_turns"` (`handleEvent`, `session.go:1537`). Hermes: `state.db` tail scan, see §4 | two separate call sites, no shared code today |
| Continue-session marker | `continueMarkerHit` | ```` ```ask-user ```` block with `kind:"continue_session"` in the result text (autonomous runs only) | `handleEvent`'s `EventResult` arm (`session.go:1546-1562`), via `ParseAskUserQuestion` (`parser.go:124`) — shared for both runtimes since it operates on `SessionResult.ResultText`, which both adapters fill |
| Generic process error | none (plain Go `error` return) | `cmd.Wait()` non-nil, stdin/stdout pipe errors, scanner errors | `runOnce` (`session.go:1383`), `hermesRunTurn`/`runOnceHermes` (`hermes_runtime.go:200,370`) |

`Session.Run`'s big `switch` (`session.go:866`) is the single place all of
these are read back into retry/pause/rotate-session decisions — but the
*setting* side above is six different call sites across two files plus
stderr scanning. This is exactly the "россыпь атомиков" RT-04 replaces with
one `ParsedEvent.Failure` event.

## 6. Known runtime differences that a format alone cannot hide

These need an adapter-side decision, not just a shared struct — call this out
explicitly wherever RT-03+ touch them:

- **Headless `clarify` self-answers itself.** `hermes chat --query-file -`
  has no attached user, so when the agent calls `clarify` Hermes answers its
  own tool call ("no user available… pick the best option") and the agent
  carries on with that guess *before* the manager ever sees the questions.
  The fix (adapter-side): `hermesStream` attaches the questions to the
  `tool_result` as `ParsedEvent.Questions` (`Source: hermes_clarify`), and
  `hermesRunTurn`'s `dispatch` (not `handleEvent`) reacts to any such
  question by killing the process immediately (`cancelTurn()`,
  `hermes_runtime.go`) before it can act on the bogus auto-answer, then
  resumes the same conversation once the real answer is ready. The ask-user
  marker path is the opposite: the marker only ever arrives on the final
  `result` line, where the process is already exiting on its own — no
  mid-stream cut needed, `clarifyQuestions()` in `hermes_runtime.go` filters
  by `Source` precisely so the marker case doesn't trip this cutoff. Claude
  Code's `AskUserQuestion` tool is unavailable under headless `-p` (RT-02) so
  it never reaches this path today; if upstream ever re-enables it, it would
  need the same kind of adapter-specific stop-and-wait decision.
- **One process per task vs. one process per turn** (§4) is not just an
  implementation detail — it changes what "close stdin to end the turn"
  means (Claude) vs. "the process exits on its own, no signal needed"
  (Hermes), and what `--resume` resumes (a whole conversation restart vs.
  the very next turn). RT-05's `Runtime` interface has to expose this as a
  first-class property, not paper over it.
- **Permission requests exist only for Claude.** Hermes applies its own
  approval policy to dangerous commands with no interactive round-trip on
  the wire (`hermes_runtime.go:warnUnsupportedHermesOptions` warns when a
  session's `permission_mode` is set but ignored). A common format either
  needs `Permission` to stay Claude-only (nil-able, as today) or Hermes needs
  a synthetic no-op — RT-03 decided the former implicitly by never touching
  `EventPermission`.
- **Token usage timing**: Claude reports usage on every `assistant` message
  (i.e. multiple times per turn); Hermes reports it exactly once, on the
  `result` line. `hermesStream` compensates by synthesizing one
  `EventLog{Usage}` from `result.tokens` (`hermes_parser.go:196`) so the
  context bar/token totals still move — but this means Hermes's "per-turn"
  usage event fires at a different point in the stream than Claude's, which
  matters if a future consumer assumes usage arrives mid-turn.
- **Rate limit and auth detection are best-effort text matching for both
  runtimes' stderr**, and additionally log-file scraping for Hermes's
  429-behind-401 case (§5) — there is no structured signal from Hermes at
  all, only from Claude's `rate_limit_event`. Any new outcome kind (RT-04)
  will likely need the same "structured event where available, text/log
  fallback otherwise" shape for Hermes specifically.

## 7. How to add a runtime — draft checklist (finalized by RT-08)

This is intentionally rough; RT-08 is the task that turns it into a real
walkthrough once RT-02..07 exist. For now, based on what Claude/Hermes
actually required:

1. Write a parser/adapter (`parser.go`/`hermes_parser.go` pattern) that turns
   the runtime's own wire format into `[]ParsedEvent` — reuse `ParsedEvent`
   as-is; do not add runtime-specific fields to it (add a case to an existing
   field's enum instead, e.g. a new `Question.Source`, once RT-03 lands).
2. Decide the runtime's process model (§4) up front: one process for
   the whole task, or one per turn. This changes how you wire `inputCh` and
   what "the turn ended" means for your adapter.
3. Add launch-arg building (`buildCLIArgs`/`buildHermesArgs` pattern) and any
   environment scrubbing your CLI needs (see `hermesEnv`,
   `hermes_runtime.go:69`, for why this mattered for Hermes specifically —
   inherited parent-session environment variables broke path resolution).
4. Wire stderr scanning for whatever your runtime's rate-limit/auth signals
   look like (§5) — there is no structured contract to lean on if the
   runtime doesn't emit one.
5. Feed everything through the existing `Session.handleEvent` — do not
   duplicate its logic. If your runtime needs something handleEvent doesn't
   support yet (like Hermes's `clarify` early-kill), that's a sign the
   *format* needs a §6-style adapter hook, which the later RT tasks are
   meant to generalize, not a reason to bypass `handleEvent`.
6. Add a fake binary under `cmd/` (see `cmd/fakeclaude`, `cmd/fakehermes`)
   with scripted scenarios so the whole pipeline is testable without a real
   API call — `CM_REAL_<RUNTIME>=1` opt-in tests are for occasional
   real-wire verification only, never the default gate (RUNTIME-TASKS.md
   §Инварианты).
7. Update `internal/config.SessionConfig.Runtime`'s accepted values and
   `Config.IsHermes()`-style helper, and the runtime picker in the session
   card UI (`SetSessionRuntime`, `docs/architecture.md`'s Wails bindings
   table).
8. Update this document (RT-01 through RT-08's own "готово когда" clauses
   all name `docs/runtimes.md` explicitly) plus the doc-sync matrix in
   `CLAUDE.md`.

RT-07's contract test suite (`internal/session/runtime_contract_test.go`,
§11a above) is the actual acceptance bar — this checklist is a map to get
there, not a substitute for passing it.

## 8. `AskUserQuestion` у Claude Code (RT-02, checked on v2.1.280)

Question RT-01 §6 left open: what happens when the model calls Anthropic's
`AskUserQuestion` tool under `claude -p` with the exact flags `buildCLIArgs`
builds (`-p --verbose --input-format stream-json --output-format stream-json
--include-partial-messages --replay-user-messages --session-id <uuid>
--model haiku --permission-mode bypassPermissions`)? Verified two ways: a
recorded real run (`testdata/claude-stream/ask-user-question.jsonl`,
replayed offline by `TestParseClaudeStream_AskUserQuestionUnavailable`,
`internal/session/session_realclaude_test.go`) and a live opt-in probe
(`TestRealClaude_AskUserQuestionUnavailable`, `CM_REAL_CLAUDE=1`).

**Finding: the tool is simply not offered to the model in `-p` mode.** No
`tool_use` block named `AskUserQuestion` ever appears, and — because the
model never tries to call it — there's no `permission_request` either (a
`permission_request` only fires for a tool call Claude Code actually
attempts). The model instead:

1. Runs `ToolSearch` looking for a matching deferred tool (Claude Code
   exposes a large tool catalog behind a search-first indirection; the
   system prompt's tool list omits `AskUserQuestion` entirely in `-p` mode).
2. Gets `"No matching deferred tools found"` (or, if `--allowedTools
   AskUserQuestion` is passed explicitly, a list of the *other* deferred
   tools it *does* have — `AskUserQuestion` is excluded from that list too).
3. Replies in plain text explaining it has no such tool and asks the caller
   to either answer inline or restate the request.
4. The turn ends completely normally: `stop_reason:"end_turn"`,
   `result.subtype:"success"`, no error, no hang. Nothing about the turn's
   lifecycle changes — `handleEvent`'s `EventResult` arm and
   `runOnce`'s close-stdin-on-result logic behave exactly as for any other
   turn.

This matches a known, currently-open upstream regression: `AskUserQuestion`
worked under `-p` in Claude Code 2.1.185 and was disabled in later versions
(anthropics/claude-code#77994). It is not something this codebase's flags
can turn back on — it was tried with `--allowedTools AskUserQuestion`
explicitly and the tool was still absent from the model's tool list.

**Chosen way to "answer" it (until upstream restores the tool): none needed.**
Since Claude Code's own harness refuses the tool call before it happens, the
manager never sees a `tool_use`/`permission_request`/hang to react to — the
turn is indistinguishable from a normal text-only turn from `ParsedEvent`'s
point of view. RT-03's shared `ParsedEvent.Questions`/`Question.Source`
design should still add a `claude_ask_user_question` source (per RT-01 §6)
for the day upstream re-enables it, but no adapter code is needed *today* to
make Claude Code sessions behave correctly — they already do, because there
is nothing to intercept. If upstream ships a fix, the two RT-02 tests above
are what will start failing (`sawAskUserUse`/`sawAskUserToolUse` becoming
true) and should be the trigger to implement real handling then.

## 9. Вопросы пользователю (RT-03): единое событие, три источника

`ParsedEvent.Questions []Question` (`parser.go`) is the one place a pending
ask-user question surfaces, whichever adapter produced it. `Question.Source`
names the mechanism so `session.go`/`hermes_runtime.go` can apply the
source-specific stop-and-resume rule below; the rest of the pipeline
(`pendingQuestionSet`, `PendingQuestion`, `AnswerQuestion`, the timeout
fallback, `KindContinueSession`) is fully shared.

| `Source` | Runtime(s) | Where it's produced | How the answer is delivered |
|---|---|---|---|
| `ask_user_marker` (`QuestionSourceAskUserMarker`) | Claude, Hermes | ```` ```ask-user ```` fenced block in a `result` turn's text, parsed by `questionFromMarker`/`ParseAskUserQuestion` (`parser.go`) and attached to `ParsedEvent.Questions` on the `EventResult` itself — never mid-stream. Autonomous runs only (see `session_askuser_test.go`). | The process is already exiting on its own when the marker arrives (it's on the final `result` line), so no process needs to be killed. `AnswerQuestion` just writes the answer to stdin as the next turn's prompt — same for both runtimes. `KindContinueSession` short-circuits this (auto-answered "no", never pauses) and a configurable timeout auto-picks the first listed option for any other kind. |
| `hermes_clarify` (`QuestionSourceHermesClarify`) | Hermes only | The Hermes `clarify` tool: `parseClarifyInput` reads its `tool_use` input, `hermesStream` (`hermes_parser.go`) stashes the questions in `h.clarifyByID` and attaches them to `ParsedEvent.Questions` on the matching `tool_result` — mid-stream, not on `result`. | `hermes chat --query-file -` has no attached user, so Hermes answers `clarify` itself before the manager can react. `hermesRunTurn`'s `dispatch` (not `handleEvent`) filters `ev.Questions` down to this source with `clarifyQuestions()` and, on a non-empty match, kills the process immediately (`cancelTurn()`) so the agent's bogus self-answer never gets acted on, then resumes the conversation via `--resume` once the real answers are ready (`clarifyAnswerMessage`, `session.go`). |
| `claude_ask_user_question` (`QuestionSourceClaudeAskUserQuestion`) | Claude (reserved) | Would come from Claude Code's `AskUserQuestion` tool — RT-02 found the tool is not offered to the model under `-p` in the current CLI version (anthropics/claude-code#77994), so no adapter code produces this source today. | Not implemented — no adapter fills it, so there is nothing to deliver an answer to yet. If upstream re-enables the tool, this is the source to route `tool_use`/`tool_result` handling through; §8 above has the trigger to watch for. |

Everything past `Question.Source` is runtime-agnostic: `pendingQuestionSet`
(multi-question sets, currently only `hermes_clarify`) and single-question
asks both resolve through the same `PendingQuestion`/`AnswerQuestion` path in
`session.go`, so the UI and `cm-mcp`/RPC layer never need to know which
source produced a question.

## 10. Исход хода (RT-04): `ParsedEvent.Failure` вместо шести атомиков

§5 above listed six call sites that each set their own `atomic.Bool` inline.
RT-04 keeps the six atomics on `Session` (`runOnce`/`runOnceHermes` still
read them back individually into `errRateLimited`/`errAuthError`/
`errSessionNotFound`/`errContextRestart` — that reading side is unchanged)
but replaces every SETTING call site with one classified value and one
consolidation point:

- `TurnFailure{Kind, Message, ResetsAt}` (`turn_failure.go`) — `Kind` is one
  of `rate_limit`\|`auth`\|`session_not_found`\|`context_restart`\|
  `step_limit`\|`other`.
- `ParsedEvent.Failure *TurnFailure` — an adapter fills this when the event
  it just parsed carries a classified failure. Only `parser.go`'s
  `handleResult` fills it today (Claude's two result-line signals: an
  `error_max_turns` subtype, or 403 text in `result.result`); Hermes's
  result line reports its own error through `hermesTurn.failed`/
  `stream.lastError` instead (see below), not through `ParsedEvent.Failure`,
  because a failed Hermes result still needs `dispatch`'s own rate-limit vs.
  auth vs. plain-error branching before the process exits.
- `Session.applyFailure(*TurnFailure)` (`ratelimit.go`) — the single place a
  `TurnFailure` becomes session state: one `switch` on `Kind`, one atomic
  `.Store(true)` per branch (`rate_limit` goes through the existing
  `onRateLimit` so its `RateLimitInfo`/UI event/`rateLimitUntil` bookkeeping
  is untouched). `nil` and `KindOther` are no-ops.
- `ClassifyStderrLine`/`ClassifyHermesStderrLine` (`turn_failure.go`) —
  stderr lines never go through `ParsedEvent` (they aren't stdout), so they
  classify directly into a `*TurnFailure` that `drainStderr`/
  `drainHermesStderr` feed straight into `applyFailure`. Hermes additionally
  recognizes `session_not_found`, which Claude has no equivalent for.

| Исход (`Kind`) | Кто определяет | Куда попадает | Что делает ядро |
|---|---|---|---|
| `rate_limit` | Claude: `handleRateLimit`→`EventRateLimit`→`onRateLimit` (structured `rate_limit_event`, unchanged, does not go through `TurnFailure`). Both runtimes' stderr: `detectRateLimitText` (unchanged, runs before classification — see `ClassifyStderrLine`'s comment). Hermes only: `hermesTurnHitRateLimit` (429-behind-401, `hermes_runtime.go`, log-scrape after process exit). | `s.onRateLimit` directly, or via `applyFailure{Kind:rate_limit}` when built from a `TurnFailure` (the 429-behind-401 path still calls `onRateLimit` directly, not through `applyFailure`, since it already has a `RateLimitInfo`) | `rateLimited`+`rateLimitInf` armed → `runOnce`/`runOnceHermes` return `errRateLimited` → `Run`'s pause/fallback-model logic |
| `auth` | Claude: `handleResult` (`parser.go`) classifies 403 result text into `ParsedEvent.Failure`; both runtimes' stderr via `ClassifyStderrLine`/`ClassifyHermesStderrLine`; Hermes's `dispatch` closure for a failed result (`stream.lastError`). | `applyFailure{Kind:auth}` | `authErrorHit` armed → `errAuthError` → `Run` pauses 60s before retrying |
| `session_not_found` | Hermes stderr only: `ClassifyHermesStderrLine` (`"Session not found: …"`) | `applyFailure{Kind:session_not_found}` | `sessionNotFoundHit` armed → `errSessionNotFound` → `Run` clears the stale resume id and retries as a fresh conversation |
| `context_restart` | `checkContextRestart` (`session.go`), runtime-agnostic — reads `SessionResult.ModelUsage` from either adapter, only armed when `Config.ContextHandoff` is set | `applyFailure{Kind:context_restart}` | `contextRestartHit` armed → scanner loop closes stdin even for an interactive session → `errContextRestart` → `Run` rotates the CLI session id and resends the distilled handoff |
| `step_limit` | Claude: `handleResult` classifies `subtype=="error_max_turns"` into `ParsedEvent.Failure`. Hermes: `hermesTurnHitStepLimit` (`task_outcome.go`, `state.db` tail scan after the process exits) | `applyFailure{Kind:step_limit}` | `stepLimitHit` armed → read by `Session.Run`'s task-outcome classification (`classifyTaskOutcome`'s "unfinished" reason string) |
| `other` | Reserved — no adapter produces it today; exists so a future classifier can say "this was a failure, not a success" without silently mapping to one of the five specific Kinds above | `applyFailure` no-ops on it | nothing; the run still ends via its normal non-nil-error/non-zero-exit path |

Continue-session (`KindContinueSession` on a `Question`, not a `TurnFailure`
`Kind`) deliberately stays outside this table — RT-03 already covers it as
an answered question, not a failure, and `continueMarkerHit` is untouched by
RT-04.

## 11a. Контрактные тесты адаптеров (RT-07)

`internal/session/runtime_contract_test.go` is one table-driven suite that
runs the SAME assertions against every `Runtime` (`contractRuntimes`):
`TestContract_InitCarriesConversationID`, `_ToolCallAndResultShareID`,
`_AgentTextReachesLog`, `_QuestionDeliveredAndAnswerResumes`,
`_TurnEndsWithTokenUsage`, `_FailureKinds` (one subtest per `TurnFailure.Kind`
the runtime can produce — `context_restart` is deliberately excluded, see
below), `_ConversationSurvivesRestart`. Each `contractCase` in
`contractRuntimes` names the prompts that steer that runtime's fake binary
(`cmd/fakeclaude` directory-mode scenario matching, `cmd/fakehermes`
substring matching — see its own doc comment) into each scenario; a Kind
with no prompt set for a runtime (e.g. Hermes has no `stepLimitPrompt` — its
step limit is a `state.db` tail scan covered by `task_outcome_test.go`
instead) skips that subtest for it rather than failing.

New fixtures this task added: `testdata/scenarios/auth-error-demo.json`
(403 in `result.result`, matches `claude/auth`), `step-limit-demo.json`
(`subtype:"error_max_turns"`, matches `claude/step_limit`),
`rate-limit-exceeded-demo.json` (`status:"exceeded"`, matches
`claude/rate_limit`), `resume-demo.json` (a two-turn interactive exchange
for `_ConversationSurvivesRestart`'s Claude case — the existing
`multi-turn.json` needs three `await_stdin` steps, one more than this test
sends). `cmd/fakehermes/main.go` gained two prompt substrings:
`FAIL_403` (writes a 403/forbidden stderr line, exit 1) and
`FAIL_SESSION_NOT_FOUND` (writes Hermes' own `"Session not found: <id>"`
stderr line, exit 1) — both exercised only through
`ClassifyHermesStderrLine`, no new scenario JSON needed since fakehermes is
driven by query substrings, not scenario files.

**Why `context_restart` is not in `_FailureKinds`:** `checkContextRestart`
(§10's table) is runtime-agnostic — it reads `SessionResult.ModelUsage`
identically from either adapter — so it has no per-runtime adapter behavior
for this suite to contract-test; `session_contexthandoff_test.go` already
covers it once, and duplicating it per runtime here would test the same
shared code twice under a different name.

**Why `_ConversationSurvivesRestart` doesn't assert on process count:**
`PerTurnProcess()` (§11 below) is a property callers are allowed to branch
on, not a shared contract — Hermes launches two processes for a two-turn
exchange, Claude launches one. The shared assertion is behavioral: the
second turn's reply reflects the first turn's content (Hermes via
`--resume`, Claude via the same long-lived process), read off `EvtLog`
`Level:"text"` entries rather than `EvtResult`, since Claude's single
long-lived interactive process only emits one `EventResult` at the very end
of the session while Hermes emits one per turn (§4) — asserting on
`EvtResult` per turn would silently only test Hermes.

**A pending retry's default 30 s pause (`retryDelay`) is not something a
contract test should wait through** — every `contractRuntimes` session sets
`RetryDelay: 1` (`RateLimitPauseSec: 1` too), the same pattern
`TestHermesRuntime_RateLimitBehind401_ResumesDespiteSoftStop`
(`hermes_runtime_test.go`) already used for Hermes; this file needed it for
Claude too, since a killed `runOnce` returns a generic (non-sentinel) error
that goes through the same `default:` retry-with-pause branch in `Run()`'s
big `switch` (§5's table, "Generic process error" row).

**How to add a runtime to the contract (RT-01 §7 point 6):** append one
`contractCase` to `contractRuntimes` with a `newSession` closure and enough
prompts to steer the new fake binary into each scenario this suite drives;
any Kind the runtime genuinely cannot produce is left as `""` and skips
cleanly. Passing this whole file with no per-runtime special-casing inside
the test functions themselves (only inside `contractCase`'s data) is what
"the runtime is connected" means.

## 11. Интерфейс `Runtime` (RT-05)

`internal/session/runtime.go` defines the seam: `Runtime` (backend identity,
process granularity, argv building, parser factory) and `Parser` (one
process's stdout translator, matching `hermesStream`'s existing shape).
`runtime_claude.go`/`runtime_hermes.go` are the two implementations —
thin wrappers over the existing code, not a relocation of it:

| Method | `claudeRuntime` | `hermesRuntime` |
|---|---|---|
| `Name()` | `"claude"` | `"hermes"` |
| `PerTurnProcess()` | `false` — one process for the whole task/session (§4) | `true` — one process per turn, `--resume`'d by id |
| `Args(s, autonomous, convID, imagePath)` | `s.buildCLIArgs(autonomous)`, ignores `convID`/`imagePath` (Claude keeps `resumeSessionID`/`CLISessionID` on `Session` itself and has no per-turn image flag) | `s.buildHermesArgs(convID, imagePath)`, ignores `autonomous` (the autonomous protocol text rides in front of the first turn's prompt via `hermesPreamble`, not an argv flag) |
| `NewParser()` | `&claudeParser{}` wrapping the stateless `ParseLine` — `Flush()`/`LastError()` are no-ops | `&hermesParserAdapter{stream: newHermesStream()}` — a **fresh** `hermesStream` per call, required because it buffers text deltas and tracks synthetic tool-call ids scoped to one process |

`Session.runtime` is set once in `New()` via `newRuntime(cfg
config.SessionConfig)` — the only place code picks a `Runtime` by
`Config.IsHermes()`. RT-06 (§4) is what routes `runOnce`/`runOnceHermes`
through it: argv building (`Runtime.Args`), the per-run atomics reset
(`resetRunAtomics`) and outcome classification (`classifySentinelError`) are
now shared, and `runOnce`'s own Claude/Hermes dispatch reads
`runtime.PerTurnProcess()` instead of re-checking `Config.IsHermes()`.
`ParseLine`/`hermesStream.Parse` themselves are still called the way they
were before RT-05/06 (`handleLine` for Claude, `newHermesStream()` directly
in `hermesRunTurn` for Hermes) — see §4's "what stayed loop-specific"
bullet for why unifying the scanning loop itself was not needed to satisfy
RT-06's "готово когда".

**Готово когда (RT-05):** existing tests unmodified and green (verified:
`TestBuildCLIArgs_*`, `internal/session`'s full suite, `go build`/`go vet`
across the module); this section describes each method's contract.

**Готово когда (RT-06, §4):** one shared reset/dispatch/classify path for
both runtimes, `runOnce`/`runOnceHermes` route argv building through
`Runtime.Args`, the full test suite (`go build`/`go vet`/`go test ./...`,
including `internal/control`'s fakeclaude e2e test and
`hermes_runtime_test.go`) is green without editing any existing test.

**Готово когда (RT-07, §11a):** `internal/session/runtime_contract_test.go`
green on both runtimes, `testdata/scenarios/scenarios_doc.md` documents the
three new fixtures it needed, `go build`/`go vet`/`go test ./...` green
(pre-existing `internal/fsutil` case-sensitivity failures are unrelated to
this block, observed on `master` before this task).

