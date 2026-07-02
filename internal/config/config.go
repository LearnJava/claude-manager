package config

import (
	"fmt"
	"net/url"
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
		if cfg.Projects[i].MixedMaxRounds == 0 {
			cfg.Projects[i].MixedMaxRounds = 3
		}
		for j := range cfg.Projects[i].Sessions {
			applySessionDefaults(&cfg.Projects[i].Sessions[j])
		}
	}

	for i := range cfg.Workers {
		applyWorkerDefaults(&cfg.Workers[i])
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

func applyWorkerDefaults(w *WorkerConfig) {
	if w.Role == "" {
		w.Role = "hands"
	}
	if w.ReasoningEffort == "" {
		w.ReasoningEffort = "low"
	}
	if w.MaxOutputTokens == 0 {
		w.MaxOutputTokens = 16000
	}
	if w.ContinuationCap == 0 {
		w.ContinuationCap = 3
	}
	if w.RequestTimeoutSec == 0 {
		w.RequestTimeoutSec = 180
	}
}

// kiloGatewayURL is the OpenAI-compatible gateway both preset workers use.
const kiloGatewayURL = "https://api.kilo.ai/api/gateway"

// WorkerPresets returns ready-made configs for the two models validated by the
// lumen bench (2026-07-02, ranking: step37 >= ultra >> the rest): Step 3.7
// Flash as "hands" (fast, precise on well-specified briefs) and Nemotron 3
// Ultra as "quality" (honest feedback cycle; needs pure-ASCII FIND anchors).
func WorkerPresets() []WorkerConfig {
	presets := []WorkerConfig{
		{
			Name:    "step37",
			BaseURL: kiloGatewayURL,
			Model:   "stepfun/step-3.7-flash:free",
			KeyEnv:  "KILO_API_KEY",
			Role:    "hands",
		},
		{
			Name:             "nemotron-ultra",
			BaseURL:          kiloGatewayURL,
			Model:            "nvidia/nemotron-3-ultra-550b-a55b:free",
			KeyEnv:           "KILO_API_KEY",
			Role:             "quality",
			ASCIIAnchorsOnly: true,
		},
	}
	for i := range presets {
		applyWorkerDefaults(&presets[i])
	}
	return presets
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
		if p.MixedProgramming {
			// Gates are the ground truth of mixed programming — a project must
			// not accept worker patches unchecked (model tests are untrusted).
			if len(p.Gates) == 0 {
				return fmt.Errorf("config: project %q enables mixed_programming but defines no gates", p.Name)
			}
			if len(cfg.Workers) == 0 {
				return fmt.Errorf("config: project %q enables mixed_programming but no [[worker]] is configured", p.Name)
			}
		}
	}

	workerNames := map[string]bool{}
	for _, w := range cfg.Workers {
		if err := validateWorker(w); err != nil {
			return err
		}
		if workerNames[w.Name] {
			return fmt.Errorf("config: duplicate worker name %q", w.Name)
		}
		workerNames[w.Name] = true
	}
	return nil
}

var workerRoles = map[string]bool{"hands": true, "quality": true, "eyes": true}

var workerEfforts = map[string]bool{"none": true, "low": true, "medium": true, "high": true}

func validateWorker(w WorkerConfig) error {
	if w.Name == "" {
		return fmt.Errorf("config: worker missing name")
	}
	if w.BaseURL == "" {
		return fmt.Errorf("config: worker %q missing base_url", w.Name)
	}
	u, err := url.Parse(w.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: worker %q has invalid base_url %q", w.Name, w.BaseURL)
	}
	if w.Model == "" {
		return fmt.Errorf("config: worker %q missing model", w.Name)
	}
	if w.KeyEnv == "" {
		return fmt.Errorf("config: worker %q missing key_env (API keys are read from the environment, never from config)", w.Name)
	}
	if !workerRoles[w.Role] {
		return fmt.Errorf("config: worker %q has invalid role %q (want hands|quality|eyes)", w.Name, w.Role)
	}
	if !workerEfforts[w.ReasoningEffort] {
		return fmt.Errorf("config: worker %q has invalid reasoning_effort %q (want none|low|medium|high)", w.Name, w.ReasoningEffort)
	}
	if w.ContinuationCap < 0 || w.MaxOutputTokens < 0 || w.RequestTimeoutSec < 0 {
		return fmt.Errorf("config: worker %q has negative limits", w.Name)
	}
	return nil
}
