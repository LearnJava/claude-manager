package main

import (
	"os"
	"os/exec"
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

// A newly added project gets a "Chat" session: an empty folder gets just
// Chat, a folder whose overlay has other sessions keeps them and gains Chat,
// and a later save of a known project without Chat does not bring it back.
func TestUpdateConfigNewProjectGetsChat(t *testing.T) {
	empty := t.TempDir()
	withDev := t.TempDir()
	if err := os.MkdirAll(config.ProjectConfigDir(withDev), 0o755); err != nil {
		t.Fatal(err)
	}
	dev := "[[session]]\nname = \"Developer 1\"\ntask_source = \"STATUS-P1.md\"\n"
	if err := os.WriteFile(config.ProjectConfigPath(withDev), []byte(dev), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &App{cfg: &config.AppConfig{}, cfgPath: filepath.Join(t.TempDir(), "config.toml")}
	incoming := config.AppConfig{Projects: []config.ProjectConfig{
		{Name: "empty", Path: empty},
		{Name: "dev", Path: withDev},
	}}
	if err := a.UpdateConfig(incoming); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	names := func(p config.ProjectConfig) []string {
		var out []string
		for _, s := range p.Sessions {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(a.cfg.Projects[0]); len(got) != 1 || got[0] != "Chat" {
		t.Errorf("empty project sessions = %v, want [Chat]", got)
	}
	if got := names(a.cfg.Projects[1]); len(got) != 2 || got[0] != "Developer 1" || got[1] != "Chat" {
		t.Errorf("dev project sessions = %v, want [Developer 1 Chat]", got)
	}

	known := config.AppConfig{Projects: []config.ProjectConfig{{Name: "empty", Path: empty}}}
	if err := a.UpdateConfig(known); err != nil {
		t.Fatalf("UpdateConfig known: %v", err)
	}
	if n := len(a.cfg.Projects[0].Sessions); n != 0 {
		t.Errorf("known project re-gained Chat: %v", names(a.cfg.Projects[0]))
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
	if len(s) != 2 || s[0].Name != "Solo" || s[1].Name != "Chat" {
		t.Errorf("sessions = %+v, want the explicit Solo plus Chat", s)
	}
}

// The mirror flag is manager-side: it stays in the global [[project]] pointer
// (never the project folder) and survives a reload, and MirrorProject copies
// the folder's settings into the mirror without touching the folder itself.
func TestMirrorProjectKeepsFlagGlobalAndSyncs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	proj := t.TempDir()
	writeExistingOverlay(t, proj)
	mirrorDir := filepath.Join(t.TempDir(), "projects")

	a := &App{
		cfg: &config.AppConfig{
			Settings: config.GlobalSettings{MirrorDir: mirrorDir},
			Projects: []config.ProjectConfig{{Name: "claude-manager", Path: proj}},
		},
		cfgPath: filepath.Join(t.TempDir(), "config.toml"),
	}
	st, err := a.MirrorProject("claude-manager")
	if err != nil {
		t.Fatalf("MirrorProject: %v", err)
	}
	if !st.Enabled || st.Files == 0 {
		t.Fatalf("status = %+v, want enabled with files", st)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, "files", ".claude-manager", "config.toml")); err != nil {
		t.Errorf("project config.toml not mirrored: %v", err)
	}

	global, err := os.ReadFile(a.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(global), "mirror = true") {
		t.Errorf("global config lost the mirror flag:\n%s", global)
	}
	overlay, _ := os.ReadFile(config.ProjectConfigPath(proj))
	if strings.Contains(string(overlay), "mirror") {
		t.Errorf("mirror flag leaked into the project folder:\n%s", overlay)
	}

	if err := a.UnmirrorProject("claude-manager"); err != nil {
		t.Fatal(err)
	}
	if a.GetProjectMirror("claude-manager").Enabled {
		t.Error("UnmirrorProject left the flag on")
	}
}
