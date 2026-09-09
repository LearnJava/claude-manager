package config

import "time"

type AppConfig struct {
	Settings     GlobalSettings       `toml:"settings"`
	Optimization OptimizationSettings `toml:"optimization"`
	Projects     []ProjectConfig      `toml:"project"`
	Workers      []WorkerConfig       `toml:"worker"`
}

// WorkerConfig describes an external OpenAI-compatible model used for mixed
// programming (MIXED-TASKS.md): the manager sends it a self-contained brief,
// receives FIND/REPLACE patches, applies them in a worktree and runs gates.
// Quirk fields encode per-model behaviour learned from the lumen bench
// (2026-07-02); see WorkerPresets for the two validated models.
type WorkerConfig struct {
	Name    string `toml:"name"`     // unique worker id, e.g. "step37"
	BaseURL string `toml:"base_url"` // OpenAI-compatible gateway, e.g. https://api.kilo.ai/api/gateway
	Model   string `toml:"model"`    // model id, e.g. stepfun/step-3.7-flash:free
	KeyEnv  string `toml:"key_env"`  // env var holding the API key (never stored in config)
	Role    string `toml:"role"`     // hands | quality | eyes

	// Quirks. Zero values are filled in by applyDefaults.
	ReasoningEffort   string `toml:"reasoning_effort"`    // none | low | medium | high (default low)
	MaxOutputTokens   int    `toml:"max_output_tokens"`   // completion cap per call (default 16000)
	ContinuationCap   int    `toml:"continuation_cap"`    // max finish_reason=length continuations (default 3)
	ASCIIAnchorsOnly  bool   `toml:"ascii_anchors_only"`  // briefs must use pure-ASCII FIND anchors
	RequestTimeoutSec int    `toml:"request_timeout_sec"` // per-request timeout (default 180)
}

// OptimizationSettings mirrors the [optimization] TOML block from PLAN.md
// section 20.4. Zero values are filled in by applyDefaults.
type OptimizationSettings struct {
	// Context management (PLAN.md 20.3.1)
	ContextRestartThreshold float64 `toml:"context_restart_threshold"` // e.g. 0.75
	ContextWarnThreshold    float64 `toml:"context_warn_threshold"`    // e.g. 0.60
	ContextRestartMode      string  `toml:"context_restart_mode"`      // resume | fresh | ask | off

	// Cache optimization (PLAN.md 20.3.2-3)
	ExcludeDynamicSystemPrompt bool `toml:"exclude_dynamic_system_prompt"`
	SessionStartDelay          int  `toml:"session_start_delay"` // seconds between session starts

	// Turn limits (PLAN.md 20.3.5)
	MaxTurnsDefault    int  `toml:"max_turns_default"`
	MaxTurnsAutoAdjust bool `toml:"max_turns_auto_adjust"`

	// Loop detection (PLAN.md 20.3.8)
	LoopDetection           bool   `toml:"loop_detection"`
	LoopThreshold           int    `toml:"loop_threshold"` // repeats before detection (e.g. 3)
	LoopAction              string `toml:"loop_action"`    // warn | send_hint | restart
	LoopHint                string `toml:"loop_hint"`      // text sent to stdin on send_hint
	LoopWindow              int    `toml:"loop_window"`    // ring buffer size (e.g. 20)
	LoopIgnoreReadAfterEdit bool   `toml:"loop_ignore_read_after_edit"`

	// Auto routing (PLAN.md 20.3.4)
	AutoModelRouting bool `toml:"auto_model_routing"`

	// Reporting (PLAN.md 20.6)
	ShowCostPerTurn     bool `toml:"show_cost_per_turn"`
	ShowCacheEfficiency bool `toml:"show_cache_efficiency"`

	// Experience layer (LEARN-TASKS.md LN-03): mines CLI transcripts into
	// action_signatures for the "Actions" tab and downstream candidate mining.
	// Default off — with it off, finishRun never opens a transcript file.
	ExperienceTracking bool `toml:"experience_tracking"`
}

