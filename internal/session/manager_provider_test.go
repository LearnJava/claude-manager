package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// newProviderTestManager builds a manager whose config carries one provider
// ("prov") with model "m1", plus a session P1.
func newProviderTestManager(t *testing.T) (*SessionManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.AppConfig{
		Settings: config.GlobalSettings{ClaudePath: "claude", DefaultRetryDelay: 1, RateLimitPause: 1},
		Providers: map[string]config.ProviderConfig{
			"prov": {
				DisplayName: "Prov",
				BaseURL:     "https://example.invalid/api",
				KeyEnv:      "PROV_TEST_KEY",
				SettingsDir: "state/prov",
				Models:      []config.ProviderModel{{ID: "m1", Label: "M1", Efforts: []string{"low", "high"}}},
			},
		},
		Projects: []config.ProjectConfig{{
			Name: "lumen",
			Path: dir,
			Sessions: []config.SessionConfig{
				{Name: "P1", Model: "sonnet"},
			},
		}},
	}
	return NewSessionManager(cfg, filepath.Join(dir, "config.toml"), nil, nil), dir
}

func TestProviderArtifacts(t *testing.T) {
	m, dir := newProviderTestManager(t)

	// Empty provider: nothing, and no error (plain Claude launch).
	sf, env, err := m.providerArtifacts("", "sonnet")
	if err != nil || sf != "" || env != nil {
		t.Errorf("empty provider = (%q, %v, %v), want zero values", sf, env, err)
	}

	// Unknown provider: hard error.
	if _, _, err := m.providerArtifacts("nobody", "m1"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unknown provider err = %v", err)
	}

	// Provider session without a model: per-model files need a model.
	if _, _, err := m.providerArtifacts("prov", ""); err == nil || !strings.Contains(err.Error(), "needs a model") {
		t.Errorf("modelless provider err = %v", err)
	}

	// Happy path: settings file created on demand, auth token from key_env.
	t.Setenv("PROV_TEST_KEY", "secret-token")
	sf, env, err = m.providerArtifacts("prov", "m1")
	if err != nil {
		t.Fatalf("providerArtifacts: %v", err)
	}
	wantPath := filepath.Join(dir, "state", "prov", "m1.json")
	if sf != wantPath {
		t.Errorf("settingsFile = %q, want %q", sf, wantPath)
	}
	if _, err := os.ReadFile(sf); err != nil {
		t.Errorf("settings file not created: %v", err)
	}
	if len(env) != 1 || env[0] != "ANTHROPIC_AUTH_TOKEN=secret-token" {
		t.Errorf("env = %v, want the auth token pair", env)
	}

	// Key neither in env nor config: no pair, no error (the CLI will fail
	// with its own auth error; the manager only warns).
	t.Setenv("PROV_TEST_KEY", "")
	sf, env, err = m.providerArtifacts("prov", "m1")
	if err != nil || sf == "" || env != nil {
		t.Errorf("missing key = (%q, %v, %v), want file and no env", sf, env, err)
	}

	// Live-config fallback when the env var is empty.
	m.cfg.Settings.ZCodeAPIKey = "config-fallback"
	_, env, err = m.providerArtifacts("prov", "m1")
	if err != nil || len(env) != 1 || env[0] != "ANTHROPIC_AUTH_TOKEN=config-fallback" {
		t.Errorf("fallback env = %v (err %v)", env, err)
	}
}

func TestSetSessionModelDerivesProvider(t *testing.T) {
	m, _ := newProviderTestManager(t)

	// Not-yet-started session: the config default is updated in place.
	if err := m.SetSessionModel("lumen/P1", "m1"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	_, sc, err := m.findConfig("lumen", "P1")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Model != "m1" || sc.Provider != "prov" {
		t.Errorf("config = (model %q, provider %q), want (m1, prov)", sc.Model, sc.Provider)
	}

	// Switching back to a plain Claude model clears the provider.
	if err := m.SetSessionModel("lumen/P1", "sonnet"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if sc.Model != "sonnet" || sc.Provider != "" {
		t.Errorf("config = (model %q, provider %q), want (sonnet, \"\")", sc.Model, sc.Provider)
	}
}

// TestStartSessionRefusesForeignRuntime: a session whose runtime this build
// does not run (a value written into the shared home config by another
// line's build) loads and displays, but its launch is refused with a clear
// error — never a silent claude-CLI fallback.
func TestStartSessionRefusesForeignRuntime(t *testing.T) {
	m, _ := newProviderTestManager(t)
	m.cfg.Projects[0].Sessions[0].Runtime = "something-else"
	err := m.StartSession("lumen", "P1")
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("StartSession on foreign runtime = %v, want a refusal", err)
	}
	if err := m.StartSessionWithProvider("lumen", "P1", "", "sonnet", ""); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("StartSessionWithProvider on foreign runtime = %v, want a refusal", err)
	}
}
