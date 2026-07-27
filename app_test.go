package main

import (
	"os"
	"path/filepath"
	"testing"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
)

func TestHasClaudeMd(t *testing.T) {
	dir := t.TempDir()
	a := &App{}

	if a.HasClaudeMd(dir) {
		t.Error("HasClaudeMd should be false: no CLAUDE.md written yet")
	}

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.HasClaudeMd(dir) {
		t.Error("HasClaudeMd should be true once CLAUDE.md exists")
	}
}

func TestHasClaudeMd_EmptyPath(t *testing.T) {
	a := &App{}
	if a.HasClaudeMd("") {
		t.Error("HasClaudeMd(\"\") should be false, not stat the cwd")
	}
}

func TestUpsertInitSession_CreatesWhenAbsent(t *testing.T) {
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen"}},
	}
	if !upsertInitSession(cfg, "lumen") {
		t.Fatal("expected project to be found")
	}
	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 || sessions[0].Name != "Init" {
		t.Fatalf("expected one Init session, got %+v", sessions)
	}
	if sessions[0].Prompt != analysis.ClaudeMdInitPrompt {
		t.Error("Init session should carry ClaudeMdInitPrompt")
	}
	if sessions[0].PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q, want bypassPermissions default", sessions[0].PermissionMode)
	}
}

func TestUpsertInitSession_UsesProjectDefaultPermissionMode(t *testing.T) {
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", DefaultPermissionMode: "acceptEdits"}},
	}
	upsertInitSession(cfg, "lumen")
	if got := cfg.Projects[0].Sessions[0].PermissionMode; got != "acceptEdits" {
		t.Errorf("PermissionMode = %q, want acceptEdits (project default)", got)
	}
}

func TestUpsertInitSession_UpdatesPromptOnlyWhenPresent(t *testing.T) {
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{
			Name: "lumen",
			Sessions: []config.SessionConfig{{
				Name:           "Init",
				Model:          "opus", // user's manual override
				Prompt:         "stale prompt",
				PermissionMode: "acceptEdits",
			}},
		}},
	}
	if !upsertInitSession(cfg, "lumen") {
		t.Fatal("expected project to be found")
	}
	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 {
		t.Fatalf("should update in place, not duplicate: got %+v", sessions)
	}
	if sessions[0].Prompt != analysis.ClaudeMdInitPrompt {
		t.Error("Prompt should be refreshed to the current ClaudeMdInitPrompt")
	}
	if sessions[0].Model != "opus" || sessions[0].PermissionMode != "acceptEdits" {
		t.Errorf("user's manual overrides should survive re-upsert, got %+v", sessions[0])
	}
}

func TestUpsertInitSession_ProjectNotFound(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "other"}}}
	if upsertInitSession(cfg, "lumen") {
		t.Error("expected false for an unknown project")
	}
	if len(cfg.Projects[0].Sessions) != 0 {
		t.Error("unrelated project should be untouched")
	}
}

func TestUpsertInitSession_DoesNotMutateCallerSlices(t *testing.T) {
	original := []config.SessionConfig{{Name: "P1"}}
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", Sessions: original}},
	}
	upsertInitSession(cfg, "lumen")
	if len(original) != 1 {
		t.Errorf("upsertInitSession must not mutate the caller's slice in place, got len=%d", len(original))
	}
}

func TestUpsertChatSession_CreatesWhenAbsent(t *testing.T) {
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen"}},
	}
	if !upsertChatSession(cfg, "lumen") {
		t.Fatal("expected project to be found")
	}
	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 || sessions[0].Name != "Chat" {
		t.Fatalf("expected one Chat session, got %+v", sessions)
	}
	if sessions[0].Prompt != "" {
		t.Errorf("Chat session should have no canned prompt, got %q", sessions[0].Prompt)
	}
	if sessions[0].TaskSource != "" || sessions[0].AutoRestart {
		t.Errorf("Chat session must be plain interactive (no task_source/auto_restart), got %+v", sessions[0])
	}
}

func TestUpsertChatSession_LeavesExistingUntouched(t *testing.T) {
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{
			Name: "lumen",
			Sessions: []config.SessionConfig{{
				Name:   "Chat",
				Model:  "opus", // user's manual override
				Prompt: "a note the user added",
			}},
		}},
	}
	if !upsertChatSession(cfg, "lumen") {
		t.Fatal("expected project to be found")
	}
	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 {
		t.Fatalf("should not duplicate the existing Chat session, got %+v", sessions)
	}
	if sessions[0].Model != "opus" || sessions[0].Prompt != "a note the user added" {
		t.Errorf("existing Chat session must survive re-upsert untouched, got %+v", sessions[0])
	}
}

func TestUpsertChatSession_ProjectNotFound(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "other"}}}
	if upsertChatSession(cfg, "lumen") {
		t.Error("expected false for an unknown project")
	}
	if len(cfg.Projects[0].Sessions) != 0 {
		t.Error("unrelated project should be untouched")
	}
}

func TestUpsertChatSession_DoesNotMutateCallerSlices(t *testing.T) {
	original := []config.SessionConfig{{Name: "P1"}}
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", Sessions: original}},
	}
	upsertChatSession(cfg, "lumen")
	if len(original) != 1 {
		t.Errorf("upsertChatSession must not mutate the caller's slice in place, got len=%d", len(original))
	}
}
