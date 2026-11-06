package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providerCfg is a minimal valid provider for tests that do not care about
// the specifics.
func providerCfg() ProviderConfig {
	return ProviderConfig{
		DisplayName: "Test Provider",
		BaseURL:     "https://example.invalid/api",
		KeyEnv:      "TEST_PROVIDER_KEY",
		SettingsDir: "state/testprov",
		Models: []ProviderModel{
			{ID: "m1", Label: "M1", Efforts: []string{"low", "high"}},
		},
	}
}

func TestDefaultProvidersSeedGLM(t *testing.T) {
	cfg := defaults()
	p, ok := cfg.Providers["glm"]
	if !ok {
		t.Fatal("defaults() must seed the glm provider")
	}
	if p.DisplayName != "Claude GLM" || p.BaseURL == "" || p.KeyEnv != "ZAI_API_KEY" {
		t.Errorf("unexpected seeded provider: %+v", p)
	}
	if len(p.Models) != 2 {
		t.Fatalf("seeded provider models = %d, want 2", len(p.Models))
	}
	for _, m := range p.Models {
		if len(m.Efforts) != 3 || m.Efforts[0] != "low" || m.Efforts[2] != "max" {
			t.Errorf("model %q efforts = %v, want [low high max]", m.ID, m.Efforts)
		}
	}
}

func TestLoadKeepsUserProviderOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `
[settings]
claude_path = "claude"

[providers.glm]
display_name = "My GLM"
key_env = "MY_KEY"

[[providers.glm.models]]
id = "glm-5.3[1m]"
label = "GLM-5.3 1M"
efforts = ["low", "high", "max"]

[[project]]
name = "p"
path = "` + strings.ReplaceAll(dir, `\`, `/`) + `"
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Providers["glm"]
	if p.DisplayName != "My GLM" {
		t.Errorf("display_name = %q, want user override", p.DisplayName)
	}
	if p.KeyEnv != "MY_KEY" {
		t.Errorf("key_env = %q, want user override", p.KeyEnv)
	}
	if len(p.Models) != 1 || p.Models[0].ID != "glm-5.3[1m]" {
		t.Errorf("models = %+v, want the user's single [1m] variant", p.Models)
	}
}

func TestValidateProvider(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderConfig
		want string
	}{
		{"missing base_url", func() ProviderConfig { c := providerCfg(); c.BaseURL = ""; return c }(), "missing base_url"},
		{"bad base_url", func() ProviderConfig { c := providerCfg(); c.BaseURL = "not a url"; return c }(), "invalid base_url"},
		{"key_env looks like a value", func() ProviderConfig { c := providerCfg(); c.KeyEnv = "sk-secret-value"; return c }(), "key_env"},
		{"empty key_env", func() ProviderConfig { c := providerCfg(); c.KeyEnv = ""; return c }(), "key_env"},
		{"no models", func() ProviderConfig { c := providerCfg(); c.Models = nil; return c }(), "no models"},
		{"duplicate model id", func() ProviderConfig {
			c := providerCfg()
			c.Models = append(c.Models, ProviderModel{ID: "m1", Label: "dup", Efforts: []string{"low"}})
			return c
		}(), "duplicate model id"},
		{"unknown effort", func() ProviderConfig {
			c := providerCfg()
			c.Models[0].Efforts = []string{"turbo"}
			return c
		}(), "invalid effort"},
		{"empty model id", func() ProviderConfig {
			c := providerCfg()
			c.Models[0].ID = ""
			return c
		}(), "empty id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProvider("prov", tc.p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validateProvider() = %v, want error containing %q", err, tc.want)
			}
		})
	}
	if err := validateProvider("prov", providerCfg()); err != nil {
		t.Errorf("valid provider rejected: %v", err)
	}
}

