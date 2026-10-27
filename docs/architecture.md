# Architecture Reference

Tech stack, the package/component map, the Wails-bound method table, the SQLite schema and the file-logging setup. See [CLAUDE.md](../CLAUDE.md) for the project index.

## Tech Stack

- **Backend:** Go 1.25+ (`go.mod`), Wails v2.16 (desktop shell)
- **Frontend:** Svelte + TypeScript + Tailwind CSS
- **Config:** TOML (`~/.claude-manager/config.toml`)
- **Storage:** SQLite (session history, metrics, logs)
- **IPC:** Wails bindings (Go functions callable from JS as async, events via `runtime.EventsEmit`)


## Architecture

```
claude-manager/
├── main.go                          # Wails bootstrap; `claude-manager hook cmdguard` runs the PreToolUse hook and exits
├── app.go                           # App struct, Wails lifecycle, 40 exported methods
├── internal/
│   ├── cmdguard/                    # PreToolUse hook installed in Claude sessions (command_guard): fix.go rewrites
│   │                                #   Bash commands that cannot work (cd into a dir only under the project root,
│   │                                #   Windows paths bash mangles); hook.go reads the payload, logs to cmdguard.log;
│   │                                #   protect.go denies edits of the manager's own .claude-manager/config*.toml
│   ├── envcheck/                    # Finds claude/git/Git Bash/Python/gh/go/node/wails and their versions, and the
│   │                                #   install command for this OS + package manager (Settings → Environment);
│   │                                #   NewestPython picks the newest off-PATH Windows Python (also env card)
│   ├── guardaudit/                  # Replays cmdguard over saved transcripts/run logs: per-rule rewrites (on a
│   │                                #   working command = unsafe rule) + clusters of failures no rule covers
│   ├── config/
│   │   ├── types.go                 # AppConfig, GlobalSettings, OptimizationSettings,
│   │   │                            #   ProjectConfig, SessionConfig, PermissionRule,
│   │   │                            #   WorkerConfig (mixed programming, MIXED-TASKS.md)
│   │   └── config.go                # Load/save TOML, defaults, validation
│   ├── mirror/
│   │   └── mirror.go                # Local copy of mirrored projects' settings + saved logs in
│   │                                #   <checkout>/projects/<name>/, own git repo, no remote
│   ├── logger/
│   │   └── logger.go                # Global slog logger (logger.L); Init() opens app.log
│   ├── sesstoken/
│   │   └── sesstoken.go             # Per-session control-plane tokens (CM_CONTROL_TOKEN, MCP-19)
│   ├── session/
│   │   ├── manager.go               # SessionManager: Start/Stop/Resume/Override/SendMessage/GetAll
│   │   │                            #   + ClearSessionState/GetSessionState (crash recovery)
│   │   ├── queuewatch.go            # 2 s poller of every task_source queue file + files its pointers name (Stat only); emits session:queue_changed (QV-02)
│   │   ├── workdir.go               # prepareWorkDir (CLI cwd = pool slot), start-stage narration (startWatch), slot-conflict guard helper
│   │   ├── session.go               # Session goroutine: bidirectional streaming, CLI args builder,
│   │   │                            #   task source check (hasTasks), crash recovery (--resume),
│   │   │                            #   rate-limit fallback restart, auth error (403) handling,
│   │   │                            #   context-triggered handoff restart (checkContextRestart, LN-15)
│   │   ├── state.go                 # StateStore: persist session_id to ~/.claude-manager/state/
│   │   │                            #   for crash recovery; atomic write (tmp → rename)
│   │   ├── env.go                   # CLAUDE_MANAGER_PID/_EXE in every session's environment, so
│   │   │                            #   a session can recognise the manager hosting it
│   │   │                            #   (.claude/hooks/guard-self-kill.py)
│   │   ├── parser.go                # Parse stream-json: assistant, tool_use, result,
│   │   │                            #   permission_request, rate_limit_event, init,
│   │   │                            #   TodoWrite todos (current task + progress);
│   │   │                            #   see docs/runtimes.md for the full Claude<->Hermes
│   │   │                            #   ParsedEvent mapping (RUNTIME-TASKS.md RT-01)
│   │   ├── tasknotify.go            # CLI <task-notification> user turn → readable system entry
│   │   ├── input.go                 # Write to stdin: user_message, permission_response
│   │   ├── hermes_parser.go         # Hermes CLI stream-json → the same ParsedEvent (HERMES-TASKS.md)
│   │   ├── hermes_runtime.go        # runtime = "hermes": `hermes chat` per turn, --resume by id
│   │   ├── runtime.go               # RT-05: Runtime/Parser interfaces — the seam between the
│   │   │                            #   shared machinery and a specific CLI backend
│   │   ├── runtime_claude.go        # RT-05: claudeRuntime — wraps buildCLIArgs/ParseLine
│   │   ├── runtime_hermes.go        # RT-05: hermesRuntime — wraps buildHermesArgs/hermesStream
│   │   ├── task_outcome.go          # Hermes turn → completed/slice/unfinished; 429 behind a 401 via agent.log
│   │   ├── handoff_budget.go        # LN-28: mid-turn "context budget reached" note (handoff_context_tokens)
│   │   ├── loop_guard.go            # loop_detection: feeds tool calls to optimization.LoopDetector; warn / send_hint / restart + alert:loop
│   │   ├── loop_memory.go           # WS-04: SetLoopMemory wiring (factory + resolve) for [optimization] loop_memory
│   │   ├── strict_gates.go          # WS-05: gate_rounds — gates after a closed task, red output sent back as a resumed turn
│   │   ├── launch_tuning.go         # [optimization] → launch flags: --exclude-dynamic-system-prompt-sections,
│   │   │                            #   adaptive --max-turns (1.6 × longest recent run, ≥ max_turns_default)
│   │   ├── permission_timeout.go    # permission_timeout/_action auto-answer; notify/sound flags on session:permission
│   │   ├── budget_alerts.go         # daily/weekly_budget_alert → alert:budget once per day/week (after finishRun)
│   │   ├── preflight_gate.go        # PreflightMode + SessionPreflight: pre-flight on a UI start (PLAN.md §17.6)
│   │   ├── launch_env.go            # utf8Env: PYTHONUTF8/PYTHONIOENCODING for Claude processes (python_utf8)
│   │   ├── envcard.go               # envCard: OS/shell/Python/project-root card appended to the system prompt (env_card)
│   │   ├── managercard.go           # managerCard: project sessions + task protocol appended to the system prompt (manager_card)
│   │   ├── interrupt.go             # Session.Interrupt: control_request interrupt aborts the turn in flight, session waits for the next message
│   │   ├── slashcmds.go             # "/" autocomplete catalog per runtime; Hermes `/skill` → `-s`
│   │   └── ratelimit.go             # Rate limit detection, retry logic, timers
│   ├── worktree/
│   │   └── slot.go                  # Session work folder: SlotName (worktree_name / p<N>-work / <name>-work), EnsureSlot (pool slot
│   │                                #   under .claude/worktrees/, repairs an interrupted checkout, never deletes)
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
│   │   ├── journal.go               # LN-06: GenerateJournalEntry — haiku distillation of one
│   │   │                            #   completed run (task pointer, files changed, result text)
│   │   │                            #   into {done, surprises, avoid} for the project journal
│   │   ├── handoff.go               # LN-15: GenerateHandoff — haiku distillation of an
│   │   │                            #   in-flight task interrupted by a context restart
│   │   │                            #   (task pointer, TodoWrite state, recent transcript
│   │   │                            #   steps) into {done, remaining, decisions, files_changed}
│   │   ├── skill.go                 # LN-09: DistillSkill — sonnet distillation of one LN-08
│   │   │                            #   SkillCandidate (+ related failures, gates) into a
│   │   │                            #   SkillDraft; RenderSkillMarkdown renders the SKILL.md body
│   │   ├── skillreview.go           # LN-25: ReviewSkill — second-model verdict on an autopilot draft
│   │   ├── failurerule.go           # LN-38: DraftFailureRule — one-line CLAUDE.md rule for a recurring error (claude -p, prompt on stdin)
│   │   └── schema.go                # JSON Schema for analyst structured output + brief output
│   ├── optimization/
│   │   ├── routing.go               # ModelRouter: auto model routing by task complexity, now
│   │   │                            #   overridable by measured outcome (LEARN-TASKS.md LN-13)
│   │   ├── outcomes.go              # LN-13: OutcomeStats/OutcomeProvider + evaluateOutcome — the
│   │   │                            #   tier-ordering/min-runs/cost-threshold rules that decide
│   │   │                            #   whether a project's own history justifies downgrading
│   │   │                            #   Route()'s recommendation to a cheaper model
│   │   ├── context.go               # Context utilization monitor, auto-restart at threshold;
│   │   │                            #   wired into internal/session's run loop by LN-15
│   │   ├── cache.go                 # Cache efficiency tracking, warming delay between session starts
│   │   ├── loop.go                  # Loop detection (repeated tool calls, ring buffer;
│   │   │                            #   WS-04: per-run tally for repeats outside the window)
│   │   ├── loopmemory.go            # WS-04: LoopMemory interface + PastLoopHint text
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
│   │   ├── transcript.go            # LN-01: Trajectory — backend-agnostic parse of Claude CLI's
│   │                                #   own JSONL transcripts (~/.claude/projects/<slug>/<id>.jsonl,
│   │                                #   CM_TRANSCRIPTS_DIR override); tool_use/tool_result stitching,
│   │                                #   incremental ReadFrom(path, offset). A second backend (LN-17,
│   │                                #   markdown logs) produces the same Trajectory type.
│   │   ├── signature.go             # LN-02: Signature — normalizes a Step's InputText into an
│   │                                #   aggregable sig (Bash argv rules, path/pattern rules) +
│   │                                #   the verbatim arg; feeds action_signatures/ingest_state
│   │                                #   (store/migrations.go) via Store.InsertActions/TopSignatures.
│   │   ├── backfill.go              # LN-36: BackfillErrorText — fills err_text of failed rows indexed
│   │                                #   before LN-35 from their transcripts (once per start, background).
│   │   ├── mdlog.go                 # LN-17: ParseLogFile — the markdown-backend twin of
│   │                                #   transcript.go's Read/ReadFrom, parsing store.RenderExport's
│   │                                #   "md" format (auto-saved per-run logs) into a Trajectory via
│   │                                #   a FIFO tool_use/tool_result binding queue.
│   │   ├── indexer.go               # LN-17: IngestDir — recursive bulk import of a log directory
│   │                                #   (.jsonl via transcript.go, .md via mdlog.go) into
│   │                                #   action_signatures, deduped by store.IsLogFileImported
│   │                                #   (name+size+mtime, since one file = one closed run).
│   │                                #   LN-03: IngestRun — the live twin, called from
│   │                                #   SessionManager.finishRun per completed run via the
│   │                                #   ingest_state offset (LN-02), wired in app.go and gated on
│   │                                #   [optimization] experience_tracking.
│   │   ├── allowlist.go             # LN-04: ClassifyPermission/Candidates — a hard read-only
│   │   │                            #   whitelist over permission_events (store/migrations.go),
│   │   │                            #   feeding the Permissions tab's rule suggestions.
│   │   ├── primer.go                # LN-05: BuildPrimer — the "Project state (auto-generated)"
│   │   │                            #   block prepended to a fresh run's prompt (task, git state,
│   │   │                            #   files the previous run touched, gate commands), capped
│   │   │                            #   at MaxPrimerChars; wired via session.PrimerFunc to avoid
│   │   │                            #   the same import cycle as ActionIndexFunc (LN-03).
│   │   ├── journal.go               # LN-06: AppendEntry/LastEntries — episodic memory in
│   │   │                            #   <project>/.claude-manager/journal.md, one markdown
│   │   │                            #   section per completed task, rotated into
│   │   │                            #   journal-archive-YYYY-MM.md past MaxJournalEntries;
│   │   │                            #   wired via session.JournalWriteFunc (same cycle reason).
│   │   ├── failures.go              # LN-07: FailureCluster/FixPair — mines "failed → the next
│   │   │                            #   attempt fixed it" pairs from worker.MixedTask gate/round
│   │   │                            #   transitions and action_signatures error→success retries,
│   │   │                            #   normalizes the failure text (ErrorKey) into a cluster key;
│   │   │                            #   feeds LN-08/09 and the read-only "Failures" tab.
│   │   ├── failurerules.go          # LN-37: ErrorClusters (all is_error rows by ErrorKey, noise skipped),
│   │   │                            #   FailureRuleCandidates, AppendClaudeMdRule, FailureRuleEffect.
│   │   ├── candidate.go             # LN-08: MineCandidates — n-grams (1..4) of signatures
│   │   │                            #   recurring across a run-share of a project's runs,
│   │   │                            #   scored distinctRuns*log(1+rediscoveryChars)*outcomeWeight,
│   │   │                            #   loop-flagged (ContextLossSuspect) rather than inflated,
│   │   │                            #   nested-n-gram deduped; feeds LN-09's distiller.
│   │   ├── skillfiles.go            # LN-10: WriteSkillFile — atomic, never-gitignored write
│   │   │                            #   of an approved skill to <project>/.claude/skills/
│   │   │                            #   <name>/SKILL.md; ValidSkillName + a confinedPath-style
│   │   │                            #   check reject any unsafe name before it touches disk.
│   │   ├── skillquality.go          # LN-11: BuildSkillQualityReport — median input-tokens/
│   │   │                            #   num-turns/completed-rate before vs. after a skill's
│   │   │                            #   approved_at, restricted to comparable runs (a step
│   │   │                            #   whose sig is in source_json); flags "protuhla"
│   │   │                            #   (unused in the last 20 runs, or no token drop after
│   │   │                            #   >=5 post-approval runs) as a suggestion, never an
│   │   │                            #   auto-archive.
│   │   ├── skillusage.go            # LN-24: BuildSkillUsage — Skill/skill_view loads per skill
│   │   ├── skillpilot.go            # LN-25/26: SkillPilot — distill → review → trial → keep/switch off
│   │   ├── attribution.go           # LN-12: EstimateTokens (chars/4) + BuildAttributionReport —
│   │   │                            #   per-signature/per-tool estimated-token cut of
│   │   │                            #   action_signatures.result_chars, most expensive first;
│   │   │                            #   the "Cost by tool" tab.
│   │   ├── waste.go                 # WS-01: AnalyzeWaste/BuildWasteReport — successful but
│   │   │                            #   useless calls per run (repeat_read, empty_search,
│   │   │                            #   oversized_read, rerun_cmd), priced like attribution.go;
│   │   │                            #   the "Waste" tab. Read only.
│   │   ├── hookguard.go / .py       # WS-03: the generated PreToolUse/PostToolUse hook —
│   │   │                            #   RenderHookScript (embedded hookguard.py + rules JSON),
│   │   │                            #   PlanHook/ApplyHookPlan (script + settings.json
│   │   │                            #   registration as a diff, written only on approval).
│   │   ├── hookrules.go             # WS-03: HookRuleCandidates (waste kinds + commands that
│   │   │                            #   kept failing), HookRuleEffect, EvaluateHookTrials
│   │   │                            #   (keep, or take out when runs got longer/more erroneous).
│   │   ├── loopmemory.go            # WS-04: LoopMemory (loop_events record + hint from an earlier
│   │   │                            #   resolved loop), ResolveRunLoops (resolved_by from the
│   │   │                            #   indexed run's action_signatures)
│   │   ├── duration.go              # LN-18: DurationProfile — median/p90/max/fail-rate per
│   │   │                            #   signature from action_signatures.dur_sec (n>=10 only);
│   │   │                            #   durationSection renders the primer's "Command timing"
│   │   │                            #   block (LN-05) and the sleep anti-pattern line.
│   │   ├── affinity.go              # LN-14: OrderByCacheAffinity — cache-friendly session
│   │   │                            #   launch order (group by model, then by descending
│   │   │                            #   Read/Edit/Write file overlap with the previous pick);
│   │   │                            #   LastRunFiles reads the file list from
│   │   │                            #   action_signatures. Wired from app.go:StartProject.
│   │   ├── handoff.go               # LN-15: BuildHandoffInput — reads the tail of an
│   │   │                            #   interrupted CLI session's own transcript (LN-01,
│   │   │                            #   best-effort) + the live TodoWrite state into
│   │   │                            #   analysis.HandoffInput; RenderHandoffPrompt formats
│   │   │                            #   the distilled result into the first user turn of the
│   │   │                            #   fresh, non-resumed process. Wired via
│   │   │                            #   session.HandoffFunc (same cycle reason as PrimerFunc).
│   │   └── regression.go            # LN-16: DetectRegression — a session's own trailing
│   │                                #   median cost/input-tokens (last RegressionWindow runs)
│   │                                #   vs. the just-finished run, 2x = regression; Hint names
│   │                                #   what grew in the manager's own prompt overhead
│   │                                #   (primer/skill descriptions/journal tail) since the last
│   │                                #   measurement (OverheadTracker, in-memory only). Wired via
│   │                                #   session.RegressionFunc (same cycle reason as PrimerFunc).
│   ├── store/
│   │   ├── store.go                 # SQLite: init, CRUD for runs/logs/plans/metrics/briefs/skills
│   │   ├── migrations.go            # CREATE TABLE statements, indexes
│   │   ├── loopevents.go            # WS-04: loop_events (detected loops, resolved_by)
│   │   └── logfiles.go              # Auto-saved per-run log files in <project>/.claude-manager/logs/,
│   │                                #   shared RenderExport (md/json/txt) for auto-save + manual export
│   └── hooks/
│       └── hooks.go                 # Pre/post task hooks (shell commands)
├── frontend/src/
│   ├── App.svelte                   # Root layout: sidebar + main panel + status bar + modals
│   ├── stores/
│   │   ├── sessions.ts              # Session state, Wails event subscriptions
│   │   ├── projects.ts              # Projects state
│   │   ├── queues.ts                # Per-session queue snapshot (one GetSessionRoadmap call feeds
│   │   │                            #   the mini-bar, Queue tab and board); syncQueue() re-reads on
│   │   │                            #   task_source_description / tasks_done change
│   │   ├── theme.ts                 # Dark/light theme toggle, localStorage persistence
│   │   ├── logSearch.ts             # Log filter store, Ctrl+F focus
│   │   ├── logView.ts               # Log rendering mode: markdown vs raw + layout: feed vs
│   │   │                            #   classic (localStorage; UI-02)
│   │   ├── workers.ts               # Mixed programming: worker:* events, per-project tasks/
│   │   │                            #   quality, register+dispatch+cancel actions (MP-08)
│   │   └── experience.ts            # LN-03: fetchTopActions/fetchActionSamples wrappers +
│   │                                #   SignatureStat/ActionRow row types for ExperiencePanel;
│   │                                #   LN-04: fetchPermissionCandidates/addPermissionRule +
│   │                                #   PermissionCandidate/CandidateSet types; LN-10:
│   │                                #   fetchSkills/approveSkill/archiveSkill + Skill/
│   │                                #   SkillDraft types for SkillReview.svelte; LN-11:
│   │                                #   fetchSkillQuality + SkillEffect/SkillStats types
│   │                                #   for SkillReview.svelte's effect table
│   ├── components/
│   │   ├── Sidebar.svelte           # Project tree, session indicators, start/stop/delete,
│   │   │                            #   auto-routing trigger, resizable via drag handle
│   │   ├── ModelPicker.svelte       # Pre-start model selector: recommendation + override dropdowns
│   │   ├── ResumePrompt.svelte      # Unfinished previous run: continue (--resume) / start fresh
│   │   ├── LogStream.svelte         # Real-time log with color coding, autoscroll, search filter,
│   │   │                            #   "Markdown" checkbox, "Лента" (feed) layout toggle: groups
│   │   │                            #   tool calls via lib/logGroups.ts (UI-02); feed is the default
│   │   │                            #   since UI-05, classic stays one click away and unchanged
│   │   ├── LiveStatus.svelte        # Sticky live line under the feed (spinner, verb/tool phrase, time) from
│   │   │                            #   session.activity; lib/liveStatus.ts holds text + ticking clock
│   │   ├── LogEntryRow.svelte       # One log row (collapse, markdown render/raw, M override);
│   │   │                            #   shared by LogStream's classic view and expanded feed groups
│   │   ├── TaskPanel.svelte         # Right of the log, two tabs — "Queue · N": QueuePanel (only
│   │   │                            #   when the session has a queue, then the default);
│   │   │                            #   "Roadmap": RoadmapTree
│   │   ├── QueuePanel.svelte        # Queue tab: current-row card (live TodoWrite step) + numbered
│   │   │                            #   "Next" list with size chips and "waits for" dependencies
│   │   ├── QueueMiniBar.svelte      # Sidebar row footer: done/current/queued segments + counts,
│   │   │                            #   click unfolds what is still ahead (no session switch)
│   │   ├── QueueBoard.svelte        # Read-only board replacing the log (SessionView "Log | Board"
│   │   │                            #   toggle): Queue / In progress / Blocked (if any) / Done
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
│   │   ├── ExperiencePanel.svelte   # LN-03: "Experience" modal, "Actions" tab — sortable
│   │   │                            #   signature table, click a row to load sample calls;
│   │   │                            #   LN-04: "Permissions" tab — safe/needs-review suggestion
│   │   │                            #   tables, per-row session picker + "Add rule" button;
│   │   │                            #   LN-10: "Skills" tab — mounts SkillReview.svelte
│   │   ├── SkillReview.svelte       # LN-10: review/edit/accept/archive one project's
│   │   │                            #   distilled skills — markdown Edit/Preview split
│   │   │                            #   (PlanReview.svelte style), overwrite-conflict banner;
│   │   │                            #   LN-11: before/after-approval effect table above the
│   │   │                            #   list, per-row "OK"/"Suggest archiving"/"Not enough
│   │   │                            #   data" verdict
│   │   ├── ContextMenu.svelte       # App-wide right-click menu (mounted once in App.svelte):
│   │   │                            #   cut/copy/paste/select all in fields, copy selection,
│   │   │                            #   open/copy link, copy code block
│   │   └── RateLimitBanner.svelte   # Rate limit countdown banner
│   └── lib/
│       ├── formatters.ts            # Log formatting, time, cost, tokens, percent;
│       │                            #   log colours are light/dark class pairs
│       ├── queue.ts                 # Pure queue model from a RoadmapView: in_queue rows (current
│       │                            #   first), waitsFor from depends_on, recent done, segments
│       ├── contextMenu.ts           # ContextMenu.svelte logic: target → menu items, clipboard
│       │                            #   (navigator.clipboard with Wails runtime fallback), BrowserOpenURL
│       ├── markdown.ts              # Dependency-free markdown → safe HTML for the log
│       │                            #   (hasMarkdown/renderMarkdown, escapes everything)
│       └── models.ts                # Model catalog: MODELS/EFFORTS + normalizeModel/modelLabel
│                                    #   (one spelling per model in every dropdown)
├── cmd/                             # Test/control harness binaries (see PLAN.md §21)
│   ├── fakeclaude/                  # Scripted Claude CLI double (deterministic stream-json)
│   ├── fakeworker/                  # Scripted OpenAI-compatible worker double (SSE, MP-07)
│   ├── fakehermes/                  # Scripted `hermes chat --format stream-json` double
│   ├── guardaudit/                  # CLI front end of internal/guardaudit (`go run ./cmd/guardaudit`)
│   ├── cm-mcp-eval/                 # MCP-16 opt-in (CM_MCP_EVAL=1) evaluation: real `claude -p` with only cm-mcp over a throw-away manager on fakeclaude; tasks.go (13 tasks + state checks), solutions.go (scripted agents for the offline test), claude.go (runner, stream metrics), instance.go/evalapp.go (headless manager + app)
│   └── cm-mcp/                      # MCP server on the official go-sdk: main.go (control-plane proxy), server.go (protocol layer), resources.go (cm:// resources), prompts.go (scenario prompts), conformance_test.go + e2e_test.go (stdio subprocess + full control through MCP), scripts/mcp-inspect.sh (manual Inspector run); docs/embed.go embeds mcp.md as cm://guide
├── internal/
│   ├── testkit/                     # Scenario loaders + fakeclaude<->parser conformance,
│   │                                #   fakeworker handler (MP-07)
│   └── control/                     # Headless control-plane: Emitter, RPC (incl. mixed), WS,
│                                    #   wait/wait-for-worker, MCP tools, e2e runner,
│                                    #   access.go (levels, self-protection, audit log, session-token caller),
│                                    #   rpc_projects.go + cfgops.go (point config edits, dry_run diff, version)
│                                    #   rpc_logs.go + mcptools_logs.go (log search/export, cost, experience tables)
│                                    #   rpc_planning.go + mcptools_planning.go (roadmap RPCs; preflight, roadmap and mixed tools)
│                                    #   rpc_skills.go + mcptools_skills.go (experience layer: skill candidates, distil, approve, autopilot)
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


## Wails Bindings (app.go)

> `cm-mcp` + the control-plane (`internal/control`) are the public interface to these bindings: every method below has an MCP tool or a reasoned `parityExcluded` entry (`app_parity_test.go`; `parityPending` is empty). Guide: [mcp.md](./mcp.md).

All exported methods become async JS functions via auto-generated bindings in `frontend/wailsjs/`.

| Method | Description |
|---|---|
| `GetConfig()` | Full AppConfig including Optimization settings |
| `CheckEnvironment()` | `envcheck.Report`: required/recommended/build tools found on this machine, their versions, and an install command for each missing one (winget/brew/apt/dnf/pacman/zypper/apk) — Settings → Global → Environment |
| `UpdateConfig(cfg)` | Persist config to TOML, reload session manager |
| `GetProjects()` | Project list shortcut |
| `GetAutoModelRouting()` | Whether auto_model_routing is enabled |
| `GetModelRecommendation(project, name)` | Run preflight analysis, return ModelRecommendation or null |
| `RunPreflight(project, task)` | Run analyst on an ad-hoc task, persist and return draft TaskPlan |
| `SessionPreflight(project, name)` | Pre-flight gate for a UI start: runs the analyst on the session's prompt when `preflight_analysis` + its `preflight` mode call for it; returns the plan to review, or nil to start at once |
| `ApprovePlan(plan)` | Persist an (edited) plan as approved; returns plan with store ID |
| `ExecutePlan(planID)` | Execute persisted plan (one-shot CLI per subtask, context handoff) |
| `GetPlan(planID)` | Load persisted plan with subtasks (poll during execution) |
| `GenerateRoadmap(project, idea, model)` | Decompose a project idea into a draft roadmap plan (Opus by default); streams `plan:roadmap_progress` while running |
| `GetLatestDraftRoadmap(project)` | Recover the most recent unapproved roadmap plan for a project — e.g. after a `GenerateRoadmap` response never reached the frontend — without re-running the analyst |
| `ApproveRoadmap(planID, overwrite)` | Write ROADMAP.md/STATUS-P1.md into the project + bootstrap the "P1" session |
| `GetSessionRoadmap(project, session)` | Roadmap tree for the TaskPanel "Roadmap" tab (tasks + done/current/pending, one group node per pointed-at roadmap file); nil when the session has no task source or the project has no roadmap |
| `GetRoadmapTaskDetail(project, relPath)` | Body of one `tasks/NN-*.md` file (path confined to the project folder) |
| `GetRoadmapRowDetail(project, roadmapFile, line)` | Long-form text of one roadmap row (its `note` column, else the raw row) for roadmaps that keep descriptions inline |
| `HasSessionProtocol(project)` | Whether the project folder has `scripts/worktree-pool.sh` or `docs/git-workflow.md`; drives the banner under Settings → "Use worktree" |
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
| `SetSessionRuntime(id, runtime)` | Switch a stopped session between `claude` and `hermes` CLI (sidebar picker); persisted as the session's `runtime`. Refused while the session runs |
| `StopSession(id, soft)` | Stop (soft=true finishes current task first) |
| `InterruptSession(id)` | Abort the current action (turn) but keep the session alive, waiting for the next message |
| `RestartSession(id)` | Hard stop + restart |
| `RestartSessionMode(id, fresh)` | Hard stop + restart; `fresh` clears saved state (new conversation), else resumes it. Behind the Restart dialog |
| `ResumeSession(id)` | Resume from saved CLI session ID |
| `StartProject(project)` | Start all sessions in a project — in cache-affinity order when `experience_tracking` + a store are on (see "Cache-Affinity Launch Order" LN-14), else plain config order |
| `StopProject(project)` | Stop all sessions in a project |
| `StopAll()` | Stop every session |
| `SendMessage(id, message)` | Write user_message to stdin |
| `ListSlashCommands(id)` | `[]session.SlashCommand{name, description, kind}` for the message box's "/" autocomplete — Claude Code commands+skills for a Claude session, Hermes skills for a Hermes one (`internal/session/slashcmds.go`) |
| `SendMessageWithImages(id, message, images)` | Write a user turn with pasted image attachments (`session.ImageAttachment{media_type, data_base64}`) as an Anthropic content-block array |
| `RespondPermission(id, requestID, decision)` | Write permission response to stdin |
| `GetPendingPermissions()` | All sessions with pending permission requests |
| `AnswerQuestion(id, questionID, answer)` | Resolve a pending ask-user question (genuine decision), continuing the same conversation |
| `GetPendingQuestions()` | All sessions currently blocked on a genuine-decision ask-user question |
| `GetAllSessions()` | Snapshot of all session states |
| `GetSessionLog(id, offset, limit)` | Paginated log entries |
| `GetPreviousRunLog(id, limit)` | Saved log tail of the session's last finished run (the run in progress skipped), as live-feed entries — the feed shows it on top after an app restart |
| `GetSessionMetrics(id)` | Token/cost metrics for one session |
| `GetHistory(project, limit)` | Past session runs from SQLite |
| `GetDailyCost(date)` | Cost aggregate for a date |
| `GetDailyTokens(date)` | Token volume for a date (input/output/cache split + total + `duration_ms`, the wall-clock time of the day's runs, WS-02), from the same `daily_metrics` rows as `GetDailyCost` |
| `GetProjectCost(project, days)` | Cost aggregate for a project over N days |
| `GetProjectTokens(project, days)` | Token volume for a project over N days — the token twin of `GetProjectCost` |
| `GetTopActions(project, days)` | Aggregated tool-call signatures for the "Actions" tab (LEARN-TASKS.md LN-03) |
| `GetActionSamples(project, sig, limit)` | Concrete example rows for one signature — the "Actions" tab's click-through |
| `GetDurationProfile(project)` | Median/p90/max/fail-rate duration profile per signature — the "Timing" tab (LEARN-TASKS.md LN-18) |
| `GetTokenAttribution(project, topN)` | Estimated-token attribution by signature (top-N) and by tool, priced with re-reads (`LifetimeTokens`), plus cost by session length (`ByLength`) — the "Cost by tool" tab (LEARN-TASKS.md LN-12, LN-29) |
| `GetWasteReport(project)` | Successful but useless work over the last 90 days — repeated reads, empty-search streaks, whole-file reads, commands rerun without an edit — per category: calls, est. tokens, share, 5 examples, plus the share of `[settings] limit_window_tokens` the waste ate (`WindowKnown`/`WindowShare`, WS-02) — the "Waste" tab (WASTE-TASKS.md WS-01). Cut-offs: `[optimization] waste_empty_search_chars` / `waste_oversized_read_tokens` |
| `GetPermissionCandidates(project, days)` | Suggested auto-allow permission rules, split into safe/needs-review — the "Permissions" tab (LEARN-TASKS.md LN-04) |
| `AddPermissionRule(project, session, tool, pattern, decision)` | Append a `PermissionRule` to one session's config — the Permissions tab's "Add rule" button |
| `GetSkillCandidates(project)` | Mine a project's recent `action_signatures` into ranked skill candidates — the Skills tab's "Candidates" list, and the only source of an `experience.SkillCandidate` to pass to `DistillSkill` below (LEARN-TASKS.md LN-08) |
| `DistillSkill(project, candidate, gates, model, minScore)` | Distill one LN-08 skill candidate into a draft `SKILL.md`, persisted to the `skills` table (status=draft); streams `skill:progress`; `minScore <= 0` resolves to a relative threshold over the project's current candidates rather than a fixed score (LEARN-TASKS.md LN-23); returns `analysis.ErrBelowThreshold`, naming the score and threshold, when the candidate doesn't clear it (LN-09) |
| `GetSkills(project)` | List every skill row (draft/approved/archived) for a project — the "Skills" tab (LEARN-TASKS.md LN-10) |
| `ApproveSkill(id, md, overwrite)` | Write a (possibly edited) draft's markdown to `<project>/.claude/skills/<name>/SKILL.md`; status becomes `trial` when the project's autopilot is on, else `approved`; returns `experience.ErrSkillFileExists` when the file is already there and `overwrite` is false |
| `ArchiveSkill(id)` | Switch a skill off: moves its folder out of `.claude/skills/` into `<project>/.claude-manager/archived-skills/<name>-<id>/` (the CLI stops seeing it), marks the row archived (LEARN-TASKS.md LN-24) |
| `RestoreSkill(id, overwrite)` | Bring an archived skill back (the archived copy, hand edits included) or apply a rejected one anyway; starts a new trial or marks it approved (LN-24) |
| `GetSkillQuality(project)` | Before/after-approval effect (median tokens/turns/completed-rate) per applied skill, plus a stale suggestion: "unused" now means not loaded by any agent (`Skill`/`skill_view` tool calls), not signature overlap (LN-11, LN-24) |
| `GetSkillUsage(project)` | Loads, runs-with-load, runs since last load per skill, keyed by skill ID (LN-24) |
| `GetSkillAutopilot(project)` / `SetSkillAutopilot(project, enabled, dailyBudgetUSD)` | Read / persist the project's skill-autopilot switch and daily budget (private `config.local.toml`; turning it on also turns on `experience_tracking`) (LN-27) |
| `RunSkillAutopilot(project)` | One autopilot step now: decide finished trials, then distill, review and apply at most one candidate; errors if the autopilot is off (LN-25/26) |
| `GetHookRules(project)` | WS-03. The project's hook rules (newest first) with the effect of installed ones (steps and errors per run, before vs after); first decides trials that ran `HookRuleTrialRuns` runs. `Enabled=false` and no side effect unless `[optimization] hook_rules = true`. No MCP tool (see `ApplyHookRule`) |
| `ProposeHookRules(project)` | WS-03. Stores as drafts the rules the history justifies (repeat_read / empty_search / oversized_read when the Waste report found them; failure_cmd for a Bash command that failed in ≥3 runs and was fixed by another form). Rows only, no files |
| `PreviewHookRule(id)` | WS-03. The diff installing a draft would make in the project (`.claude/hooks/cm-waste-guard.py`, `.claude/settings.json`); writes nothing |
| `ApplyHookRule(id)` | WS-03. After the user saw the diff: writes the files, rule → `trial`. One trial at a time. Excluded from MCP: installing writes into the user's repository only on approval in the UI |
| `RemoveHookRule(id)` | WS-03. A draft → `dismissed`; an installed rule → `reverted`, files rewritten without it (the last rule out deletes both) |
| `GetFailureRules(project)` | A project's failure rules (newest first) with the examples each was drafted from and, for accepted ones, the effect (hits of the error in the 20 runs before vs the runs after), plus `Pending` — recurring errors without a rule yet (LN-38) |
| `DraftFailureRules(project, limit)` | Paid: one `claude -p` call per recurring error (first `limit`, ≤ 0 → 3) writes a one-line CLAUDE.md rule; stored as `draft`, or `dismissed` when CLAUDE.md already says it. Writes nothing into the project (LN-38) |
| `AcceptFailureRule(id, ruleText)` | For a draft, or an applied `trial`/`kept` rule (which then leaves the system prompt, LN-41): appends the (user-edited) rule to the "Learned from failures" section of the project's CLAUDE.md and marks it `accepted`; the only write into the repository (LN-38) |
| `DismissFailureRule(id)` | Marks a draft `dismissed`; the error is not proposed again (LN-38) |
| `RevertFailureRule(id)` | Switches an autopilot rule (`trial`/`kept`) off → `reverted`, `reason.code = reverted_manual`; it leaves the sessions' system prompt at their next start (LN-41) |
| `RestoreFailureRule(id)` | `reverted` → `kept` (`restored_manual`), no new trial (LN-41) |
| `ImportProjectLogs(project, dir)` | Bulk-import a directory of saved CLI logs into `action_signatures` (empty `dir` = the project's own `.claude-manager/logs/`); streams `experience:import` progress — the "Import logs" button (LEARN-TASKS.md LN-20) |
| `GetRateLimitStatus()` | Current rate limit info |
| `ExportLog(id, entries, format)` | Save log as MD/JSON/TXT via native dialog |
| `CleanOldLogs(days)` | Delete logs older than N days from SQLite |
| `GetProjectLogFiles(project)` | List auto-saved log files under `<project>/.claude-manager/logs/` (name/size/mod_time) |
| `ClearProjectLogs(project)` | Delete a project's saved log files + their `session_logs` SQLite rows (keeps `session_runs`) |
| `MirrorProject(project)` | "Mirror into Claude Manager": set the project's `mirror` flag and sync it now → `ProjectMirrorStatus` |
| `UnmirrorProject(project)` | Clear the `mirror` flag; the copy and its history stay |
| `SyncProjectMirrors()` | Re-sync every mirrored project now → `[]mirror.Result` |
| `GetProjectMirror(project)` | `{enabled, dir, lastSync, files, error}` for Settings |
| `ClearSessionState(project, name)` | Delete crash-recovery state file (equivalent to --new) |
| `GetSessionState(project, name)` | Return persisted state (session_id + started_at) or nil |
| `PickDirectory(title)` | Native folder picker dialog |
| `ShowMainWindow()` | Restore window from tray |
| `MinimizeToTray()` | Hide window to system tray |
| `Notify(title, body)` | Windows toast notification |


## Build & Run

```bash
wails dev                    # Dev mode (hot reload frontend)
wails build -nosyncgomod     # Production: build/bin/claude-manager.exe (~19 MB)
run-claude-manager.bat       # What the user runs: pull origin, rebuild if HEAD moved, relaunch loop
```

**Only `wails build` makes a correct exe.** Besides compiling, it runs `npm install`/`npm run build`,
regenerates the frontend bindings (`frontend/wailsjs` — gitignored, so they exist only after a Wails
step) and embeds the Windows resources from `build/windows/` (icon; manifest with per-monitor-v2 DPI
awareness and Common Controls v6; version info). A bare `go build -tags desktop,production` — which
the launcher used for a while — produced an exe with the generic Windows icon, no manifest (blurry
at display scaling above 100%) and whatever bindings were left on disk; once a bound method is added
upstream the frontend build fails and the launcher keeps the old exe. Measured 2026-10-13: 39 s for
`wails build` against 17 s for the bare build, both with the frontend steps; the launcher rebuilds
only when a pull moved `HEAD`, so the difference is paid per update, not per start. The bare build
stays in the launcher only as a warned fallback when no `wails` CLI is found.

The Wails CLI must match `github.com/wailsapp/wails/v2` in `go.mod` (v2.16.0):
`go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`. An older CLI fails on a newer Go before
compiling anything — CLI v2.12 with Go 1.27 stops at `internal error: package "os" without types`,
with or without `-skipbindings`; `GOTOOLCHAIN=go1.25.0 wails build` was the stopgap until the 2.16
bump. `-nosyncgomod` keeps Wails from editing `go.mod`, yet `wails build` still rewrites it with LF
line endings, content unchanged; the launcher puts the checkout copy back when `git diff` shows no
content change. `frontend/package.json.md5` (Wails' "did package.json change" hash) depends on the
checkout's line endings, so it is gitignored rather than re-committed after every build.


## Go Dependencies

```
github.com/wailsapp/wails/v2
github.com/BurntSushi/toml
modernc.org/sqlite            # pure-Go SQLite driver (no cgo/C toolchain needed)
github.com/google/uuid
git.sr.ht/~jackmordaunt/go-toast/v2
```

No DI frameworks, no ORMs. Standard library for everything else.


## SQLite Tables

- `session_runs` — completed runs with cost/tokens/duration/model/**effort** (LN-13)
- `session_logs` — log entries per run (batch insert)
- `daily_metrics` — aggregated cost/tokens per day per project (input, output, **cache read, cache creation**)
- `task_plans` — pre-flight analysis plans
- `plan_subtasks` — subtasks within plans
- `mixed_briefs` — generated mixed-programming briefs (MP-06)
- `action_signatures` — normalized tool-call signatures mined from CLI transcripts, per run (LN-02); `dur_sec` is the tool_use→tool_result gap, feeding the duration profile (LN-18); `err_text` is one meaningful error line of a failed call (`experience.ErrorLine`, LN-35) — the failure-cluster key
- `failure_rules` — one row per recurring error (`UNIQUE(project, error_key)`), `status` draft | accepted | dismissed | trial | kept | reverted (the last three: autopilot rules, LN-40/41), `rule_text` is the one-line rule written into the project's CLAUDE.md under "Learned from failures" (`experience.AppendClaudeMdRule`), `decided_at` anchors the before/after measurement (`experience.FailureRuleEffect`) (LN-37)
- `loop_events` — one row per detected tool-call loop (`sig` joins to `action_signatures`, `far` = found outside the ring buffer), `resolved_by` = the first different successful action after it, filled in once the run is indexed; only written with `[optimization] loop_memory` (WS-04)
- `ingest_state` — per-CLI-session transcript byte offset, so re-indexing never re-inserts rows (LN-02)
- `imported_logfiles` — bulk-import dedup for `IngestDir`, keyed by (project, name, size, mtime) (LN-17)
- `permission_events` — one row per resolved permission_request (auto-decided or human), source for the Permissions tab's rule suggestions (LN-04)
- `skills` — one row per distilled procedure (draft/rejected/trial/approved/archived), source_json holds the candidate signatures (LN-09/10/11); `origin` (manual/auto), `reason` (JSON `SkillReason`: why it is in its status), `cost_usd`, `updated_at` added by the skill autopilot (LN-24..27)


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

