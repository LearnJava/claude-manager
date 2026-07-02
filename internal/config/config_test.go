package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTOML writes content to a temp config file and returns its path.
func writeTOML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- Load basics ----

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "no-such.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.ClaudePath != "claude" {
		t.Errorf("ClaudePath = %q, want default \"claude\"", cfg.Settings.ClaudePath)
	}
	if len(cfg.Workers) != 0 {
		t.Errorf("expected no workers by default, got %d", len(cfg.Workers))
	}
}

// ---- Worker defaults ----

func TestWorkerDefaultsApplied(t *testing.T) {
	p := writeTOML(t, `
[[worker]]
name = "step37"
base_url = "https://api.kilo.ai/api/gateway"
model = "stepfun/step-3.7-flash:free"
key_env = "KILO_API_KEY"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Workers) != 1 {
		t.Fatalf("workers = %d, want 1", len(cfg.Workers))
	}
	w := cfg.Workers[0]
	if w.Role != "hands" {
		t.Errorf("Role = %q, want default \"hands\"", w.Role)
	}
	if w.ReasoningEffort != "low" {
		t.Errorf("ReasoningEffort = %q, want default \"low\"", w.ReasoningEffort)
	}
	if w.MaxOutputTokens != 16000 {
		t.Errorf("MaxOutputTokens = %d, want default 16000", w.MaxOutputTokens)
	}
	if w.ContinuationCap != 3 {
		t.Errorf("ContinuationCap = %d, want default 3", w.ContinuationCap)
	}
	if w.RequestTimeoutSec != 180 {
		t.Errorf("RequestTimeoutSec = %d, want default 180", w.RequestTimeoutSec)
	}
}

func TestMixedMaxRoundsDefault(t *testing.T) {
	p := writeTOML(t, `
[[project]]
name = "demo"
path = "C:/demo"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Projects[0].MixedMaxRounds; got != 3 {
		t.Errorf("MixedMaxRounds = %d, want default 3", got)
	}
}

// ---- Worker validation ----

const validWorkerBlock = `
[[worker]]
name = "step37"
base_url = "https://api.kilo.ai/api/gateway"
model = "stepfun/step-3.7-flash:free"
key_env = "KILO_API_KEY"
`

func TestWorkerValidation(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string // substring of the expected error; "" = expect success
	}{
		{"valid", validWorkerBlock, ""},
		{"missing_name", `
[[worker]]
base_url = "https://x.example"
model = "m"
key_env = "K"
`, "missing name"},
		{"missing_base_url", `
[[worker]]
name = "w"
model = "m"
key_env = "K"
`, "missing base_url"},
		{"bad_base_url_scheme", `
[[worker]]
name = "w"
base_url = "ftp://x.example"
model = "m"
key_env = "K"
`, "invalid base_url"},
		{"bad_base_url_no_host", `
[[worker]]
name = "w"
base_url = "https://"
model = "m"
key_env = "K"
`, "invalid base_url"},
		{"missing_model", `
[[worker]]
name = "w"
base_url = "https://x.example"
key_env = "K"
`, "missing model"},
		{"missing_key_env", `
[[worker]]
name = "w"
base_url = "https://x.example"
model = "m"
`, "missing key_env"},
		{"bad_role", `
[[worker]]
name = "w"
base_url = "https://x.example"
model = "m"
key_env = "K"
role = "driver"
`, "invalid role"},
		{"bad_effort", `
[[worker]]
name = "w"
base_url = "https://x.example"
model = "m"
key_env = "K"
reasoning_effort = "max"
`, "invalid reasoning_effort"},
		{"duplicate_names", validWorkerBlock + validWorkerBlock, "duplicate worker name"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTOML(t, tc.content))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load: expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// ---- Mixed programming opt-in rules ----

func TestMixedProgrammingRequiresGates(t *testing.T) {
	p := writeTOML(t, validWorkerBlock+`
[[project]]
name = "demo"
path = "C:/demo"
mixed_programming = true
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "no gates") {
		t.Fatalf("expected 'no gates' error, got %v", err)
	}
}

func TestMixedProgrammingRequiresWorkers(t *testing.T) {
	p := writeTOML(t, `
[[project]]
name = "demo"
path = "C:/demo"
mixed_programming = true
gates = ["go build ./...", "go test ./..."]
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "no [[worker]]") {
		t.Fatalf("expected 'no [[worker]]' error, got %v", err)
	}
}

func TestMixedProgrammingValidOptIn(t *testing.T) {
	p := writeTOML(t, validWorkerBlock+`
[[project]]
name = "demo"
path = "C:/demo"
mixed_programming = true
gates = ["go build ./...", "go test ./..."]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Projects[0].MixedProgramming {
		t.Error("MixedProgramming not set")
	}
	if len(cfg.Projects[0].Gates) != 2 {
		t.Errorf("Gates = %d, want 2", len(cfg.Projects[0].Gates))
	}
}

func TestMixedProgrammingOffByDefault(t *testing.T) {
	p := writeTOML(t, `
[[project]]
name = "demo"
path = "C:/demo"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Projects[0].MixedProgramming {
		t.Error("MixedProgramming must be an explicit opt-in, got true by default")
	}
}

// ---- Presets ----

func TestWorkerPresetsAreValid(t *testing.T) {
	presets := WorkerPresets()
	if len(presets) != 2 {
		t.Fatalf("presets = %d, want 2", len(presets))
	}
	byName := map[string]WorkerConfig{}
	for _, w := range presets {
		if err := validateWorker(w); err != nil {
			t.Errorf("preset %q invalid: %v", w.Name, err)
		}
		byName[w.Name] = w
	}
	step, ok := byName["step37"]
	if !ok {
		t.Fatal("missing step37 preset")
	}
	if step.Model != "stepfun/step-3.7-flash:free" || step.Role != "hands" {
		t.Errorf("step37 preset wrong: %+v", step)
	}
	ultra, ok := byName["nemotron-ultra"]
	if !ok {
		t.Fatal("missing nemotron-ultra preset")
	}
	if ultra.Model != "nvidia/nemotron-3-ultra-550b-a55b:free" || ultra.Role != "quality" {
		t.Errorf("nemotron-ultra preset wrong: %+v", ultra)
	}
	if !ultra.ASCIIAnchorsOnly {
		t.Error("nemotron-ultra preset must require ASCII anchors (lumen bench: Cyrillic in FIND anchors is unsafe for nemotron models)")
	}
}

// ---- Save/Load round-trip ----

func TestSaveLoadRoundTripWorkers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg := defaults()
	cfg.Workers = WorkerPresets()
	cfg.Projects = []ProjectConfig{{
		Name:             "demo",
		Path:             dir,
		MixedProgramming: true,
		Gates:            []string{"go test ./..."},
	}}
	if err := Save(cfg, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(got.Workers) != 2 {
		t.Fatalf("workers after round-trip = %d, want 2", len(got.Workers))
	}
	if got.Workers[0].Name != cfg.Workers[0].Name || got.Workers[0].Model != cfg.Workers[0].Model {
		t.Errorf("worker[0] round-trip mismatch: %+v", got.Workers[0])
	}
	if !got.Projects[0].MixedProgramming || len(got.Projects[0].Gates) != 1 {
		t.Errorf("project mixed settings round-trip mismatch: %+v", got.Projects[0])
	}
}
