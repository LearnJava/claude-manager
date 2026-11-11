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

// TestSetSessionProvider: the "which AI" dropdown switch — provider set with
// the provider's first model on a crossing, cleared back to plain Claude,
// unknown refused, hermes refused for providers.
func TestSetSessionProvider(t *testing.T) {
	m, _ := newProviderTestManager(t)

	if _, err := m.SetSessionProvider("lumen/P1", "nobody"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unknown provider err = %v", err)
	}

	// Not-started session: provider set, model jumps to the provider's first.
	model, err := m.SetSessionProvider("lumen/P1", "prov")
	if err != nil {
		t.Fatalf("SetSessionProvider: %v", err)
	}
	if model != "m1" {
		t.Errorf("resolved model = %q, want m1", model)
	}
	_, sc, _ := m.findConfig("lumen", "P1")
	if sc.Provider != "prov" || sc.Model != "m1" {
		t.Errorf("config = (%q, %q), want (prov, m1)", sc.Provider, sc.Model)
	}

	// A provider model already set keeps its place on a re-select.
	sc.Model = "m1"
	if _, err := m.SetSessionProvider("lumen/P1", "prov"); err != nil {
		t.Fatalf("re-select: %v", err)
	}

	// Back to plain Claude: provider cleared, model stays.
	if _, err := m.SetSessionProvider("lumen/P1", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	_, sc, _ = m.findConfig("lumen", "P1")
	if sc.Provider != "" || sc.Model != "m1" {
		t.Errorf("after clear = (%q, %q), want (\"\", m1)", sc.Provider, sc.Model)
	}

	// SetSessionModelWithEffort writes both.
	if err := m.SetSessionModelWithEffort("lumen/P1", "m1", "low"); err != nil {
		t.Fatalf("SetSessionModelWithEffort: %v", err)
	}
	if sc.Effort != "low" || sc.Provider != "prov" || sc.Model != "m1" {
		t.Errorf("after effort switch = %+v", sc)
	}
}

// newHermesProviderTestManager: same as newProviderTestManager but the P1
// session runs on the hermes runtime, and the provider carries a
// hermes_provider_id ("zai"-like).
func newHermesProviderTestManager(t *testing.T) (*SessionManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.AppConfig{
		Settings: config.GlobalSettings{ClaudePath: "claude", HermesPath: "hermes", DefaultRetryDelay: 1, RateLimitPause: 1},
		Providers: map[string]config.ProviderConfig{
			"prov": {
				DisplayName: "Prov", BaseURL: "https://example.invalid/api",
				KeyEnv: "PROV_TEST_KEY", SettingsDir: "state/prov",
				HermesProviderID: "zai",
				Models:           []config.ProviderModel{{ID: "m1", Label: "M1", Efforts: []string{"low", "high"}}},
			},
			"honly": {
				DisplayName: "HermesOnly", BaseURL: "http://127.0.0.1:8412/v1",
				HermesProviderID: "custom",
				Models:           []config.ProviderModel{{ID: "k3", Label: "K3", Efforts: []string{"low"}}},
			},
		},
		Projects: []config.ProjectConfig{{
			Name: "lumen", Path: dir,
			Sessions: []config.SessionConfig{
				{Name: "P1", Model: "sonnet", Runtime: "hermes"},
			},
		}},
	}
	return NewSessionManager(cfg, filepath.Join(dir, "config.toml"), nil, nil), dir
}

// TestSetSessionProviderHermes: a hermes session selects a provider and gets
// the registry's hermes_provider_id in HermesProvider plus the provider's
// first model; a provider without hermes_provider_id is refused; the
// hermes-only provider works the same way.
func TestSetSessionProviderHermes(t *testing.T) {
	m, _ := newHermesProviderTestManager(t)

	model, err := m.SetSessionProvider("lumen/P1", "prov")
	if err != nil {
		t.Fatalf("SetSessionProvider on hermes: %v", err)
	}
	if model != "m1" {
		t.Errorf("resolved model = %q, want m1", model)
	}
	_, sc, _ := m.findConfig("lumen", "P1")
	if sc.Provider != "prov" || sc.HermesProvider != "zai" || sc.Model != "m1" {
		t.Errorf("config = (provider %q, hermes %q, model %q), want (prov, zai, m1)",
			sc.Provider, sc.HermesProvider, sc.Model)
	}

	// Back to plain hermes (no provider): both cleared.
	if _, err := m.SetSessionProvider("lumen/P1", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	_, sc, _ = m.findConfig("lumen", "P1")
	if sc.Provider != "" || sc.HermesProvider != "" {
		t.Errorf("after clear = (%q, %q), want empty", sc.Provider, sc.HermesProvider)
	}

	// The hermes-only provider (no settings dir) is selectable on hermes.
	if model, err := m.SetSessionProvider("lumen/P1", "honly"); err != nil || model != "k3" {
		t.Errorf("hermes-only provider = (%q, %v), want (k3, nil)", model, err)
	}
	_, sc, _ = m.findConfig("lumen", "P1")
	if sc.HermesProvider != "custom" || sc.Model != "k3" {
		t.Errorf("hermes-only config = (%q, %q), want (custom, k3)", sc.HermesProvider, sc.Model)
	}
}

// TestProviderArtifactsHermesOnlyRefused: a hermes-only provider (no settings
// dir) is refused on the claude runtime — no settings file could ever point
// the claude CLI at its OpenAI-compatible endpoint.
func TestProviderArtifactsHermesOnlyRefused(t *testing.T) {
	m, _ := newHermesProviderTestManager(t)
	_, _, err := m.providerArtifacts("honly", "k3")
	if err == nil || !strings.Contains(err.Error(), "hermes-only") {
		t.Errorf("hermes-only on claude = %v, want a refusal", err)
	}
}

// TestSetSessionProviderHermesLive: a LIVE hermes session (a managedSession
// exists) switching providers must update the live session's argv inputs —
// hermes builds every turn's --provider/-m from the Session's own Config
// copy, which SetSessionProvider used to leave stale, so the switch showed
// in the UI while every following turn kept riding the previous brain.
// Leaving a provider (back to plain hermes) clears the live flag the same way.
func TestSetSessionProviderHermesLive(t *testing.T) {
	m, _ := newHermesProviderTestManager(t)

	// Register a live hermes session the way a real start does: a Session
	// whose Config copy carries the runtime and the current brain.
	sc := config.SessionConfig{Name: "P1", Model: "sonnet", Runtime: config.RuntimeHermes}
	sess := New(Params{
		ID:          sessionID("lumen", "P1"),
		ProjectName: "lumen",
		ProjectPath: ".",
		Config:      sc,
		OnEvent:     func(id string, ev SessionEvent) { m.onSessionEvent(id, ev) },
	})
	ms := &managedSession{session: sess, project: "lumen", name: "P1"}
	m.mu.Lock()
	m.sessions[sess.ID] = ms
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); delete(m.sessions, sess.ID); m.mu.Unlock() })

	// Select a provider: model resolves to its first, and the live copy
	// must carry both the --provider flag and the model for the next turn.
	model, err := m.SetSessionProvider("lumen/P1", "prov")
	if err != nil {
		t.Fatalf("SetSessionProvider live hermes: %v", err)
	}
	if model != "m1" {
		t.Errorf("resolved model = %q, want m1", model)
	}
	_, cfgSc, _ := m.findConfig("lumen", "P1")
	if cfgSc.Provider != "prov" || cfgSc.HermesProvider != "zai" || cfgSc.Model != "m1" {
		t.Errorf("config = (%q, %q, %q), want (prov, zai, m1)", cfgSc.Provider, cfgSc.HermesProvider, cfgSc.Model)
	}
	if got := sess.ActiveModel(); got != "m1" {
		t.Errorf("live activeModel = %q, want m1 (the next turn rides the old model otherwise)", got)
	}
	args := sess.buildHermesArgs("", "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--provider zai") {
		t.Errorf("hermes argv %v lacks --provider zai — the next turn would not ride the new brain", args)
	}

	// Leave the provider: the live flag must clear too, or the next turn
	// keeps --provider zai while the UI shows plain Hermes.
	if _, err := m.SetSessionProvider("lumen/P1", ""); err != nil {
		t.Fatalf("clear live hermes: %v", err)
	}
	args = sess.buildHermesArgs("", "")
	if joined := strings.Join(args, " "); strings.Contains(joined, "--provider") {
		t.Errorf("hermes argv %v still carries --provider after clearing", args)
	}
	_, cfgSc, _ = m.findConfig("lumen", "P1")
	if cfgSc.Provider != "" || cfgSc.HermesProvider != "" {
		t.Errorf("after clear = (%q, %q), want empty", cfgSc.Provider, cfgSc.HermesProvider)
	}
}

