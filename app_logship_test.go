package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"claude-manager/internal/buildinfo"
	"claude-manager/internal/config"
)

func TestShipLogs_EmptyRepoClonesNothing(t *testing.T) {
	dir := t.TempDir()
	a := &App{cfg: &config.AppConfig{}, cfgPath: filepath.Join(dir, "config.toml")}
	if _, err := a.ShipLogsNow(); err == nil {
		t.Fatal("expected an error with empty logs_repo")
	}
	if _, err := os.Stat(filepath.Join(dir, "cm-logs")); err == nil {
		t.Fatal("clone created with empty logs_repo")
	}
	if st := a.GetLogShipStatus(); st.Enabled || st.Repo != "" || st.Host == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestShipLogs_BareRepoGetsMachineFile(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	dir := t.TempDir()
	cfg := &config.AppConfig{}
	cfg.Settings.LogsRepo = bare
	a := &App{cfg: cfg, cfgPath: filepath.Join(dir, "config.toml")}
	if _, err := a.ShipLogsNow(); err != nil {
		t.Fatalf("ShipLogsNow: %v", err)
	}
	clone := filepath.Join(dir, "cm-logs", "logs", buildinfo.Host(), "machine.md")
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("machine.md missing: %v", err)
	}
	st := a.GetLogShipStatus()
	if !st.Enabled || st.LastRun == "" || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
}
