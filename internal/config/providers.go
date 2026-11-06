package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// providerSettingsSuffix marks a model id whose settings file additionally
// sets the 1M-context autocompact window (e.g. "glm-5.3[1m]").
const providerSettingsSuffix = "[1m]"

// ProviderSettingsPath returns the settings file path for provider/model:
// <cfgDir>/<settings_dir>/<sanitized model id>.json. cfgPath may be empty,
// in which case the path is relative to the working directory (tests).
func ProviderSettingsPath(cfg *AppConfig, cfgPath, provider, model string) string {
	p, ok := cfg.Providers[provider]
	if !ok {
		return ""
	}
	dir := p.SettingsDir
	if dir == "" {
		dir = filepath.Join("state", provider)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(cfgPath), dir)
	}
	return filepath.Join(dir, sanitizeModelID(model)+".json")
}

// sanitizeModelID folds a model id into a safe file name. Ids like
// "glm-5.3[1m]" contain characters some filesystems reject.
func sanitizeModelID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// EnsureProviderSettings idempotently creates the per-model settings files
// for every configured provider: <cfgDir>/<settings_dir>/<model>.json holding
// the endpoint env block the CLI needs to talk to the provider. Existing
// files are NEVER rewritten — a user's manual edits survive every restart
// (GLM-PLAN.md §4). The auth token deliberately never appears in these
// files; it is injected into the process env instead (GLM-PLAN.md §5).
func EnsureProviderSettings(cfg *AppConfig, cfgPath string) error {
	for id, p := range cfg.Providers {
		for _, m := range p.Models {
			path := ProviderSettingsPath(cfg, cfgPath, id, m.ID)
			if path == "" {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				continue // never overwrite: manual edits are the user's
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("provider %q settings %s: %w", id, path, err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("provider %q settings dir: %w", id, err)
			}
			data, err := json.MarshalIndent(map[string]any{"env": providerSettingsEnv(p, m.ID)}, "", "  ")
			if err != nil {
				return fmt.Errorf("provider %q settings json: %w", id, err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
				return fmt.Errorf("provider %q settings %s: %w", id, path, err)
			}
		}
	}
	return nil
}

// providerSettingsEnv builds the "env" block of a provider settings file:
// the Anthropic-compatible endpoint plus alias guards so any haiku/sonnet/
// opus resolution inside the CLI lands on the same provider model. The
// "[1m]" suffix switches on the 1M-context autocompact window.
func providerSettingsEnv(p ProviderConfig, modelID string) map[string]string {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":             p.BaseURL,
		"API_TIMEOUT_MS":                 "3000000",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  modelID,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": modelID,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   modelID,
	}
	if strings.HasSuffix(modelID, providerSettingsSuffix) {
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "1000000"
	}
	return env
}

// ProviderForModel returns the id of the provider offering modelID, or ""
// when the model belongs to no provider (a plain Claude model). The first
// matching provider wins; provider model ids are expected to be distinctive.
func ProviderForModel(cfg *AppConfig, modelID string) string {
	if modelID == "" || cfg == nil {
		return ""
	}
	for id, p := range cfg.Providers {
		for _, m := range p.Models {
			if m.ID == modelID {
				return id
			}
		}
	}
	return ""
}

// ProviderAuthToken returns the API key for a provider launch: the provider's
// key_env variable when the manager's environment carries it, otherwise the
// live-config zcode_api_key fallback (the key sits there for the zcode
// runtime on machines that run both builds). Empty means "no key available" —
// the caller warns and starts anyway; the CLI then fails with its own
// understandable auth error. The key is returned for the child process env
// only and is never written to disk by the manager.
func ProviderAuthToken(cfg *AppConfig, p ProviderConfig) string {
	if key := os.Getenv(p.KeyEnv); key != "" {
		return key
	}
	if cfg != nil {
		return cfg.Settings.ZCodeAPIKey
	}
	return ""
}
