# Architecture Reference

Tech stack, the package/component map, the Wails-bound method table, the SQLite schema and the file-logging setup. See [CLAUDE.md](../CLAUDE.md) for the project index.

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
│   │   │                            #   rate-limit fallback restart, auth error (403) handling,
│   │   │                            #   context-triggered handoff restart (checkContextRestart, LN-15)
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
│   │   ├── transcript.go            # LN-01: Trajectory — backend-agnostic parse of Claude CLI's
│   │                                #   own JSONL transcripts (~/.claude/projects/<slug>/<id>.jsonl,
│   │                                #   CM_TRANSCRIPTS_DIR override); tool_use/tool_result stitching,
│   │                                #   incremental ReadFrom(path, offset). A second backend (LN-17,
│   │                                #   markdown logs) produces the same Trajectory type.
│   │   ├── signature.go             # LN-02: Signature — normalizes a Step's InputText into an
│   │                                #   aggregable sig (Bash argv rules, path/pattern rules) +
│   │                                #   the verbatim arg; feeds action_signatures/ingest_state
│   │                                #   (store/migrations.go) via Store.InsertActions/TopSignatures.
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
│   │   ├── attribution.go           # LN-12: EstimateTokens (chars/4) + BuildAttributionReport —
│   │   │                            #   per-signature/per-tool estimated-token cut of
│   │   │                            #   action_signatures.result_chars, most expensive first;
│   │   │                            #   the "Cost by tool" tab.
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
| `StartProject(project)` | Start all sessions in a project — in cache-affinity order when `experience_tracking` + a store are on (see "Cache-Affinity Launch Order" LN-14), else plain config order |
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
| `GetTopActions(project, days)` | Aggregated tool-call signatures for the "Actions" tab (LEARN-TASKS.md LN-03) |
| `GetActionSamples(project, sig, limit)` | Concrete example rows for one signature — the "Actions" tab's click-through |
| `GetDurationProfile(project)` | Median/p90/max/fail-rate duration profile per signature — the "Timing" tab (LEARN-TASKS.md LN-18) |
| `GetTokenAttribution(project, topN)` | Estimated-token attribution by signature (top-N) and by tool — the "Cost by tool" tab (LEARN-TASKS.md LN-12) |
| `GetPermissionCandidates(project, days)` | Suggested auto-allow permission rules, split into safe/needs-review — the "Permissions" tab (LEARN-TASKS.md LN-04) |
| `AddPermissionRule(project, session, tool, pattern, decision)` | Append a `PermissionRule` to one session's config — the Permissions tab's "Add rule" button |
| `GetSkillCandidates(project)` | Mine a project's recent `action_signatures` into ranked skill candidates — the Skills tab's "Candidates" list, and the only source of an `experience.SkillCandidate` to pass to `DistillSkill` below (LEARN-TASKS.md LN-08) |
| `DistillSkill(project, candidate, gates, model, minScore)` | Distill one LN-08 skill candidate into a draft `SKILL.md`, persisted to the `skills` table (status=draft); streams `skill:progress`; `minScore <= 0` resolves to a relative threshold over the project's current candidates rather than a fixed score (LEARN-TASKS.md LN-23); returns `analysis.ErrBelowThreshold`, naming the score and threshold, when the candidate doesn't clear it (LN-09) |
| `GetSkills(project)` | List every skill row (draft/approved/archived) for a project — the "Skills" tab (LEARN-TASKS.md LN-10) |
| `ApproveSkill(id, md, overwrite)` | Write a (possibly edited) draft's markdown to `<project>/.claude/skills/<name>/SKILL.md`, mark it approved; returns `experience.ErrSkillFileExists` when the file is already there and `overwrite` is false |
| `ArchiveSkill(id)` | Mark a skill row archived — never touches any file already written into the project |
| `GetSkillQuality(project)` | Before/after-approval effect (median tokens/turns/completed-rate) per approved skill, plus a "protuhla" (stale) suggestion — the Skills tab's effect table (LEARN-TASKS.md LN-11) |
| `ImportProjectLogs(project, dir)` | Bulk-import a directory of saved CLI logs into `action_signatures` (empty `dir` = the project's own `.claude-manager/logs/`); streams `experience:import` progress — the "Import logs" button (LEARN-TASKS.md LN-20) |
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


## SQLite Tables

- `session_runs` — completed runs with cost/tokens/duration/model/**effort** (LN-13)
- `session_logs` — log entries per run (batch insert)
- `daily_metrics` — aggregated cost/tokens per day per project (input, output, **cache read, cache creation**)
- `task_plans` — pre-flight analysis plans
- `plan_subtasks` — subtasks within plans
- `mixed_briefs` — generated mixed-programming briefs (MP-06)
- `action_signatures` — normalized tool-call signatures mined from CLI transcripts, per run (LN-02); `dur_sec` is the tool_use→tool_result gap, feeding the duration profile (LN-18)
- `ingest_state` — per-CLI-session transcript byte offset, so re-indexing never re-inserts rows (LN-02)
- `imported_logfiles` — bulk-import dedup for `IngestDir`, keyed by (project, name, size, mtime) (LN-17)
- `permission_events` — one row per resolved permission_request (auto-decided or human), source for the Permissions tab's rule suggestions (LN-04)
- `skills` — one row per distilled procedure (draft/approved/archived), source_json holds the candidate signatures LN-11 checks against later runs (LN-09/10/11)


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

