package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/store"
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
	if sessions[0].ContextHandoff {
		t.Error("Chat session must not get the context restart: it would cut the user's conversation")
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

func TestSetSessionModelInConfig_SetsModelAndEffort(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", Model: "sonnet", Effort: "medium"}},
	}}}
	if !setSessionModelInConfig(cfg, "lumen", "P1", "opus", "high") {
		t.Fatal("expected a change to be reported")
	}
	got := cfg.Projects[0].Sessions[0]
	if got.Model != "opus" || got.Effort != "high" {
		t.Errorf("got %+v, want model=opus effort=high", got)
	}
}

func TestSetSessionModelInConfig_EmptyEffortKeepsConfigured(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", Model: "sonnet", Effort: "high"}},
	}}}
	setSessionModelInConfig(cfg, "lumen", "P1", "haiku", "")
	got := cfg.Projects[0].Sessions[0]
	if got.Model != "haiku" || got.Effort != "high" {
		t.Errorf("got %+v: a live model switch must not reset effort", got)
	}
}

// Nothing to write means nothing gets written: rememberSessionModel skips the
// whole config round-trip (and the overlay files it rewrites) in that case.
func TestSetSessionModelInConfig_NoChange(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", Model: "sonnet", Effort: "high"}},
	}}}
	if setSessionModelInConfig(cfg, "lumen", "P1", "sonnet", "high") {
		t.Error("identical model/effort should report no change")
	}
	if setSessionModelInConfig(cfg, "lumen", "P1", "sonnet", "") {
		t.Error("identical model with no effort override should report no change")
	}
}

func TestSetSessionModelInConfig_UnknownProjectOrSession(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", Model: "sonnet"}},
	}}}
	if setSessionModelInConfig(cfg, "other", "P1", "opus", "") {
		t.Error("unknown project should report no change")
	}
	if setSessionModelInConfig(cfg, "lumen", "P9", "opus", "") {
		t.Error("unknown session should report no change")
	}
	if cfg.Projects[0].Sessions[0].Model != "sonnet" {
		t.Error("existing session must be untouched")
	}
}

func TestSetSessionModelInConfig_DoesNotMutateCallerSlices(t *testing.T) {
	original := []config.SessionConfig{{Name: "P1", Model: "sonnet"}}
	cfg := &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", Sessions: original}},
	}
	setSessionModelInConfig(cfg, "lumen", "P1", "opus", "")
	if original[0].Model != "sonnet" {
		t.Errorf("caller's slice mutated in place: %+v", original[0])
	}
}

// TestAddPermissionRuleInConfig_AppendsRule (LEARN-TASKS.md LN-04): the "Add
// rule" button writes into the target session's own PermissionRules — the
// project overlay round-trips this through the exact same
// GetConfig/UpdateConfig path as every other session field, since
// PermissionRules lives on SessionConfig inside the (possibly overlay-backed)
// ProjectConfig.
func TestAddPermissionRuleInConfig_AppendsRule(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1"}},
	}}}
	if !addPermissionRuleInConfig(cfg, "lumen", "P1", "Bash", "go test ./...", "allow") {
		t.Fatal("expected a change to be reported")
	}
	rules := cfg.Projects[0].Sessions[0].PermissionRules
	if len(rules) != 1 || rules[0] != (config.PermissionRule{Tool: "Bash", Pattern: "go test ./...", Decision: "allow"}) {
		t.Fatalf("got rules %+v", rules)
	}
}

func TestAddPermissionRuleInConfig_DuplicateIsNoOp(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name: "lumen",
		Sessions: []config.SessionConfig{{
			Name:            "P1",
			PermissionRules: []config.PermissionRule{{Tool: "Bash", Pattern: "ls", Decision: "allow"}},
		}},
	}}}
	if addPermissionRuleInConfig(cfg, "lumen", "P1", "Bash", "ls", "allow") {
		t.Error("an identical (tool, pattern, decision) triple should report no change")
	}
	if len(cfg.Projects[0].Sessions[0].PermissionRules) != 1 {
		t.Error("duplicate rule must not be appended")
	}
}

