package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/session"
)

// TestRealClaude_GenerateClaudeMdSession drives the exact code path the
// Sidebar's "Generate" button calls (App.HasClaudeMd, App.GenerateClaudeMdSession)
// against a real project directory with the real Claude CLI, and dumps the
// resulting CLAUDE.md. It spends real API budget, so it is gated behind
// CM_REAL_CLAUDE=1 and skipped by default, mirroring
// internal/session/session_realclaude_test.go.
//
//	CM_REAL_CLAUDE=1 CM_REAL_CLAUDE_PROJECT=D:/GoProjects/brewtimer-demo \
//	  go test . -run TestRealClaude_GenerateClaudeMdSession -v -timeout 5m
func TestRealClaude_GenerateClaudeMdSession(t *testing.T) {
	if os.Getenv("CM_REAL_CLAUDE") != "1" {
		t.Skip("set CM_REAL_CLAUDE=1 to run the real-CLI integration test")
	}
	projectPath := os.Getenv("CM_REAL_CLAUDE_PROJECT")
	if projectPath == "" {
		t.Skip("set CM_REAL_CLAUDE_PROJECT to a real project directory with no CLAUDE.md")
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.AppConfig{
		Settings: config.GlobalSettings{ClaudePath: "claude"},
		Projects: []config.ProjectConfig{{Name: "demo", Path: projectPath}},
	}

	ce := control.NewControlEmitter(500)
	mgr := session.NewSessionManager(cfg, cfgPath, nil, ce)
	a := &App{cfg: cfg, cfgPath: cfgPath, manager: mgr}

	if a.HasClaudeMd(projectPath) {
		t.Fatalf("test project %s already has a CLAUDE.md — pick a clean directory", projectPath)
	}

	if err := a.GenerateClaudeMdSession("demo"); err != nil {
		t.Fatalf("GenerateClaudeMdSession: %v", err)
	}

	// "Init" is a plain interactive session (no auto_restart/task_source), so
	// like any manually-started session it keeps stdin open and stays
	// "working" after its result — it does not settle to "idle" on its own.
	// Wait for the turn's result (NumTurns/cost populated) instead, then stop
	// it explicitly, exactly like the user clicking Stop would.
	deadline := time.Now().Add(4 * time.Minute)
	var st session.SessionState
	for time.Now().Before(deadline) {
		var ok bool
		st, ok = mgr.GetSession("demo/Init")
		if ok && st.NumTurns > 0 {
			break
		}
		if st.Status == "error" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("Init session turn finished: status=%s cost=$%.4f turns=%d", st.Status, st.TotalCostUSD, st.NumTurns)
	if st.NumTurns == 0 {
		t.Fatalf("Init session produced no result in time (status=%s)", st.Status)
	}

	if !a.HasClaudeMd(projectPath) {
		t.Fatal("CLAUDE.md was not created")
	}
	data, err := os.ReadFile(filepath.Join(projectPath, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Generated CLAUDE.md (%d bytes):\n%s", len(data), data)

	_ = mgr.StopSession("demo/Init", false)
	mgr.Shutdown()
}