type GlobalSettings struct {
	ClaudePath        string `toml:"claude_path"`
	DefaultRetryDelay int    `toml:"default_retry_delay"`
	RateLimitPause    int    `toml:"rate_limit_pause"`
	LogRetentionDays  int    `toml:"log_retention_days"`
	Theme             string `toml:"theme"`

	// UI language: "en" or "ru". Frontend-only (no effect on prompts sent to
	// Claude, which stay English) — see "UI Localization" in CLAUDE.md.
	Language string `toml:"language"`

	// Crash recovery: persist session_id to disk so interrupted sessions can be
	// resumed with --resume after an app restart (mirrors orchestrator.py behaviour).
	CrashRecovery bool `toml:"crash_recovery"`

	// Pre-flight analysis
	PreflightAnalysis          bool    `toml:"preflight_analysis"`
	PreflightModel             string  `toml:"preflight_model"`
	PreflightMaxBudget         float64 `toml:"preflight_max_budget"`
	PreflightAutoApproveSingle bool    `toml:"preflight_auto_approve_single"`

	// Permission handling
	PermissionNotifyAfter        int    `toml:"permission_notify_after"`
	PermissionTimeout            int    `toml:"permission_timeout"`
	PermissionTimeoutAction      string `toml:"permission_timeout_action"`
	PermissionNativeNotification bool   `toml:"permission_native_notification"`
	PermissionSound              bool   `toml:"permission_sound"`

	// Budget alerts
	DailyBudgetAlert        float64 `toml:"daily_budget_alert"`
	WeeklyBudgetAlert       float64 `toml:"weekly_budget_alert"`
	RateLimitAlertThreshold float64 `toml:"rate_limit_alert_threshold"`

	// Cache warming: stagger session starts by this many seconds (PLAN.md 20.3.3).
	SessionStartDelay int `toml:"session_start_delay"`
}

// ProjectOverlay is the per-project configuration stored inside the project
// folder itself (like .git), so the context travels with the repo. It is split
// across two files under <project>/.claude-manager/ and merged onto the global
// [[project]] entry at load time (local wins over committed wins over global):
//
//	config.toml        committed  — shared, non-sensitive: sessions, gates
//	config.local.toml  gitignored — privacy opt-in + external endpoints:
//	                                 mixed_programming, [[worker]] base_urls
//
// MixedProgramming is a pointer so an absent overlay field leaves the global
// value untouched (a plain bool cannot distinguish "unset" from "false").
type ProjectOverlay struct {
	Sessions              []SessionConfig `toml:"session"`
	Gates                 []string        `toml:"gates"`
	DefaultPermissionMode string          `toml:"default_permission_mode"`
	MixedProgramming      *bool           `toml:"mixed_programming"`
	MixedMaxRounds        int             `toml:"mixed_max_rounds"`
	Workers               []WorkerConfig  `toml:"worker"`
	// Journal opts a project into the episodic-memory journal (LEARN-TASKS.md
	// LN-06): one short distilled entry per completed task, appended to
	// <project>/.claude-manager/journal.md. Private layer like
	// MixedProgramming above — not because a journal entry itself leaves the
	// machine (it doesn't, unlike a mixed-programming brief), but because
	// enabling it means an extra haiku CLI call after every completed task,
	// which a teammate cloning the repo should opt into themselves.
	Journal bool `toml:"journal"`
	// JournalCommit, when true, skips gitignoring journal.md (and any
	// journal-archive-*.md rotated out of it) so a project that wants its
	// journal reviewed and committed like any other doc can do so.
	JournalCommit bool `toml:"journal_commit"`
}

type ProjectConfig struct {
	Name     string          `toml:"name"`
	Path     string          `toml:"path"`
	Sessions []SessionConfig `toml:"session"`

	// DefaultPermissionMode seeds PermissionMode for sessions newly added to
	// this project going forward; it never touches existing sessions' saved
	// values. Empty means "use the built-in default" (bypassPermissions).
	DefaultPermissionMode string `toml:"default_permission_mode"`

	// Mixed programming (MIXED-TASKS.md). Explicit privacy opt-in: briefs and
	// verbatim code excerpts are sent to external free endpoints that log
	// requests. Off by default; enabling requires non-empty Gates.
	MixedProgramming bool `toml:"mixed_programming"`
	// Gates are blocking check commands run in the task worktree after worker
	// patches are applied (e.g. "go build ./...", "go test ./..."). A non-zero
	// exit rejects the round; the output is returned to the model as feedback.
	// Model-written tests are never trusted as the ground truth — gates are.
	Gates []string `toml:"gates"`
	// MixedMaxRounds caps feedback rounds per subtask before the task is marked
	// needs-human (default 3).
	MixedMaxRounds int `toml:"mixed_max_rounds"`

	// Journal / JournalCommit — see ProjectOverlay's fields of the same name
	// (LEARN-TASKS.md LN-06). Private-layer opt-in, default off.
	Journal       bool `toml:"journal"`
	JournalCommit bool `toml:"journal_commit"`
}