func TestAddPermissionRuleInConfig_UnknownProjectOrSession(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1"}},
	}}}
	if addPermissionRuleInConfig(cfg, "other", "P1", "Bash", "ls", "allow") {
		t.Error("unknown project should report no change")
	}
	if addPermissionRuleInConfig(cfg, "lumen", "P9", "Bash", "ls", "allow") {
		t.Error("unknown session should report no change")
	}
	if len(cfg.Projects[0].Sessions[0].PermissionRules) != 0 {
		t.Error("existing session must be untouched")
	}
}

func TestAddPermissionRuleInConfig_DoesNotMutateCallerSlices(t *testing.T) {
	original := []config.PermissionRule{{Tool: "Bash", Pattern: "ls", Decision: "allow"}}
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", PermissionRules: original}},
	}}}
	addPermissionRuleInConfig(cfg, "lumen", "P1", "Bash", "go test ./...", "allow")
	if len(original) != 1 {
		t.Errorf("addPermissionRuleInConfig must not mutate the caller's slice in place, got len=%d", len(original))
	}
}

func TestSplitSessionID(t *testing.T) {
	cases := []struct {
		id            string
		project, name string
		ok            bool
	}{
		{"lumen/P1", "lumen", "P1", true},
		{"lumen/sub/P1", "lumen", "sub/P1", true}, // session names may contain slashes
		{"lumen", "", "", false},
		{"/P1", "", "", false},
		{"lumen/", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		project, name, ok := splitSessionID(c.id)
		if ok != c.ok || project != c.project || name != c.name {
			t.Errorf("splitSessionID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.id, project, name, ok, c.project, c.name, c.ok)
		}
	}
}

// rememberSessionModel is a no-op without a loaded config or with an empty
// model — it must never write a session's model away to "".
func TestRememberSessionModel_NoConfigOrEmptyModel(t *testing.T) {
	(&App{}).rememberSessionModel("lumen", "P1", "opus", "")

	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name:     "lumen",
		Sessions: []config.SessionConfig{{Name: "P1", Model: "sonnet"}},
	}}}
	a := &App{cfg: cfg}
	a.rememberSessionModel("lumen", "P1", "  ", "")
	if cfg.Projects[0].Sessions[0].Model != "sonnet" {
		t.Error("an empty model must not overwrite the configured one")
	}
}

func TestProjectPath_Found(t *testing.T) {
	a := &App{cfg: &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", Path: "/repo/lumen"}},
	}}
	path, err := a.projectPath("lumen")
	if err != nil {
		t.Fatalf("projectPath: %v", err)
	}
	if path != "/repo/lumen" {
		t.Errorf("projectPath: got %q want %q", path, "/repo/lumen")
	}
}

func TestProjectPath_NotFound(t *testing.T) {
	a := &App{cfg: &config.AppConfig{Projects: []config.ProjectConfig{{Name: "other"}}}}
	if _, err := a.projectPath("lumen"); err == nil {
		t.Error("expected error for unknown project")
	}
}

func TestProjectPath_NoFolderConfigured(t *testing.T) {
	a := &App{cfg: &config.AppConfig{Projects: []config.ProjectConfig{{Name: "lumen"}}}}
	if _, err := a.projectPath("lumen"); err == nil {
		t.Error("expected error when project has no Path set")
	}
}

func TestProjectPath_NoConfigLoaded(t *testing.T) {
	a := &App{}
	if _, err := a.projectPath("lumen"); err == nil {
		t.Error("expected error when no config is loaded")
	}
}

func TestGetProjectLogFiles(t *testing.T) {
	dir := t.TempDir()
	a := &App{cfg: &config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "lumen", Path: dir}},
	}}

	if _, err := store.SaveSessionLogFile(dir, "lumen/S1", []store.LogEntry{
		{Message: "hi", Level: "text"},
	}); err != nil {
		t.Fatalf("SaveSessionLogFile: %v", err)
	}

	files, err := a.GetProjectLogFiles("lumen")
	if err != nil {
		t.Fatalf("GetProjectLogFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(files))
	}
}

func TestGetProjectLogFiles_UnknownProject(t *testing.T) {
	a := &App{cfg: &config.AppConfig{}}
	if _, err := a.GetProjectLogFiles("lumen"); err == nil {
		t.Error("expected error for unknown project")
	}
}

