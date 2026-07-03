# fakeworker scenarios (MIXED-TASKS.md MP-07)

Each file is a `testkit.WorkerScenario`: scripted responses that
`cmd/fakeworker` (or `testkit.NewFakeWorker` in-process) serves to
`POST /chat/completions`, one response per request, in order. A request past
the last response gets HTTP 500 so a drifting test fails loudly.

Response fields: `status` (0/200 = SSE success, >=400 = error reply with
`body`), `content` + `finish_reason` (`stop`|`length`), `chunk_size` (runes
per SSE delta), `repeat` (serve the same step N consecutive requests).

All patch-bearing scenarios target the shared seed file `greet.go`
(`return "TODO"` anchor) — the same content the mixed e2e scenarios seed via
`seed_files` and `internal/worker/fakeworker_test.go` uses as `greetSeed`.

| File | Covers |
|---|---|
| `clean-round1.json` | Valid patch, green on round 1 |
| `broken-then-clean.json` | FIND anchor with a dropped line (step37 quirk) → rejection feedback → clean patch on round 2 |
| `rate-limit-storm.json` | 3×429 then success — progressive backoff in the client |
| `length-continuation.json` | `finish_reason=length` ×2 — reply reassembled across continuations |
| `length-loop.json` | Endless `length` replies — continuation cap stops the loop |
| `missing-end.json` | Missing `>>>END` between patches (nemotron-ultra quirk) — parser splices with a warning |

Used by: `internal/worker/fakeworker_test.go` (client-level),
`testdata/e2e/mixed-*.json` via `internal/control/e2e_test.go`
(control-plane level).
