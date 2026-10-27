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

// ---- Per-project overlay (settings in the project folder) ----

// writeGlobalWithProject writes a global config whose single project points at
// projectPath, and returns the global config file path.
func writeGlobalWithProject(t *testing.T, projectPath string) string {
	t.Helper()
	return writeTOML(t, `
[[project]]
name = "demo"
path = `+`"`+strings.ReplaceAll(projectPath, `\`, `\\`)+`"`+`
`)
}

// writeProjectOverlay writes files under <projectPath>/.claude-manager/.
func writeProjectOverlay(t *testing.T, projectPath, file, content string) {
	t.Helper()
	dir := filepath.Join(projectPath, ".claude-manager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOverlaySessionsMergedFromProjectFolder(t *testing.T) {
	proj := t.TempDir()
	writeProjectOverlay(t, proj, "config.toml", `
[[session]]
name = "P1"
model = "opus"

[[session]]
name = "P2"
`)
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (from overlay)", len(sessions))
	}
	if sessions[0].Name != "P1" || sessions[0].Model != "opus" {
		t.Errorf("session[0] = %+v, want P1/opus", sessions[0])
	}
	if sessions[1].Effort != "high" {
		t.Errorf("overlay session defaults not applied: effort = %q", sessions[1].Effort)
	}
}

func TestOverlayLocalWinsOverCommitted(t *testing.T) {
	proj := t.TempDir()
	writeProjectOverlay(t, proj, "config.toml", `
gates = ["go build ./..."]
[[session]]
name = "shared"
`)
	writeProjectOverlay(t, proj, "config.local.toml", `
[[session]]
name = "local-override"
`)
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Projects[0].Sessions
	if len(s) != 1 || s[0].Name != "local-override" {
		t.Fatalf("sessions = %+v, want single local-override (local layer wins)", s)
	}
	// Gates come only from the committed layer; local must not wipe them.
	if len(cfg.Projects[0].Gates) != 1 {
		t.Errorf("gates = %v, want committed layer preserved", cfg.Projects[0].Gates)
	}
}

func TestOverlayMixedOptInIsPrivateLayer(t *testing.T) {
	proj := t.TempDir()
	writeProjectOverlay(t, proj, "config.toml", `
gates = ["go test ./..."]
`)
	writeProjectOverlay(t, proj, "config.local.toml", `
mixed_programming = true
[[worker]]
name = "step37"
base_url = "https://api.kilo.ai/api/gateway"
model = "stepfun/step-3.7-flash:free"
key_env = "KILO_API_KEY"
`)
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Projects[0].MixedProgramming {
		t.Error("mixed_programming opt-in from local layer not applied")
	}
	if len(cfg.Workers) != 1 || cfg.Workers[0].Name != "step37" {
		t.Errorf("overlay worker not merged into global registry: %+v", cfg.Workers)
	}
	// Validation runs post-merge: gates (committed) + worker (local) satisfy opt-in.
}

func TestOverlayJournalOptInIsPrivateLayer(t *testing.T) {
	proj := t.TempDir()
	writeProjectOverlay(t, proj, "config.toml", `
gates = ["go test ./..."]
`)
	writeProjectOverlay(t, proj, "config.local.toml", `
journal = true
journal_commit = true
`)
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Projects[0].Journal {
		t.Error("journal opt-in from local layer not applied")
	}
	if !cfg.Projects[0].JournalCommit {
		t.Error("journal_commit from local layer not applied")
	}
}

func TestOverlayMissingLeavesGlobalInlineUntouched(t *testing.T) {
	// No overlay files → project uses whatever the global config declared inline.
	proj := t.TempDir() // exists but has no .claude-manager
	p := writeTOML(t, `
[[project]]
name = "demo"
path = `+`"`+strings.ReplaceAll(proj, `\`, `\\`)+`"`+`
[[project.session]]
name = "inline"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Projects[0].Sessions
	if len(s) != 1 || s[0].Name != "inline" {
		t.Fatalf("sessions = %+v, want inline session preserved", s)
	}
}

func TestSaveProjectOverlaySplitAndGitignore(t *testing.T) {
	proj := t.TempDir()
	p := ProjectConfig{
		Name:             "demo",
		Path:             proj,
		Sessions:         []SessionConfig{{Name: "P1", Model: "sonnet"}},
		Gates:            []string{"go build ./..."},
		MixedProgramming: true,
		MixedMaxRounds:   5,
	}
	workers := []WorkerConfig{{
		Name: "step37", BaseURL: "https://api.kilo.ai/api/gateway",
		Model: "stepfun/step-3.7-flash:free", KeyEnv: "KILO_API_KEY",
	}}
	if err := SaveProjectOverlay(proj, p, workers); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}

	// Committed file must NOT contain the private opt-in or endpoints.
	shared, err := os.ReadFile(ProjectConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "mixed_programming") || strings.Contains(string(shared), "base_url") {
		t.Errorf("committed config leaks private fields:\n%s", shared)
	}
	// Local file carries them.
	local, err := os.ReadFile(ProjectLocalConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "mixed_programming") || !strings.Contains(string(local), "base_url") {
		t.Errorf("local config missing private fields:\n%s", local)
	}
	// .gitignore excludes the local file.
	gi, err := os.ReadFile(filepath.Join(ProjectConfigDir(proj), ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gi), "config.local.toml") {
		t.Errorf(".gitignore missing config.local.toml:\n%s", gi)
	}

	// Round-trip: loading via a global registry reconstructs the merged project.
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	got := cfg.Projects[0]
	if len(got.Sessions) != 1 || got.Sessions[0].Name != "P1" {
		t.Errorf("sessions round-trip mismatch: %+v", got.Sessions)
	}
	if !got.MixedProgramming || got.MixedMaxRounds != 5 || len(got.Gates) != 1 {
		t.Errorf("mixed settings round-trip mismatch: %+v", got)
	}
	if len(cfg.Workers) != 1 {
		t.Errorf("workers round-trip = %d, want 1", len(cfg.Workers))
	}
}

func TestSaveProjectOverlayJournalRoundTrip(t *testing.T) {
	proj := t.TempDir()
	p := ProjectConfig{
		Name:          "demo",
		Path:          proj,
		Journal:       true,
		JournalCommit: true,
	}
	if err := SaveProjectOverlay(proj, p, nil); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}

	shared, err := os.ReadFile(ProjectConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "journal") {
		t.Errorf("committed config leaks the private journal opt-in:\n%s", shared)
	}
	local, err := os.ReadFile(ProjectLocalConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "journal = true") || !strings.Contains(string(local), "journal_commit = true") {
		t.Errorf("local config missing journal fields:\n%s", local)
	}

	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	got := cfg.Projects[0]
	if !got.Journal || !got.JournalCommit {
		t.Errorf("journal settings round-trip mismatch: %+v", got)
	}
}