func TestClearProjectLogs_RemovesFilesAndDBRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{
		cfg: &config.AppConfig{
			Projects: []config.ProjectConfig{{Name: "lumen", Path: dir}},
		},
		store: st,
	}

	if _, err := store.SaveSessionLogFile(dir, "lumen/S1", []store.LogEntry{
		{Message: "hi", Level: "text"},
	}); err != nil {
		t.Fatalf("SaveSessionLogFile: %v", err)
	}

	run := &store.SessionRun{Project: "lumen", Session: "S1", Model: "sonnet", Status: "completed"}
	if err := st.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if err := st.InsertLogs(run.ID, []store.LogEntry{{Level: "text", Message: "db row"}}); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	if err := a.ClearProjectLogs("lumen"); err != nil {
		t.Fatalf("ClearProjectLogs: %v", err)
	}

	files, err := a.GetProjectLogFiles("lumen")
	if err != nil {
		t.Fatalf("GetProjectLogFiles after clear: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files after clear, got %d", len(files))
	}

	logs, err := st.GetLogs(run.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetLogs after clear: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 db log rows after clear, got %d", len(logs))
	}

	if runAfter, err := st.GetRun(run.ID); err != nil || runAfter == nil {
		t.Errorf("expected session_runs row to survive ClearProjectLogs, err=%v", err)
	}
}

// ---- Roadmap panel bindings ----

func newRoadmapApp(t *testing.T, taskSource string) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	plan := &analysis.TaskPlan{
		Project: "lumen",
		Subtasks: []analysis.PlannedSubtask{
			{ID: "setup", Name: "scaffolding", Summary: "bootstrap the module", Prompt: "Create go.mod."},
			{ID: "store", Name: "storage", Summary: "sqlite layer", Prompt: "Port the store.", DependsOn: []string{"setup"}},
		},
		ExecutionOrder: [][]string{{"setup"}, {"store"}},
	}
	if _, _, err := analysis.WriteRoadmapFiles(dir, plan, false); err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}
	a := &App{cfg: &config.AppConfig{
		Projects: []config.ProjectConfig{{
			Name:     "lumen",
			Path:     dir,
			Sessions: []config.SessionConfig{{Name: "P1", TaskSource: taskSource}},
		}},
	}}
	return a, dir
}

func TestGetSessionRoadmap(t *testing.T) {
	a, _ := newRoadmapApp(t, "STATUS-P1.md")
	view, err := a.GetSessionRoadmap("lumen", "P1")
	if err != nil {
		t.Fatalf("GetSessionRoadmap: %v", err)
	}
	if view == nil {
		t.Fatal("expected a roadmap view")
	}
	if view.Total != 2 || view.Done != 0 {
		t.Errorf("counts: %d done of %d", view.Done, view.Total)
	}
	if view.Nodes[0].Status != analysis.RoadmapTaskActive || !view.Nodes[0].Current {
		t.Errorf("first task: status %q current=%v", view.Nodes[0].Status, view.Nodes[0].Current)
	}
}

func TestGetSessionRoadmap_NoTaskSourceIsNotAnError(t *testing.T) {
	// A Chat/Init session has no task source; the panel shows a placeholder
	// rather than an error banner.
	a, _ := newRoadmapApp(t, "")
	view, err := a.GetSessionRoadmap("lumen", "P1")
	if err != nil {
		t.Fatalf("GetSessionRoadmap: %v", err)
	}
	if view != nil {
		t.Errorf("expected nil view, got %+v", view)
	}
}

func TestGetSessionRoadmap_UnknownProject(t *testing.T) {
	a, _ := newRoadmapApp(t, "STATUS-P1.md")
	if _, err := a.GetSessionRoadmap("nope", "P1"); err == nil {
		t.Error("expected an error for an unknown project")
	}
}

func TestGetRoadmapTaskDetail(t *testing.T) {
	a, _ := newRoadmapApp(t, "STATUS-P1.md")
	body, err := a.GetRoadmapTaskDetail("lumen", "tasks/01-setup.md")
	if err != nil {
		t.Fatalf("GetRoadmapTaskDetail: %v", err)
	}
	if !strings.Contains(body, "Create go.mod.") {
		t.Errorf("unexpected detail body:\n%s", body)
	}
	if _, err := a.GetRoadmapTaskDetail("lumen", "../outside.md"); err == nil {
		t.Error("path escaping the project must be rejected")
	}
}