func TestEnsureProviderSettings(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := &AppConfig{Providers: map[string]ProviderConfig{"prov": providerCfg()}}

	if err := EnsureProviderSettings(cfg, cfgPath); err != nil {
		t.Fatalf("EnsureProviderSettings: %v", err)
	}
	path := ProviderSettingsPath(cfg, cfgPath, "prov", "m1")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings file not created at %s: %v", path, err)
	}
	body := string(raw)
	for _, want := range []string{
		`"env": {`,
		`"ANTHROPIC_BASE_URL": "https://example.invalid/api"`,
		`"ANTHROPIC_DEFAULT_HAIKU_MODEL": "m1"`,
		`"ANTHROPIC_DEFAULT_SONNET_MODEL": "m1"`,
		`"ANTHROPIC_DEFAULT_OPUS_MODEL": "m1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings file missing %s\n%s", want, body)
		}
	}
	if strings.Contains(body, "AUTH_TOKEN") || strings.Contains(body, "API_KEY") {
		t.Errorf("settings file must not carry any credential\n%s", body)
	}
	if strings.Contains(body, "AUTO_COMPACT") {
		t.Errorf("non-[1m] model must not set the autocompact window\n%s", body)
	}

	// Idempotency: a manual edit survives regeneration attempts.
	edited := strings.Replace(body, `"API_TIMEOUT_MS": "3000000"`, `"API_TIMEOUT_MS": "6000000"`, 1)
	if edited == body {
		t.Fatal("test bug: edit target not present")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProviderSettings(cfg, cfgPath); err != nil {
		t.Fatalf("second EnsureProviderSettings: %v", err)
	}
	raw2, _ := os.ReadFile(path)
	if string(raw2) != edited {
		t.Error("EnsureProviderSettings overwrote a manual edit")
	}
}

func TestEnsureProviderSettings1MVariant(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := &AppConfig{Providers: map[string]ProviderConfig{"prov": {
		BaseURL:     "https://example.invalid/api",
		KeyEnv:      "TEST_PROVIDER_KEY",
		SettingsDir: "state/testprov",
		Models:      []ProviderModel{{ID: "m1[1m]", Label: "M1", Efforts: []string{"low"}}},
	}}}
	if err := EnsureProviderSettings(cfg, cfgPath); err != nil {
		t.Fatal(err)
	}
	path := ProviderSettingsPath(cfg, cfgPath, "prov", "m1[1m]")
	if filepath.Base(path) != "m1_1m_.json" {
		t.Errorf("sanitized file name = %q", filepath.Base(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000"`) {
		t.Errorf("[1m] settings missing autocompact window\n%s", raw)
	}
}

func TestProviderSettingsPathUnknownProvider(t *testing.T) {
	cfg := &AppConfig{}
	if p := ProviderSettingsPath(cfg, "x.toml", "nobody", "m1"); p != "" {
		t.Errorf("unknown provider path = %q, want empty", p)
	}
}

func TestProviderForModel(t *testing.T) {
	cfg := &AppConfig{Providers: map[string]ProviderConfig{"prov": providerCfg()}}
	if got := ProviderForModel(cfg, "m1"); got != "prov" {
		t.Errorf("ProviderForModel(m1) = %q, want prov", got)
	}
	if got := ProviderForModel(cfg, "sonnet"); got != "" {
		t.Errorf("ProviderForModel(sonnet) = %q, want empty", got)
	}
	if got := ProviderForModel(nil, "m1"); got != "" {
		t.Errorf("ProviderForModel(nil) = %q, want empty", got)
	}
}

func TestProviderAuthToken(t *testing.T) {
	p := providerCfg()
	t.Setenv("TEST_PROVIDER_KEY", "from-env")
	cfg := &AppConfig{}
	if got := ProviderAuthToken(cfg, p); got != "from-env" {
		t.Errorf("env key lost: %q", got)
	}
	// Env wins over the config fallback.
	cfg.Settings.ZCodeAPIKey = "from-config"
	if got := ProviderAuthToken(cfg, p); got != "from-env" {
		t.Errorf("env must win over config fallback, got %q", got)
	}
	// Fallback when the env var is absent.
	t.Setenv("TEST_PROVIDER_KEY", "")
	if got := ProviderAuthToken(cfg, p); got != "from-config" {
		t.Errorf("config fallback lost: %q", got)
	}
	// Nothing anywhere: empty, not an error.
	cfg.Settings.ZCodeAPIKey = ""
	if got := ProviderAuthToken(cfg, p); got != "" {
		t.Errorf("want empty key, got %q", got)
	}
}
