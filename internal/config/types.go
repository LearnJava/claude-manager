package config

import "time"

type AppConfig struct {
	Settings     GlobalSettings       `toml:"settings"`
	Optimization OptimizationSettings `toml:"optimization"`
	Projects     []ProjectConfig      `toml:"project"`
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
	LoopThreshold           int    `toml:"loop_threshold"`             // repeats before detection (e.g. 3)
	LoopAction              string `toml:"loop_action"`                // warn | send_hint | restart
	LoopHint                string `toml:"loop_hint"`                  // text sent to stdin on send_hint
	LoopWindow              int    `toml:"loop_window"`                // ring buffer size (e.g. 20)
	LoopIgnoreReadAfterEdit bool   `toml:"loop_ignore_read_after_edit"`

	// Auto routing (PLAN.md 20.3.4)
	AutoModelRouting bool `toml:"auto_model_routing"`

	// Reporting (PLAN.md 20.6)
	ShowCostPerTurn     bool `toml:"show_cost_per_turn"`
	ShowCacheEfficiency bool `toml:"show_cache_efficiency"`
}

type GlobalSettings struct {
	ClaudePath        string `toml:"claude_path"`
	DefaultRetryDelay int    `toml:"default_retry_delay"`
	RateLimitPause    int    `toml:"rate_limit_pause"`
	LogRetentionDays  int    `toml:"log_retention_days"`
	Theme             string `toml:"theme"`

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
	DailyBudgetAlert          float64 `toml:"daily_budget_alert"`
	WeeklyBudgetAlert         float64 `toml:"weekly_budget_alert"`
	RateLimitAlertThreshold   float64 `toml:"rate_limit_alert_threshold"`

	// Cache warming: stagger session starts by this many seconds (PLAN.md 20.3.3).
	SessionStartDelay int `toml:"session_start_delay"`
}

type ProjectConfig struct {
	Name     string          `toml:"name"`
	Path     string          `toml:"path"`
	Sessions []SessionConfig `toml:"session"`
}

type SessionConfig struct {
	Name            string          `toml:"name"`
	Prompt          string          `toml:"prompt"`
	AutoRestart     bool            `toml:"auto_restart"`
	MaxTasks        int             `toml:"max_tasks"`
	StopWhenNoTasks bool            `toml:"stop_when_no_tasks"`
	TaskSource      string          `toml:"task_source"`

	// Model and performance
	Model                   string `toml:"model"`
	FallbackModel           string `toml:"fallback_model"`
	FallbackModelOnRateLimit bool   `toml:"fallback_model_on_rate_limit"`
	Effort                  string `toml:"effort"`

	// Permissions
	PermissionMode  string          `toml:"permission_mode"`
	AllowedTools    []string        `toml:"allowed_tools"`
	DisallowedTools []string        `toml:"disallowed_tools"`
	PermissionRules []PermissionRule `toml:"permission_rule"`

	// Budget
	MaxBudgetUSD float64 `toml:"max_budget_usd"`

	// Worktree
	UseWorktree bool `toml:"use_worktree"`

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
}

type PermissionRule struct {
	Tool     string `toml:"tool"`     // "Bash", "Edit", "Write", "*"
	Pattern  string `toml:"pattern"`  // glob for command or path
	Decision string `toml:"decision"` // "allow", "deny", "ask"
}

// SessionStatus represents the current state of a session.
type SessionStatus int

const (
	StatusIdle              SessionStatus = iota
	StatusStarting
	StatusAnalyzing
	StatusWorking
	StatusWaitingPermission
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
	Level     string    `json:"level"`     // "text" | "tool" | "error" | "result" | "system"
	Source    string    `json:"source"`    // "claude" | "manager"
	Message   string    `json:"message"`
	ToolName  string    `json:"tool_name"`  // for tool calls
	ToolInput string    `json:"tool_input"` // abbreviated input
}
