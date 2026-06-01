# Scenario Library

Maps every scenario file in `testdata/scenarios/` to the Session Status Flow
state or app feature it exercises. Used by `fakeclaude` (directory mode) and
validated by `internal/testkit/conformance_test.go`.

## Session Status Flow

```
Idle → Starting → Working ⇄ WaitingPermission
                    ↓
              RateLimited → Retrying → Working
                    ↓
              Stopping → Idle
                    ↓
                 Error
```

---

## Scenarios

### happy-path.json
**State:** Working → Idle (success)  
**Match:** `simple`, `happy`, `hello`

Exercises the baseline flow: `system/init` → one `assistant` turn → `result`
(success). Validates that `InitInfo`, `TokenUsage`, and `SessionResult` are
all populated on a clean run. Used as the reference scenario in unit tests.

---

### error-exit.json
**State:** Working → Error (non-zero exit)  
**Match:** `crash`, `fail`, `error`

Exercises the error path: `system/init` → `assistant` → `result` (subtype
`error`) with `exit_code: 1`. Confirms the manager detects a failed session
and transitions to the Error state.

---

### permission-allow.json
**State:** Working ⇄ WaitingPermission → Working → Idle  
**Match:** `refactor`, `edit`

Exercises the permission round-trip when the user **approves**: the session
emits a `permission_request`, blocks on `await_stdin` for a
`permission_response` with `decision: allow`, then continues and emits a
success `result`. Validates `PermissionRequest.Tool`, `.ID`, and `.RiskLevel`.

---

### permission-deny.json
**State:** Working ⇄ WaitingPermission → Error  
**Match:** `delete`, `drop`

Exercises the permission round-trip when the user **denies**: same flow as
`permission-allow` but the `on: {perm.decision: deny}` branch skips the
success events and emits `result` (subtype `error`). Validates that both
conditional branches are parseable by the parser.

---

### rate-limit.json
**State:** Working → RateLimited → Retrying → Working → Idle  
**Match:** `rate-limit`, `throttle`

Exercises rate-limit detection and recovery:
- Emits `rate_limit_event` with `utilization: 0.9` and `resetsAt: 1779584400`
  (both above the 0.75 threshold that triggers RateLimited state).
- After a simulated pause (`delay_ms: 200`) emits a second assistant turn,
  representing the session resuming after the reset window.

Validates `RateLimitInfo.Utilization`, `.ResetsAt`, `.Status`, and
`.SurpassedThreshold` are correctly extracted.

---

### loop.json
**State/Feature:** LoopDetector (three identical tool calls)  
**Match:** `loop`, `repeat`

Exercises the loop-detection heuristic: emits three consecutive `assistant`
messages each containing the same `Bash` tool_use with identical `command`
input (`grep -r TODO . --include='*.go'`). The LoopDetector in
`internal/optimization/loop.go` fires when the same tool+input appears ≥ 3
times in the last 20 calls.

Validates that all three events parse as `EventLog` with `ToolName: "Bash"`
and that `ToolInput` is identical across all three (the condition the detector
checks).

---

### context-growth.json
**State/Feature:** Context utilization monitor, auto-restart trigger  
**Match:** `context-grow`, `outgrow`

Exercises the auto-restart-on-context-growth path:
- Three assistant turns with `input_tokens` growing: 40 000 → 80 000 → **155 000**
- 155 000 exceeds the 75% threshold of the 200 000 token `context_window`
  (threshold = 150 000), which triggers `internal/optimization/context.go` to
  schedule an auto-restart.
- The `result` event includes `modelUsage["claude-sonnet-4-6"].contextWindow: 200000`
  so the monitor can confirm the window size.

Validates `TokenUsage.InputTokens` and `ModelUsage.ContextWindow` extraction.

---

### multi-turn.json
**State/Feature:** Bidirectional streaming — multiple `user_message` inputs  
**Match:** `multi-turn`, `conversation`

Exercises the bidirectional `--input-format stream-json` flow: three interleaved
`await_stdin` (user messages) and three `assistant` response turns before the
final `result`. Confirms the `Runner` correctly reads and sequences multiple
stdin messages without losing events.

Validates that the scenario contains ≥ 3 `await_stdin` steps, ≥ 3 assistant
`EventLog` events, exactly 1 `EventInit`, and exactly 1 `EventResult`.

---

### budget-exceeded.json
**State/Feature:** Budget enforcement (`max_budget_usd`)  
**Match:** `budget`, `over-budget`

Exercises the cost-guard check: a large Opus session emits `total_cost_usd:
1.50`, which exceeds the representative `max_budget_usd: 0.50` a user might
configure. The `result` also includes `cache_creation_input_tokens: 80000` to
cover the cache-cost accounting path.

Validates that `SessionResult.TotalCostUSD` is parsed correctly and is
strictly greater than `max_budget_usd`.