func TestSessionTaskSource(t *testing.T) {
	a, _ := newRoadmapApp(t, "STATUS-P1.md")
	if got := a.sessionTaskSource("lumen", "P1"); got != "STATUS-P1.md" {
		t.Errorf("got %q", got)
	}
	if got := a.sessionTaskSource("lumen", "Chat"); got != "" {
		t.Errorf("unknown session should return empty, got %q", got)
	}
	if got := a.sessionTaskSource("nope", "P1"); got != "" {
		t.Errorf("unknown project should return empty, got %q", got)
	}
	if got := (&App{}).sessionTaskSource("lumen", "P1"); got != "" {
		t.Errorf("no config should return empty, got %q", got)
	}
}

func TestGetRoadmapRowDetail(t *testing.T) {
	a, _ := newRoadmapApp(t, "STATUS-P1.md")
	view, err := a.GetSessionRoadmap("lumen", "P1")
	if err != nil || view == nil {
		t.Fatalf("GetSessionRoadmap: %v", err)
	}
	body, err := a.GetRoadmapRowDetail("lumen", view.RoadmapFile, view.Nodes[0].Line)
	if err != nil {
		t.Fatalf("GetRoadmapRowDetail: %v", err)
	}
	if !strings.Contains(body, "scaffolding") {
		t.Errorf("unexpected row detail: %q", body)
	}
	if _, err := a.GetRoadmapRowDetail("lumen", "../outside.md", 1); err == nil {
		t.Error("path escaping the project must be rejected")
	}
}

// TestUpsertP1Session_UsesPermanentSlot: use_worktree on a P1 session now
// prepares the permanent slot (p1-work) the installed protocol occupies; the CLI
// no longer gets --worktree, so no anonymous per-start copy appears.
func TestUpsertP1Session_UsesPermanentSlot(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "lumen"}}}
	upsertP1Session(cfg, "lumen")

	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 || sessions[0].Name != "P1" {
		t.Fatalf("expected one P1 session, got %+v", sessions)
	}
	if !sessions[0].UseWorktree {
		t.Error("P1 must have use_worktree = true (permanent slot)")
	}
	if sessions[0].TaskSource != "STATUS-P1.md" || !sessions[0].AutoRestart {
		t.Errorf("P1 queue wiring not set: %+v", sessions[0])
	}
	if sessions[0].Prompt != analysis.DefaultP1SessionPrompt {
		t.Error("P1 should carry DefaultP1SessionPrompt")
	}
}

// An existing P1 gets use_worktree forced on; the user's own model/prompt edits
// survive.
func TestUpsertP1Session_EnablesSlotOnExisting(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name: "lumen",
		Sessions: []config.SessionConfig{{
			Name:   "P1",
			Model:  "opus",
			Prompt: "my own prompt",
		}},
	}}}
	upsertP1Session(cfg, "lumen")

	got := cfg.Projects[0].Sessions[0]
	if !got.UseWorktree {
		t.Error("use_worktree was not enabled")
	}
	if got.Model != "opus" || got.Prompt != "my own prompt" {
		t.Errorf("manual edits were overwritten: %+v", got)
	}
	if got.TaskSource != "STATUS-P1.md" {
		t.Errorf("TaskSource = %q, want STATUS-P1.md", got.TaskSource)
	}
}

// TestUpdateConfig_AddingAlreadyConfiguredProjectPreservesOverlay reproduces
// the bug where registering a project by path — one whose folder already has
// its own <project>/.claude-manager/config.toml with real sessions, from
// being configured before this app instance ever knew about it — wiped that
// file: UpdateConfig unconditionally wrote the new, blank ProjectConfig stub
// over whatever was already on disk. Adding such a project must leave its
// existing overlay untouched.
func TestUpdateConfig_AddingAlreadyConfiguredProjectPreservesOverlay(t *testing.T) {
	dir := t.TempDir()
	existing := config.ProjectConfig{
		Sessions: []config.SessionConfig{{Name: "S1", Model: "sonnet"}},
		Gates:    []string{"go build ./..."},
	}
	if err := config.SaveProjectOverlay(dir, existing, []config.WorkerConfig{{Name: "w1", BaseURL: "http://localhost:1234", Model: "m", KeyEnv: "W1_KEY"}}); err != nil {
		t.Fatalf("seed SaveProjectOverlay: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	a := &App{cfg: &config.AppConfig{}, cfgPath: cfgPath}

	// The frontend adds the project by name+path only — nothing loaded from
	// its folder yet.
	newCfg := config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "existing", Path: dir}},
	}
	if err := a.UpdateConfig(newCfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	ov, err := config.LoadProjectOverlay(dir)
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	// Chat is appended to every newly added project.
	if ov == nil || len(ov.Sessions) != 2 || ov.Sessions[0].Name != "S1" || ov.Sessions[1].Name != "Chat" {
		t.Fatalf("sessions were wiped: %+v", ov)
	}
	if len(ov.Gates) != 1 || ov.Gates[0] != "go build ./..." {
		t.Fatalf("gates were wiped: %+v", ov)
	}
	if len(ov.Workers) != 1 || ov.Workers[0].Name != "w1" {
		t.Fatalf("local workers were wiped: %+v", ov)
	}

	// The reloaded in-memory config must also reflect the preserved overlay,
	// not a blank project.
	if len(a.cfg.Projects) != 1 || len(a.cfg.Projects[0].Sessions) != 2 {
		t.Fatalf("reloaded config missing sessions: %+v", a.cfg.Projects)
	}
}