// TestSetSessionProviderRefusalLeavesConfigUntouched: a refused switch (a
// hermes session picking a claude-only provider) must not leave a half-set
// provider behind — findConfig returns a pointer into the live config, so
// the validation has to run before any mutation.
func TestSetSessionProviderRefusalLeavesConfigUntouched(t *testing.T) {
	m, _ := newHermesProviderTestManager(t)

	_, before, _ := m.findConfig("lumen", "P1")
	beforeModel := before.Model

	if _, err := m.SetSessionProvider("lumen/P1", "claudeonly"); err == nil {
		t.Fatal("unknown provider accepted")
	}

	// The registry has no claude-only provider; make one to test the refusal.
	m.mu.Lock()
	m.cfg.Providers["claudeonly"] = config.ProviderConfig{
		DisplayName: "ClaudeOnly", BaseURL: "https://example.invalid/api",
		KeyEnv: "PROV_TEST_KEY", SettingsDir: "state/prov",
		Models: []config.ProviderModel{{ID: "c1", Label: "C1", Efforts: []string{"low"}}},
	}
	m.mu.Unlock()

	if _, err := m.SetSessionProvider("lumen/P1", "claudeonly"); err == nil || !strings.Contains(err.Error(), "no hermes runtime") {
		t.Fatalf("claude-only on hermes = %v, want a refusal", err)
	}
	_, after, _ := m.findConfig("lumen", "P1")
	if after.Provider != before.Provider || after.HermesProvider != before.HermesProvider || after.Model != beforeModel {
		t.Errorf("refused switch mutated the config: before=(%q,%q,%q) after=(%q,%q,%q)",
			before.Provider, before.HermesProvider, beforeModel,
			after.Provider, after.HermesProvider, after.Model)
	}
}
