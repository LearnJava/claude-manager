package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const defaultConfigDir = ".claude-manager"
const defaultConfigFile = "config.toml"
const projectLocalConfigFile = "config.local.toml"

// DefaultConfigPath returns the default path to the config file (~/.claude-manager/config.toml).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, defaultConfigDir, defaultConfigFile)
}

// ProjectConfigDir returns <projectPath>/.claude-manager — the per-project
// config folder that travels with the repository.
func ProjectConfigDir(projectPath string) string {
	return filepath.Join(projectPath, defaultConfigDir)
}

// ProjectConfigPath returns the committed, shared per-project config file.
func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, defaultConfigDir, defaultConfigFile)
}

// ProjectLocalConfigPath returns the gitignored, private per-project config file.
func ProjectLocalConfigPath(projectPath string) string {
	return filepath.Join(projectPath, defaultConfigDir, projectLocalConfigFile)
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

	if err := applyProjectOverlays(cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyProjectOverlays merges each project's <path>/.claude-manager overlay onto
// its global [[project]] entry, and merges any overlay-declared workers into the
// global worker registry (overlay wins, deduped by name).
func applyProjectOverlays(cfg *AppConfig) error {
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.Path == "" {
			continue
		}
		ov, err := LoadProjectOverlay(p.Path)
		if err != nil {
			return err
		}
		if ov == nil {
			continue
		}
		if len(ov.Sessions) > 0 {
			p.Sessions = ov.Sessions
		}
		if len(ov.Gates) > 0 {
			p.Gates = ov.Gates
		}
		if ov.DefaultPermissionMode != "" {
			p.DefaultPermissionMode = ov.DefaultPermissionMode
		}
		if ov.MixedProgramming != nil {
			p.MixedProgramming = *ov.MixedProgramming
		}
		if ov.MixedMaxRounds != 0 {
			p.MixedMaxRounds = ov.MixedMaxRounds
		}
		if len(ov.Workers) > 0 {
			cfg.Workers = mergeWorkers(cfg.Workers, ov.Workers)
		}
	}
	return nil
}

// LoadProjectOverlay reads and merges the per-project config files under
// <projectPath>/.claude-manager/. It returns (nil, nil) when neither file
// exists. config.local.toml overrides config.toml field-by-field.
func LoadProjectOverlay(projectPath string) (*ProjectOverlay, error) {
	shared, sharedOK, err := decodeOverlay(ProjectConfigPath(projectPath))
	if err != nil {
		return nil, err
	}
	local, localOK, err := decodeOverlay(ProjectLocalConfigPath(projectPath))
	if err != nil {
		return nil, err
	}
	if !sharedOK && !localOK {
		return nil, nil
	}
	merged := shared
	mergeOverlay(&merged, local)
	return &merged, nil
}

func decodeOverlay(path string) (ProjectOverlay, bool, error) {
	var o ProjectOverlay
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return o, false, nil
	}
	if _, err := toml.DecodeFile(path, &o); err != nil {
		return o, false, fmt.Errorf("config: decode overlay %s: %w", path, err)
	}
	return o, true, nil
}

// mergeOverlay applies the non-empty fields of src onto dst (src = local layer).
func mergeOverlay(dst *ProjectOverlay, src ProjectOverlay) {
	if len(src.Sessions) > 0 {
		dst.Sessions = src.Sessions
	}
	if len(src.Gates) > 0 {
		dst.Gates = src.Gates
	}
	if src.DefaultPermissionMode != "" {
		dst.DefaultPermissionMode = src.DefaultPermissionMode
	}
	if src.MixedProgramming != nil {
		dst.MixedProgramming = src.MixedProgramming
	}
	if src.MixedMaxRounds != 0 {
		dst.MixedMaxRounds = src.MixedMaxRounds
	}
	if len(src.Workers) > 0 {
		dst.Workers = mergeWorkers(dst.Workers, src.Workers)
	}
}

// mergeWorkers returns base with extra merged in, deduped by Name (extra wins).
func mergeWorkers(base, extra []WorkerConfig) []WorkerConfig {
	out := append([]WorkerConfig(nil), base...)
	idx := map[string]int{}
	for i, w := range out {
		idx[w.Name] = i
	}
	for _, w := range extra {
		if i, ok := idx[w.Name]; ok {
			out[i] = w
		} else {
			idx[w.Name] = len(out)
			out = append(out, w)
		}
	}
	return out
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

// SaveProjectOverlay writes the per-project config into <projectPath>/.claude-manager/,
// splitting it into the committed config.toml (sessions, gates) and the
// gitignored config.local.toml (mixed_programming opt-in, private workers). The
// caller passes only the workers that belong to this project's local layer.
// config.local.toml is added to the folder's .gitignore.
func SaveProjectOverlay(projectPath string, p ProjectConfig, localWorkers []WorkerConfig) error {
	dir := ProjectConfigDir(projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}

	// Committed layer: safe to share, no external endpoints.
	shared := struct {
		Sessions              []SessionConfig `toml:"session"`
		Gates                 []string        `toml:"gates"`
		DefaultPermissionMode string          `toml:"default_permission_mode"`
	}{Sessions: p.Sessions, Gates: p.Gates, DefaultPermissionMode: p.DefaultPermissionMode}
	if err := encodeAtomic(ProjectConfigPath(projectPath), shared); err != nil {
		return err
	}

	// Private layer: the privacy opt-in and the endpoints it sends code to.
	local := struct {
		MixedProgramming bool           `toml:"mixed_programming"`
		MixedMaxRounds   int            `toml:"mixed_max_rounds"`
		Workers          []WorkerConfig `toml:"worker"`
	}{MixedProgramming: p.MixedProgramming, MixedMaxRounds: p.MixedMaxRounds, Workers: localWorkers}
	if err := encodeAtomic(ProjectLocalConfigPath(projectPath), local); err != nil {
		return err
	}

	return EnsureGitignore(dir, projectLocalConfigFile)
}

// encodeAtomic TOML-encodes v to path via a temp file + rename.
func encodeAtomic(path string, v any) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("config: create %s: %w", tmp, err)
	}
	if err := toml.NewEncoder(f).Encode(v); err != nil {
		f.Close()
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: rename %s: %w", tmp, err)
	}
	return nil
}

// EnsureGitignore appends entry to dir/.gitignore unless already present.
// Exported so other packages that write into a project's .claude-manager
// folder (e.g. internal/store's auto-saved session log files) can gitignore
// their own subdirectory the same way config.local.toml is.
func EnsureGitignore(dir, entry string) error {
	p := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", p, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	content := string(existing)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", p, err)
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
			applySessionDefaults(&cfg.Projects[i].Sessions[j], cfg.Projects[i].DefaultPermissionMode)
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

// applySessionDefaults fills in zero-value fields on s. projectDefaultPermissionMode
// is the owning project's DefaultPermissionMode (may be empty); it only seeds a
// still-unset PermissionMode, so it never overrides a value the user already
// saved for this session.
func applySessionDefaults(s *SessionConfig, projectDefaultPermissionMode string) {
	if s.Model == "" {
		s.Model = "sonnet"
	}
	if s.Effort == "" {
		s.Effort = "high"
	}
	if s.PermissionMode == "" {
		if projectDefaultPermissionMode != "" {
			s.PermissionMode = projectDefaultPermissionMode
		} else {
			s.PermissionMode = "bypassPermissions"
		}
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
