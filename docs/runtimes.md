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
| `Clarify []ClarifyQuestion` | Questions of a Hermes `clarify` tool call, attached to its `tool_result` | Hermes only | `hermesRunTurn`'s `dispatch` (not `handleEvent` — see §6 "known differences" and RT-03) |

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
| `{"type":"tool_result",...}` | `Parse` case `"tool_result"` | flushed text + `EventLog` with one entry (`level:"tool_result"`/`"error"`), `Activity:thinking`; `name=="todo_list"` fills `Todos` from the *output*; if this call's id matches a pending `clarify`, `Clarify` is filled here |
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
- **`clarify` → `ParsedEvent.Clarify`**: see §6, "known differences".
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
  The fix (already shipped, `2064fe0`) is adapter-side: `hermesStream`
  attaches the questions to the `tool_result` as `ParsedEvent.Clarify`, and
  `hermesRunTurn`'s `dispatch` (not `handleEvent`) reacts to a non-empty
  `Clarify` by killing the process immediately (`cancelTurn()`, `hermes_runtime.go:329`)
  before it can act on the bogus auto-answer, then resumes the same
  conversation once the real answer is ready. Claude Code has an
  `AskUserQuestion` tool that (per RT-02) needs its own, likely different,
  handling — a single shared "Questions" field is the RT-03 goal, but *how*
  each runtime stops itself to wait is adapter-specific by nature (Hermes:
  kill the process; Claude: probably keep it alive on stdin, TBD by RT-02).
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

RT-07's contract test suite (once it exists) is the actual acceptance bar —
this checklist is a map to get there, not a substitute for passing it.
