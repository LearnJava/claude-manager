package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// A project folder that already carries a committed .claude-manager/config.toml
// (a cloned repo, or a project re-added after being removed from the registry).
func writeExistingOverlay(t *testing.T, proj string) {
	t.Helper()
	dir := config.ProjectConfigDir(proj)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := `gates = ["go build ./..."]

[[session]]
name = "Chat"

[[session]]
name = "Developer 1"
task_source = "STATUS-P1.md"
auto_restart = true
`
	if err := os.WriteFile(config.ProjectConfigPath(proj), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ProjectLocalConfigPath(proj), []byte("journal = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Regression: "Add project" in Settings builds the entry with Sessions: [] and
// saving it used to overwrite the folder's committed config.toml with
// `session = []`, silently deleting every session defined in the repo.
func TestUpdateConfigAddProjectKeepsExistingOverlay(t *testing.T) {
	proj := t.TempDir()
	writeExistingOverlay(t, proj)

	a := &App{cfg: &config.AppConfig{}, cfgPath: filepath.Join(t.TempDir(), "config.toml")}
	incoming := config.AppConfig{Projects: []config.ProjectConfig{
		{Name: "claude-manager", Path: proj, MixedMaxRounds: 3}, // what emptyProject() sends
	}}
	if err := a.UpdateConfig(incoming); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	raw, err := os.ReadFile(config.ProjectConfigPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"Developer 1"`) {
		t.Fatalf("committed config.toml lost its sessions:\n%s", raw)
	}

	p := a.cfg.Projects[0]
	if len(p.Sessions) != 2 || p.Sessions[1].Name != "Developer 1" || p.Sessions[1].TaskSource != "STATUS-P1.md" {
		t.Errorf("sessions after add = %+v, want Chat + Developer 1 from the folder", p.Sessions)
	}
	if len(p.Gates) != 1 {
		t.Errorf("gates = %v, want the folder's gate", p.Gates)
	}
	if !p.Journal {
		t.Error("private-layer journal opt-in was dropped")
	}
}

// A project the app already knows is saved exactly as the UI sends it:
// deleting its last session in Settings must still delete it.
func TestUpdateConfigKnownProjectCanClearSessions(t *testing.T) {
	proj := t.TempDir()
	writeExistingOverlay(t, proj)

	a := &App{
		cfg:     &config.AppConfig{Projects: []config.ProjectConfig{{Name: "claude-manager", Path: proj}}},
		cfgPath: filepath.Join(t.TempDir(), "config.toml"),
	}
	incoming := config.AppConfig{Projects: []config.ProjectConfig{{Name: "claude-manager", Path: proj}}}
	if err := a.UpdateConfig(incoming); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if n := len(a.cfg.Projects[0].Sessions); n != 0 {
		t.Errorf("sessions = %d, want 0 — an explicit clear on a known project must stick", n)
	}
}

// Sessions the user typed in for the new project win over the folder's file.
func TestUpdateConfigAddProjectExplicitSessionsWin(t *testing.T) {
	proj := t.TempDir()
	writeExistingOverlay(t, proj)

	a := &App{cfg: &config.AppConfig{}, cfgPath: filepath.Join(t.TempDir(), "config.toml")}
	incoming := config.AppConfig{Projects: []config.ProjectConfig{{
		Name: "claude-manager", Path: proj,
		Sessions: []config.SessionConfig{{Name: "Solo"}},
	}}}
	if err := a.UpdateConfig(incoming); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	s := a.cfg.Projects[0].Sessions
	if len(s) != 1 || s[0].Name != "Solo" {
		t.Errorf("sessions = %+v, want only the explicit Solo", s)
	}
}
