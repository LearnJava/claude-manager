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
		if len(ov.DocSync) > 0 {
			p.DocSync = ov.DocSync
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
		if ov.Journal {
			p.Journal = true
		}
		if ov.JournalCommit {
			p.JournalCommit = true
		}
		if ov.AutoSkills {
			p.AutoSkills = true
		}
		if ov.AutoSkillsDailyBudgetUSD != 0 {
			p.AutoSkillsDailyBudgetUSD = ov.AutoSkillsDailyBudgetUSD
		}
		if ov.GateSchedule != "" {
			p.GateSchedule = ov.GateSchedule
		}
		if len(ov.GateScheduleGates) > 0 {
			p.GateScheduleGates = ov.GateScheduleGates
		}
		if ov.GateScheduleQueue != "" {
			p.GateScheduleQueue = ov.GateScheduleQueue
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
	if len(src.DocSync) > 0 {
		dst.DocSync = src.DocSync
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
	if src.Journal {
		dst.Journal = true
	}
	if src.JournalCommit {
		dst.JournalCommit = true
	}
	if src.AutoSkills {
		dst.AutoSkills = true
	}
	if src.AutoSkillsDailyBudgetUSD != 0 {
		dst.AutoSkillsDailyBudgetUSD = src.AutoSkillsDailyBudgetUSD
	}
	if src.GateSchedule != "" {
		dst.GateSchedule = src.GateSchedule
	}
	if len(src.GateScheduleGates) > 0 {
		dst.GateScheduleGates = src.GateScheduleGates
	}
	if src.GateScheduleQueue != "" {
		dst.GateScheduleQueue = src.GateScheduleQueue
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
		DocSync               []string        `toml:"doc_sync,omitempty"`
		DefaultPermissionMode string          `toml:"default_permission_mode"`
	}{Sessions: p.Sessions, Gates: p.Gates, DocSync: p.DocSync, DefaultPermissionMode: p.DefaultPermissionMode}
	if err := encodeAtomic(ProjectConfigPath(projectPath), shared); err != nil {
		return err
	}

	// Private layer: the privacy opt-in and the endpoints it sends code to.
	local := struct {
		MixedProgramming bool           `toml:"mixed_programming"`
		MixedMaxRounds   int            `toml:"mixed_max_rounds"`
		Journal          bool           `toml:"journal"`
		JournalCommit    bool           `toml:"journal_commit"`
		AutoSkills       bool           `toml:"auto_skills"`
		AutoSkillsBudget float64        `toml:"auto_skills_daily_budget_usd"`
		GateSchedule     string         `toml:"gate_schedule,omitempty"`
		GateSchedGates   []string       `toml:"gate_schedule_gates,omitempty"`
		GateSchedQueue   string         `toml:"gate_schedule_queue,omitempty"`
		Workers          []WorkerConfig `toml:"worker"`
	}{
		MixedProgramming: p.MixedProgramming,
		MixedMaxRounds:   p.MixedMaxRounds,
		Journal:          p.Journal,
		JournalCommit:    p.JournalCommit,
		AutoSkills:       p.AutoSkills,
		AutoSkillsBudget: p.AutoSkillsDailyBudgetUSD,
		GateSchedule:     p.GateSchedule,
		GateSchedGates:   p.GateScheduleGates,
		GateSchedQueue:   p.GateScheduleQueue,
		Workers:          localWorkers,
	}
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
			Language:                     "en",
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
			Billing:                      BillingSubscription,
			PythonUTF8:                   true,
			CommandGuard:                 true,
			EnvCard:                      true,
			ManagerCard:                  true,
			MirrorIntervalMin:            15,
			LogsIntervalMin:              60,
		},
		Optimization: optimizationDefaults(),
		Providers:    defaultProviders(),
	}
}

