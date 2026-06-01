package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const defaultConfigDir = ".claude-manager"
const defaultConfigFile = "config.toml"

// DefaultConfigPath returns the default path to the config file (~/.claude-manager/config.toml).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, defaultConfigDir, defaultConfigFile)
}

// Load reads the TOML config from path. If the file does not exist, Load returns
// a config populated with defaults so the caller can start with a valid state.
func Load(path string) (*AppConfig, error) {
	cfg := defaults()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", path, err)
	}

	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save writes cfg to path in TOML format, creating directories as needed.
func Save(cfg *AppConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", filepath.Dir(path), err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("config: create %s: %w", path, err)
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	return nil
}

// defaults returns an AppConfig with all sensible zero-value defaults filled in.
func defaults() *AppConfig {
	return &AppConfig{
		Settings: GlobalSettings{
			ClaudePath:                   "claude",
			DefaultRetryDelay:            30,
			RateLimitPause:               300,
			LogRetentionDays:             30,
			Theme:                        "dark",
			CrashRecovery:                true,
			PreflightAnalysis:            true,
			PreflightModel:               "haiku",
			PreflightMaxBudget:           0,
			PreflightAutoApproveSingle:   true,
			PermissionNotifyAfter:        30,
			PermissionTimeout:            0,
			PermissionTimeoutAction:      "deny",
			PermissionNativeNotification: true,
			PermissionSound:              true,
			DailyBudgetAlert:             0,
			WeeklyBudgetAlert:            0,
			RateLimitAlertThreshold:      0.80,
			SessionStartDelay:            3,
		},
		Optimization: optimizationDefaults(),
	}
}

func optimizationDefaults() OptimizationSettings {
	return OptimizationSettings{
		ContextRestartThreshold:    0.75,
		ContextWarnThreshold:       0.60,
		ContextRestartMode:         "resume",
		ExcludeDynamicSystemPrompt: true,
		SessionStartDelay:          3,
		MaxTurnsDefault:            50,
		MaxTurnsAutoAdjust:         true,
		LoopDetection:              true,
		LoopThreshold:              3,
		LoopAction:                 "warn",
		LoopHint:                   "You seem to be repeating the same actions. Try a different approach.",
		LoopWindow:                 20,
		LoopIgnoreReadAfterEdit:    true,
		AutoModelRouting:           false,
		ShowCostPerTurn:            true,
		ShowCacheEfficiency:        true,
	}
}

// applyDefaults fills in zero-value fields in cfg with sensible defaults.
// Called after decoding so user can omit fields and still get working config.
func applyDefaults(cfg *AppConfig) {
	s := &cfg.Settings
	if s.ClaudePath == "" {
		s.ClaudePath = "claude"
	}
	if s.DefaultRetryDelay == 0 {
		s.DefaultRetryDelay = 30
	}
	if s.RateLimitPause == 0 {
		s.RateLimitPause = 300
	}
	if s.LogRetentionDays == 0 {
		s.LogRetentionDays = 30
	}
	if s.Theme == "" {
		s.Theme = "dark"
	}
	if s.PreflightModel == "" {
		s.PreflightModel = "haiku"
	}
	if s.PermissionNotifyAfter == 0 {
		s.PermissionNotifyAfter = 30
	}
	if s.PermissionTimeoutAction == "" {
		s.PermissionTimeoutAction = "deny"
	}
	if s.RateLimitAlertThreshold == 0 {
		s.RateLimitAlertThreshold = 0.80
	}
	if s.SessionStartDelay == 0 {
		s.SessionStartDelay = 3
	}

	applyOptimizationDefaults(&cfg.Optimization)

	for i := range cfg.Projects {
		for j := range cfg.Projects[i].Sessions {
			applySessionDefaults(&cfg.Projects[i].Sessions[j])
		}
	}
}

func applyOptimizationDefaults(o *OptimizationSettings) {
	if o.ContextRestartThreshold == 0 {
		o.ContextRestartThreshold = 0.75
	}
	if o.ContextWarnThreshold == 0 {
		o.ContextWarnThreshold = 0.60
	}
	if o.ContextRestartMode == "" {
		o.ContextRestartMode = "resume"
	}
	if o.SessionStartDelay == 0 {
		o.SessionStartDelay = 3
	}
	if o.MaxTurnsDefault == 0 {
		o.MaxTurnsDefault = 50
	}
	if o.LoopThreshold == 0 {
		o.LoopThreshold = 3
	}
	if o.LoopAction == "" {
		o.LoopAction = "warn"
	}
	if o.LoopHint == "" {
		o.LoopHint = "You seem to be repeating the same actions. Try a different approach."
	}
	if o.LoopWindow == 0 {
		o.LoopWindow = 20
	}
}

func applySessionDefaults(s *SessionConfig) {
	if s.Model == "" {
		s.Model = "sonnet"
	}
	if s.Effort == "" {
		s.Effort = "high"
	}
	if s.PermissionMode == "" {
		s.PermissionMode = "acceptEdits"
	}
	if s.Preflight == "" {
		s.Preflight = "auto"
	}
}

func validate(cfg *AppConfig) error {
	for _, p := range cfg.Projects {
		if p.Name == "" {
			return fmt.Errorf("config: project missing name")
		}
		if p.Path == "" {
			return fmt.Errorf("config: project %q missing path", p.Name)
		}
		names := map[string]bool{}
		for _, s := range p.Sessions {
			if s.Name == "" {
				return fmt.Errorf("config: project %q has session with empty name", p.Name)
			}
			if names[s.Name] {
				return fmt.Errorf("config: project %q has duplicate session name %q", p.Name, s.Name)
			}
			names[s.Name] = true
		}
	}
	return nil
}
