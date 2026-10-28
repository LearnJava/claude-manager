# A/B results — plain Claude Code (A) vs Claude Manager (B), 2026-10-28

One run per arm, 5 tasks (T1 S, T2 S, T3 M, T4 L, T5 L), same base 001193b, same task texts,
sonnet / effort medium / bypassPermissions in both; B: Chat (opus) sliced into 11 subtasks,
Developer 1 ran the queue with gates + strict gates (2 rounds), handoff 150k, context primer,
learned rule, auto skills.

## Quality (criterion 1: no worse)
| | A | B |
|---|---|---|
| Hidden acceptance tests (80) after each task / final | 80/80 / 80/80 | 80/80 / 80/80 |
| Final go build/vet/test ./... | green (1 timing test flaked under load, 5/5 on rerun) | green |
| Playwright | 303 passed (no new specs) | 311 passed (+8 new specs) |
| New gofmt issues (test files) | 1 | 2 |
| Test lines added | 590 | 1269 |
| Blind review (opus, X/Y hidden) | better in T1 (low) | better in T2 (low), T3 (medium), T4 (low), T5 (low-medium) |

Notable review findings: A T3 Save renames config.toml to .bak before the temp file — a window
without config.toml, on Windows a locked second rename loses the config (the failure the task is
about). B T3 validates the merge without overlay workers (rare edge). B T5 has a clock-dependent test
and status colours keyed on session statuses. A T5: no manager-level test of the run task, broken
Russian plurals, no Playwright spec. A T4: eval stub fakes a forecast; B T4 copies the pointer parser.

## Involvement (criterion 2)
Interventions during tasks: A 0, B 0. Launch actions: A 5 (each next task started by the human
when the previous one ended), B 2 (one message to Chat, one start of Developer 1).
No permission prompts (bypass), no rate limits, no context overflow, no stops midway in either arm.

## Cost and time (criterion 3)
| | A | B |
|---|---|---|
| $ at API prices (transcripts, final count) | 4.40 | 8.61 = 6.44 runs + 1.17 slicing (opus) + 0.91 skill autopilot (4 calls) + 0.09 empty session |
| sessions | 5 | 17 (11 developer runs + Chat + 4 autopilot + 1 empty) |
| cache write, tokens | 331k | 878k (each new session re-writes ~60k of prefix) |
| per task $ | 0.43 / 0.30 / 0.44 / 1.63 / 1.60 | 0.49 / 0.49 / 0.98 / 2.00 / 2.48 |
| model calls | 151 | 255 |
| cache read, M tokens | 10.7 | 13.8 |
| peak context | 115k | 92k (handoff never triggered) |
| elapsed | 1 h 37 min (instant human reactions) | ~3 h 10 min, of it ~65-70 min manager gates |
| red build/test runs | 7 of 38 | 7 of 44 |
| tool errors / repeated errors | 4 / 1 | 2 / 2 |
| commands hung to timeout | 2 (heredoc, T1 and T3) | 0 |

## Verdict by the pre-registered rule
1 holds (B no worse, slightly better tests and review). 2 equal (0 vs 0 interventions; B needs
fewer launches). 3 worse for B (x2.0 dollars, x2 elapsed). By the rule: in this run the manager
did not help enough to pay for itself; its plus was test thoroughness and unattended chaining.

## Why (conditions of this run)
- Tasks were fully specified (fixed interfaces, Done-when = acceptance) and each fit one context,
  so slicing, handoff and restarts had nothing to rescue.
- bypassPermissions removed the permission-wait problem the manager solves.
- Strict gates never went red: 65+ min of waiting bought nothing measurable here.
- n = 1 per arm: cost differences of ~20-30% are within run-to-run noise.

## What would make B cheaper and faster (candidate manager tasks)
0. Test time budget: `go test ./...` on an idle machine grew 111 s (26.09) -> 156 s (14.10) -> 222 s
   (24.10) -> 444 s (28.10), 964 -> 1508 tests; 33 tests over 10 s take 748 s, 568 s of them added
   after 14.10. Mark tests over 10 s as slow (nightly), record suite duration per gate run, alarm
   when the fast tier exceeds ~4 min. No slowdown of the manager or of agent steps was found.
1. Gates once: skip the manager's strict gate when the session's cm-task-finish already ran the gate
   on the same HEAD, or run it before the merge so it protects master (here ~75 min, all green).
2. Two test tiers: fast per task, full suite nightly / at queue end.
3. Slice by concern, not by layer: 11 sessions instead of ~6 cost re-orientation and ~60k cache
   writes each (T5 in 4 runs: ~+$0.7, +20 min).
4. Cheaper overhead: slicing on sonnet; skill autopilot batched at queue end on a cheap model
   (Chat + autopilot = 25% of B's dollars).
5. Show "gates running: go test ./... (3:20)" in the UI and app.log; shorten exit_force after the
   done marker; busy_timeout for experience indexing.
Estimate (unverified): B at ~1.1-1.2x A's time and ~1.4x its dollars; needs a re-run to confirm.

When slicing pays: over 335 past runs the cost per turn stays flat ($0.023-0.025) up to ~120 turns and
jumps to ~$0.041 (+65%) beyond. A's largest tasks took 58 and 49 turns, so splitting them bought
nothing. Split only work that will not fit ~100 turns, or to run developers in parallel; not by layer.
Next check proposed: B on the same five tasks without slicing (one pointer per task, no rewrite by
Chat), gates once before merge or off — separates the manager itself from this configuration.

## Manager issues seen during the run
Chat status stuck at "working" after its turn ended; "database is locked" in experience indexing;
silent gate pauses (status working, nothing in app.log); get_session "not found" for configured but
not started sessions; untrusted new project folder ignores settings.local.json without a warning;
cm-mcp of a running client gets 401 after a manager restart. Hung process after the done marker was
killed by the manager itself (exit_force) — worked as designed.