// defaultProviders seeds the built-in provider registry. Seeding (not a
// hard-coded lookup) means a user's [providers.*] sections in config.toml
// override these field-by-field, and adding e.g. a "[1m]" model variant is a
// pure config edit. GLM-PLAN.md §3.
func defaultProviders() map[string]ProviderConfig {
	return map[string]ProviderConfig{
		"glm": {
			DisplayName: "Claude GLM",
			BaseURL:     "https://api.z.ai/api/anthropic",
			KeyEnv:      "ZAI_API_KEY",
			SettingsDir: "state/glm",
			// The same GLM brain is reachable by the hermes runtime through
			// the CLI's own `zai` provider (GLM_API_KEY in hermes' .env),
			// and by the opencode runtime through its connected
			// zai-coding-plan integration (`opencode models`).
			HermesProviderID:   "zai",
			OpenCodeProviderID: "zai-coding-plan",
			Models: []ProviderModel{
				{ID: "glm-5.3", Label: "GLM-5.3", Efforts: []string{"low", "high", "max"}},
				{ID: "glm-5.3-flash", Label: "GLM-5.3-Flash", Efforts: []string{"low", "high", "max"}},
			},
		},
		"kimi": {
			DisplayName: "Kimi",
			// Kimi Coding Plan behind the local OpenAI-compatible proxy the
			// hermes CLI is already configured against (model.provider =
			// "custom", base_url http://127.0.0.1:8412/v1). The claude
			// runtime has no Anthropic-compatible endpoint here, so this
			// provider is hermes-only (no settings_dir, no key_env).
			BaseURL:          "http://127.0.0.1:8412/v1",
			HermesProviderID: "custom",
			Models: []ProviderModel{
				{ID: "kimi-for-coding", Label: "Kimi-for-Coding", Efforts: []string{"low", "high", "max"}},
				{ID: "k3", Label: "K3", Efforts: []string{"low", "high", "max"}},
				{ID: "k3.1-pro", Label: "K3.1-Pro", Efforts: []string{"low", "high", "max"}},
			},
		},
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
		ExperienceTracking:         true,
	}
}