// TestUpdateConfig_KnownProjectCanStillClearSessions ensures the fix above
// only special-cases a brand-new registration: editing an already-known
// project (round-tripped through GetConfig, where the frontend's data is
// authoritative) must still be able to remove sessions.
func TestUpdateConfig_KnownProjectCanStillClearSessions(t *testing.T) {
	dir := t.TempDir()
	seeded := config.ProjectConfig{Sessions: []config.SessionConfig{{Name: "S1"}}}
	if err := config.SaveProjectOverlay(dir, seeded, nil); err != nil {
		t.Fatalf("seed SaveProjectOverlay: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	a := &App{
		cfg:     &config.AppConfig{Projects: []config.ProjectConfig{{Name: "existing", Path: dir, Sessions: seeded.Sessions}}},
		cfgPath: cfgPath,
	}

	newCfg := config.AppConfig{
		Projects: []config.ProjectConfig{{Name: "existing", Path: dir}}, // sessions cleared
	}
	if err := a.UpdateConfig(newCfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	ov, err := config.LoadProjectOverlay(dir)
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if ov != nil && len(ov.Sessions) != 0 {
		t.Fatalf("expected sessions to be clearable on a known project, got: %+v", ov.Sessions)
	}
}

func TestSetSessionRuntimeInConfig(t *testing.T) {
	original := []config.SessionConfig{{Name: "P1"}}
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "lumen", Sessions: original}}}

	changed, found := setSessionRuntimeInConfig(cfg, "lumen", "P1", config.RuntimeHermes)
	if !changed || !found {
		t.Fatalf("changed=%v found=%v, want true/true", changed, found)
	}
	if cfg.Projects[0].Sessions[0].Runtime != config.RuntimeHermes {
		t.Errorf("runtime = %q, want hermes", cfg.Projects[0].Sessions[0].Runtime)
	}
	if original[0].Runtime != "" {
		t.Error("caller's slice mutated in place")
	}
	if changed, _ := setSessionRuntimeInConfig(cfg, "lumen", "P1", config.RuntimeHermes); changed {
		t.Error("identical runtime should report no change")
	}
	if _, found := setSessionRuntimeInConfig(cfg, "lumen", "P9", ""); found {
		t.Error("unknown session should not be found")
	}

	// "claude" spelled out equals the empty default.
	cfg.Projects[0].Sessions[0].Runtime = "claude"
	if changed, _ := setSessionRuntimeInConfig(cfg, "lumen", "P1", ""); changed {
		t.Error(`"claude" and "" are the same runtime`)
	}
}

func TestGetAllSessions_NilManager(t *testing.T) {
	a := &App{}
	if got := a.GetAllSessions(); got != nil {
		t.Fatalf("want nil before startup, got %v", got)
	}
}

func TestWindowOffScreen(t *testing.T) {
	cases := []struct {
		x, y int
		want bool
	}{{-32000, -32000, true}, {0, 0, false}, {-8, -8, false}, {100, -32000, true}, {1920, 50, false}}
	for _, c := range cases {
		if got := windowOffScreen(c.x, c.y); got != c.want {
			t.Errorf("windowOffScreen(%d,%d)=%v want %v", c.x, c.y, got, c.want)
		}
	}
}