// TestSaveProjectOverlayAutoSkillsRoundTrip: the skill autopilot opt-in and
// its budget live in the private layer (LEARN-TASKS.md LN-27) and survive a
// save/load; switching it off really switches it off.
func TestSaveProjectOverlayAutoSkillsRoundTrip(t *testing.T) {
	proj := t.TempDir()
	p := ProjectConfig{Name: "demo", Path: proj, AutoSkills: true, AutoSkillsDailyBudgetUSD: 1.5}
	if err := SaveProjectOverlay(proj, p, nil); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}
	shared, err := os.ReadFile(ProjectConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "auto_skills") {
		t.Errorf("committed config leaks the private autopilot opt-in:\n%s", shared)
	}
	cfg, err := Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if got := cfg.Projects[0]; !got.AutoSkills || got.AutoSkillsDailyBudgetUSD != 1.5 {
		t.Errorf("autopilot settings round-trip mismatch: %+v", got)
	}

	p.AutoSkills = false
	if err := SaveProjectOverlay(proj, p, nil); err != nil {
		t.Fatalf("SaveProjectOverlay: %v", err)
	}
	cfg, err = Load(writeGlobalWithProject(t, proj))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.Projects[0].AutoSkills {
		t.Error("AutoSkills still on after saving it off")
	}
}