type SessionConfig struct {
	Name            string `toml:"name"`
	Prompt          string `toml:"prompt"`
	AutoRestart     bool   `toml:"auto_restart"`
	MaxTasks        int    `toml:"max_tasks"`
	StopWhenNoTasks bool   `toml:"stop_when_no_tasks"`
	TaskSource      string `toml:"task_source"`

	// Model and performance
	Model                    string `toml:"model"`
	FallbackModel            string `toml:"fallback_model"`
	FallbackModelOnRateLimit bool   `toml:"fallback_model_on_rate_limit"`
	Effort                   string `toml:"effort"`

	// Permissions
	PermissionMode  string           `toml:"permission_mode"`
	AllowedTools    []string         `toml:"allowed_tools"`
	DisallowedTools []string         `toml:"disallowed_tools"`
	PermissionRules []PermissionRule `toml:"permission_rule"`

	// Budget
	MaxBudgetUSD float64 `toml:"max_budget_usd"`

	// Worktree
	UseWorktree bool `toml:"use_worktree"`
	// WorktreeName is passed to `--worktree <name>` so the branch/worktree has a
	// stable name instead of a random one. Empty falls back to the session name.
	WorktreeName string `toml:"worktree_name"`

	// Context
	SystemPromptAppend string   `toml:"system_prompt_append"`
	AddDirs            []string `toml:"add_dirs"`

	// Pre-flight override
	Preflight string `toml:"preflight"` // always | auto | never

	// Hooks
	PreTaskHook  string `toml:"pre_task_hook"`
	PostTaskHook string `toml:"post_task_hook"`

	// Crash recovery: message sent to Claude when resuming an interrupted session.
	// Leave empty to use the built-in default recovery prompt.
	CrashRecoveryPrompt string `toml:"crash_recovery_prompt"`

	// ContextPrimer prepends an auto-generated "Project state" block (current
	// task, git state, files the previous run touched, gate commands) to the
	// initial prompt of a fresh run, so it doesn't re-discover from scratch
	// what the manager already knows (LEARN-TASKS.md LN-05). Off by default;
	// with it false the prompt is byte-identical to before this flag existed.
	ContextPrimer bool `toml:"context_primer"`

	// ContextHandoff replaces a context-triggered auto-restart's normal
	// --resume (which would re-send the very expensive prefix that caused
	// the restart) with a compact distilled recap sent as the first user
	// turn of a fresh, non-resumed process (LEARN-TASKS.md LN-15). Off by
	// default; crash recovery is unaffected — it still always uses --resume.
	ContextHandoff bool `toml:"context_handoff"`
}

type PermissionRule struct {
	Tool     string `toml:"tool"`     // "Bash", "Edit", "Write", "*"
	Pattern  string `toml:"pattern"`  // glob for command or path
	Decision string `toml:"decision"` // "allow", "deny", "ask"
}

// SessionStatus represents the current state of a session.
type SessionStatus int

const (
	StatusIdle SessionStatus = iota
	StatusStarting
	StatusAnalyzing
	StatusWorking
	StatusWaitingPermission
	StatusWaitingForUser
	StatusRateLimited
	StatusRetrying
	StatusStopping
	StatusError
)

func (s SessionStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusStarting:
		return "starting"
	case StatusAnalyzing:
		return "analyzing"
	case StatusWorking:
		return "working"
	case StatusWaitingPermission:
		return "waiting_permission"
	case StatusWaitingForUser:
		return "waiting_for_user"
	case StatusRateLimited:
		return "rate_limited"
	case StatusRetrying:
		return "retrying"
	case StatusStopping:
		return "stopping"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

type LogEntry struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`  // "text" | "tool" | "error" | "result" | "system"
	Source    string    `json:"source"` // "claude" | "manager"
	Message   string    `json:"message"`
	ToolName  string    `json:"tool_name"`  // for tool calls
	ToolInput string    `json:"tool_input"` // salient input field, full text (UI collapses long values)
}