// applyDefaults fills in zero-value fields in cfg with sensible defaults.
// Called after decoding so user can omit fields and still get working config.
func applyDefaults(cfg *AppConfig) {
	s := &cfg.Settings
	if s.ClaudePath == "" {
		s.ClaudePath = "claude"
	}
	if s.HermesPath == "" {
		s.HermesPath = "hermes"
	}
	if s.OpenCodePath == "" {
		s.OpenCodePath = "opencode"
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
	if s.Language == "" {
		s.Language = "en"
	}
	if s.Billing == "" {
		s.Billing = BillingSubscription
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

	applyProviderDefaults(cfg)
}

// applyProviderDefaults finishes the provider registry after decoding: the
// seeded defaults are refilled field-by-field into partially-overridden
// entries (TOML replaces a map entry wholesale, so a user's [providers.<id>]
// with only e.g. key_env would otherwise lose base_url), display_name and
// settings_dir get per-id fallbacks, and absent built-ins are seeded.
func applyProviderDefaults(cfg *AppConfig) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	for id, def := range defaultProviders() {
		p, ok := cfg.Providers[id]
		if !ok {
			p = def
		} else {
			if p.DisplayName == "" {
				p.DisplayName = def.DisplayName
			}
			if p.BaseURL == "" {
				p.BaseURL = def.BaseURL
			}
			if p.KeyEnv == "" {
				p.KeyEnv = def.KeyEnv
			}
			if p.SettingsDir == "" {
				p.SettingsDir = def.SettingsDir
			}
			if p.HermesProviderID == "" {
				p.HermesProviderID = def.HermesProviderID
			}
			if p.OpenCodeProviderID == "" {
				p.OpenCodeProviderID = def.OpenCodeProviderID
			}
			if len(p.Models) == 0 {
				p.Models = def.Models
			}
		}
		cfg.Providers[id] = p
	}
	for id, p := range cfg.Providers {
		if p.DisplayName == "" {
			p.DisplayName = id
		}
		// A provider with no KeyEnv has no Anthropic-compatible credentials
		// to put in a settings file — it is hermes-only, so no settings_dir
		// (an empty SettingsDir keeps providerArtifacts refusing it on the
		// claude runtime).
		if p.SettingsDir == "" && p.KeyEnv != "" {
			p.SettingsDir = filepath.Join("state", id)
		}
		cfg.Providers[id] = p
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
	// Retired stop_when_no_tasks: true used to make a session autonomous on
	// its own; auto_restart now carries that. A false was written by Settings,
	// not chosen by the user, so it is not carried into run_without_tasks.
	// Without a task_source there was no queue to run, so the key only made an
	// interactive session (Chat) exit after each answer; it must not turn into
	// auto_restart there.
	if s.StopWhenNoTasks && !s.AutoRestart && s.TaskSource != "" {
		s.AutoRestart = true
	}
	s.StopWhenNoTasks = false
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
	if b := cfg.Settings.Billing; b != "" && b != BillingSubscription && b != BillingAPI {
		return fmt.Errorf("config: invalid billing %q (want subscription|api)", b)
	}
	if cfg.Settings.LimitWindowTokens < 0 {
		return fmt.Errorf("config: limit_window_tokens must not be negative")
	}
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
			// NOTE: runtime values are deliberately NOT validated on load.
			// The home config and project overlays are shared between builds
			// of this repo, and another line's build may store runtime values
			// this one does not implement; rejecting the whole config on one
			// such session used to blank the entire app (the Load fallback to
			// defaults), and one Settings save in that state wiped every
			// [[project]]. Unknown values load and round-trip untouched; the
			// enum is enforced where it belongs — on writes (SetSessionRuntime,
			// the MCP config tools) and on launch (the manager refuses to
			// start a session whose runtime this build does not run).
			names[s.Name] = true
		}
		if p.GateSchedule != "" {
			if _, err := ParseGateSchedule(p.GateSchedule); err != nil {
				return fmt.Errorf("config: project %q: %w", p.Name, err)
			}
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

	for id, p := range cfg.Providers {
		if err := validateProvider(id, p); err != nil {
			return err
		}
	}
	return nil
}

// providerEfforts is the effort vocabulary the CLI/UI knows how to pass
// through --effort; a provider may offer any subset.
var providerEfforts = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// hermesProviderIDs is the hermes CLI's own `--provider` vocabulary (the
// model.provider list in a hermes config.yaml): named providers plus the
// "custom" escape hatch for any OpenAI-compatible endpoint. A
// hermes_provider_id outside this list would make every `hermes chat` launch
// fail with its own "unknown provider" error.
var hermesProviderIDs = map[string]bool{
	"auto": true, "openrouter": true, "nous": true, "nous-api": true,
	"anthropic": true, "openai-codex": true, "copilot": true, "gemini": true,
	"zai": true, "kimi-coding": true, "minimax": true, "minimax-cn": true,
	"huggingface": true, "nvidia": true, "xiaomi": true, "arcee": true,
	"ollama-cloud": true, "deepinfra": true, "kilocode": true,
	"ai-gateway": true, "azure-foundry": true, "lmstudio": true,
	"custom": true, "ollama": true, "vllm": true, "llamacpp": true,
}

func isHermesProviderID(id string) bool { return hermesProviderIDs[id] }

func validateProvider(id string, p ProviderConfig) error {
	if id == "" {
		return fmt.Errorf("config: provider with empty id")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("config: provider %q missing base_url", id)
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: provider %q has invalid base_url %q", id, p.BaseURL)
	}
	// A hermes-only provider (hermes_provider_id set, no settings dir) never
	// generates claude-runtime artifacts, so it needs no key_env; on any
	// other provider the key_env remains mandatory.
	hermesOnly := p.HermesProviderID != "" && p.SettingsDir == ""
	if !hermesOnly && !isEnvName(p.KeyEnv) {
		return fmt.Errorf("config: provider %q key_env %q must be the *name* of an environment variable holding the API key (the key itself is never stored in config)", id, p.KeyEnv)
	}
	if p.HermesProviderID != "" && !isEnvName(p.HermesProviderID) && !isHermesProviderID(p.HermesProviderID) {
		return fmt.Errorf("config: provider %q has invalid hermes_provider_id %q", id, p.HermesProviderID)
	}
	if len(p.Models) == 0 {
		return fmt.Errorf("config: provider %q has no models", id)
	}
	modelIDs := map[string]bool{}
	for _, m := range p.Models {
		if m.ID == "" {
			return fmt.Errorf("config: provider %q has model with empty id", id)
		}
		if modelIDs[m.ID] {
			return fmt.Errorf("config: provider %q has duplicate model id %q", id, m.ID)
		}
		modelIDs[m.ID] = true
		if len(m.Efforts) == 0 {
			return fmt.Errorf("config: provider %q model %q has no efforts", id, m.ID)
		}
		for _, e := range m.Efforts {
			if !providerEfforts[e] {
				return fmt.Errorf("config: provider %q model %q has invalid effort %q (want one of low|medium|high|xhigh|max)", id, m.ID, e)
			}
		}
	}
	return nil
}

// isEnvName reports whether s is a valid environment variable name (not a
// value): letters, digits and underscores, not starting with a digit. Catches
// a pasted API key being written where its variable name belongs.
func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
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