func TestEnsureGitignoreIdempotent(t *testing.T) {
	proj := t.TempDir()
	p := ProjectConfig{Name: "d", Path: proj}
	for i := 0; i < 3; i++ {
		if err := SaveProjectOverlay(proj, p, nil); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	gi, err := os.ReadFile(filepath.Join(ProjectConfigDir(proj), ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(gi), "config.local.toml"); n != 1 {
		t.Errorf("config.local.toml appears %d times, want 1 (idempotent)", n)
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

// QV-03: the retired stop_when_no_tasks key is migrated on load and never
// written back.
func TestLoad_MigratesStopWhenNoTasks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[[project]]\nname = \"p\"\npath = '" + dir + "'\n\n" +
		"[[project.session]]\nname = \"A\"\nstop_when_no_tasks = true\ntask_source = \"STATUS-P1.md\"\n\n" +
		"[[project.session]]\nname = \"B\"\nauto_restart = true\nstop_when_no_tasks = false\n\n" +
		"[[project.session]]\nname = \"Chat\"\nstop_when_no_tasks = true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := cfg.Projects[0].Sessions[0], cfg.Projects[0].Sessions[1]
	if !a.AutoRestart {
		t.Error("stop_when_no_tasks=true with a task_source must become auto_restart=true")
	}
	if chat := cfg.Projects[0].Sessions[2]; chat.AutoRestart || chat.StopWhenNoTasks {
		t.Errorf("stop_when_no_tasks=true without task_source must stay interactive: %+v", chat)
	}
	if b.RunWithoutTasks || !b.AutoRestart {
		t.Errorf("stop_when_no_tasks=false must not become run_without_tasks: %+v", b)
	}
	if a.StopWhenNoTasks || b.StopWhenNoTasks {
		t.Error("StopWhenNoTasks must be cleared after load")
	}
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "stop_when_no_tasks") {
		t.Errorf("saved config still has stop_when_no_tasks:\n%s", out)
	}
}

// ---- Billing (WS-02) ----

func TestBillingDefaultsToSubscription(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "no-such.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Billing != BillingSubscription || cfg.Settings.LimitWindowTokens != 0 {
		t.Errorf("defaults = %q / %d, want subscription / 0", cfg.Settings.Billing, cfg.Settings.LimitWindowTokens)
	}
	// A config file that predates the key keeps the default too.
	cfg, err = Load(writeTOML(t, "[settings]\ntheme = \"light\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Billing != BillingSubscription {
		t.Errorf("Billing = %q, want subscription when the key is absent", cfg.Settings.Billing)
	}
}

func TestBillingAPIAndWindowRoundTrip(t *testing.T) {
	cfg, err := Load(writeTOML(t, "[settings]\nbilling = \"api\"\nlimit_window_tokens = 5000000\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Billing != BillingAPI || cfg.Settings.LimitWindowTokens != 5_000_000 {
		t.Errorf("got %q / %d", cfg.Settings.Billing, cfg.Settings.LimitWindowTokens)
	}
}

func TestBillingValidation(t *testing.T) {
	if _, err := Load(writeTOML(t, "[settings]\nbilling = \"free\"\n")); err == nil || !strings.Contains(err.Error(), "billing") {
		t.Errorf("unknown billing: err = %v, want a billing error", err)
	}
	if _, err := Load(writeTOML(t, "[settings]\nlimit_window_tokens = -1\n")); err == nil || !strings.Contains(err.Error(), "limit_window_tokens") {
		t.Errorf("negative window: err = %v, want a limit_window_tokens error", err)
	}
}
