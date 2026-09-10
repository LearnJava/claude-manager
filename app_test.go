package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/experience"
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

func TestGetTopActions_NoStore(t *testing.T) {
	a := &App{}
	if _, err := a.GetTopActions("lumen", 30); err == nil {
		t.Error("expected error with no store opened")
	}
}

func TestGetActionSamples_NoStore(t *testing.T) {
	a := &App{}
	if _, err := a.GetActionSamples("lumen", "Bash:git status", 0); err == nil {
		t.Error("expected error with no store opened")
	}
}

func TestGetSkillCandidates_NoStore(t *testing.T) {
	a := &App{}
	if _, err := a.GetSkillCandidates("lumen"); err == nil {
		t.Error("expected error with no store opened")
	}
}

// TestGetSkillCandidates_MinesRecurringSequence is the App-level wiring test
// for GetSkillCandidates: with experience.MineProjectCandidates tested in
// depth in internal/experience, this only needs to check the Wails entry
// point actually reaches the store and returns what was mined — the one
// thing missing before this task, per CLAUDE.md's "Experience Layer" section
// (LEARN-TASKS.md LN-08's MineCandidates was never wired to anything the UI
// could call).
func TestGetSkillCandidates_MinesRecurringSequence(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}
	now := time.Now().UTC()

	var rows []store.ActionRow
	for i := 0; i < 3; i++ {
		r := &store.SessionRun{Project: "lumen", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
		if err := st.InsertRun(r); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
		rows = append(rows,
			store.ActionRow{Project: "lumen", Session: "S1", RunID: &r.ID, StepIndex: 0, Tool: "Bash",
				Sig: "Bash:git status", Arg: "git status", Timestamp: now},
			store.ActionRow{Project: "lumen", Session: "S1", RunID: &r.ID, StepIndex: 1, Tool: "Bash",
				Sig: "Bash:git add <ARG>", Arg: "git add -A", Timestamp: now},
		)
	}
	if err := st.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	cands, err := a.GetSkillCandidates("lumen")
	if err != nil {
		t.Fatalf("GetSkillCandidates: %v", err)
	}
	found := false
	for _, c := range cands {
		if len(c.Sig) == 2 && c.Sig[0] == "Bash:git status" && c.Sig[1] == "Bash:git add <ARG>" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the recurring 2-gram among candidates, got %+v", cands)
	}
}

// TestGetTopActions_IncludesBulkImportedRows: a row with RunID == nil (from
// IngestDir's bulk import, LEARN-TASKS.md LN-17) must still show up in
// GetTopActions — the "Actions" tab must not silently drop imported history
// (LEARN-TASKS.md LN-03 "Готово когда").
func TestGetTopActions_IncludesBulkImportedRows(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}

	if err := st.InsertActions([]store.ActionRow{
		{Project: "lumen", Session: "S1", CLISessionID: "old-log.md", Tool: "Bash",
			Sig: "Bash:git status", Arg: "git status --short", Timestamp: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	stats, err := a.GetTopActions("lumen", 30)
	if err != nil {
		t.Fatalf("GetTopActions: %v", err)
	}
	if len(stats) != 1 || stats[0].Count != 1 {
		t.Fatalf("GetTopActions = %+v, want one row with Count 1", stats)
	}

	samples, err := a.GetActionSamples("lumen", "Bash:git status", 0)
	if err != nil {
		t.Fatalf("GetActionSamples: %v", err)
	}
	if len(samples) != 1 || samples[0].Arg != "git status --short" {
		t.Fatalf("GetActionSamples = %+v, want the one bulk-imported row", samples)
	}
}

// TestProjectStartOrder_TrackingOff: with experience_tracking off (the
// default), projectStartOrder must return nil regardless of a configured
// store, so StartProject falls back to plain SessionManager.StartProject
// unchanged (LEARN-TASKS.md LN-14, invariant 6).
func TestProjectStartOrder_TrackingOff(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{
		store: st,
		cfg: &config.AppConfig{
			Projects: []config.ProjectConfig{{Name: "lumen", Sessions: []config.SessionConfig{
				{Name: "S1", Model: "sonnet"}, {Name: "S2", Model: "sonnet"},
			}}},
		},
	}
	if got := a.projectStartOrder("lumen"); got != nil {
		t.Fatalf("want nil with tracking off, got %v", got)
	}
}

// TestProjectStartOrder_NoStore mirrors TrackingOff for the other half of
// the gate: tracking on but no store configured (e.g. cmd/playwright-server).
func TestProjectStartOrder_NoStore(t *testing.T) {
	a := &App{
		cfg: &config.AppConfig{
			Optimization: config.OptimizationSettings{ExperienceTracking: true},
			Projects: []config.ProjectConfig{{Name: "lumen", Sessions: []config.SessionConfig{
				{Name: "S1", Model: "sonnet"},
			}}},
		},
	}
	if got := a.projectStartOrder("lumen"); got != nil {
		t.Fatalf("want nil with no store, got %v", got)
	}
}

// TestProjectStartOrder_NoHistoryPreservesConfigOrder: tracking on, store
// configured, but no run history for any session yet — order must match
// plain config order byte-for-byte (LEARN-TASKS.md invariant 6).
func TestProjectStartOrder_NoHistoryPreservesConfigOrder(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{
		store: st,
		cfg: &config.AppConfig{
			Optimization: config.OptimizationSettings{ExperienceTracking: true},
			Projects: []config.ProjectConfig{{Name: "lumen", Sessions: []config.SessionConfig{
				{Name: "S1", Model: "sonnet"}, {Name: "S2", Model: "sonnet"}, {Name: "S3", Model: "sonnet"},
			}}},
		},
	}
	got := a.projectStartOrder("lumen")
	want := []string{"S1", "S2", "S3"}
	if len(got) != len(want) {
		t.Fatalf("projectStartOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("projectStartOrder = %v, want %v", got, want)
		}
	}
}

// TestProjectStartOrder_GroupsByModelUsingHistory: with run history recorded
// for each session, the order groups by model (sonnet before haiku, matching
// first appearance in config) and orders the sonnet group by descending
// file overlap with S1.
func TestProjectStartOrder_GroupsByModelUsingHistory(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	seedRun := func(session string, files []string) {
		run := &store.SessionRun{Project: "lumen", Session: session, Model: "sonnet", StartedAt: time.Now().UTC(), Status: "completed"}
		if err := st.InsertRun(run); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
		var rows []store.ActionRow
		for i, f := range files {
			rows = append(rows, store.ActionRow{
				Project: "lumen", Session: session, RunID: &run.ID, StepIndex: i,
				Tool: "Edit", Sig: "Edit:*.go", Arg: f, Timestamp: time.Now().UTC(),
			})
		}
		if err := st.InsertActions(rows); err != nil {
			t.Fatalf("InsertActions: %v", err)
		}
	}
	seedRun("S1", []string{"a.go", "b.go"})
	seedRun("S2", []string{"a.go", "b.go", "c.go"}) // heavy overlap with S1
	seedRun("S3", []string{"z.go"})                 // no overlap with S1

	a := &App{
		store: st,
		cfg: &config.AppConfig{
			Optimization: config.OptimizationSettings{ExperienceTracking: true},
			Projects: []config.ProjectConfig{{Name: "lumen", Sessions: []config.SessionConfig{
				{Name: "S1", Model: "sonnet"},
				{Name: "S3", Model: "sonnet"},
				{Name: "Haiku", Model: "haiku"},
				{Name: "S2", Model: "sonnet"},
			}}},
		},
	}
	got := a.projectStartOrder("lumen")
	want := []string{"S1", "S2", "S3", "Haiku"}
	if len(got) != len(want) {
		t.Fatalf("projectStartOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("projectStartOrder = %v, want %v", got, want)
		}
	}
}

func TestProjectStartOrder_UnknownProject(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{
		store: st,
		cfg: &config.AppConfig{
			Optimization: config.OptimizationSettings{ExperienceTracking: true},
			Projects:     []config.ProjectConfig{{Name: "lumen", Sessions: []config.SessionConfig{{Name: "S1"}}}},
		},
	}
	if got := a.projectStartOrder("other"); got != nil {
		t.Fatalf("want nil for unknown project, got %v", got)
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

// ---- Skills tab bindings (LEARN-TASKS.md LN-10) ----

func newSkillApp(t *testing.T) (*App, *store.Store, string) {
	t.Helper()
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
	return a, st, dir
}

func insertDraftSkill(t *testing.T, st *store.Store, name string) *store.Skill {
	t.Helper()
	sk := &store.Skill{
		Project:    "lumen",
		Name:       name,
		Status:     "draft",
		DraftJSON:  `{"name":"` + name + `"}`,
		MD:         "---\nname: " + name + "\n---\ndraft body",
		SourceJSON: `["Bash:git status"]`,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}
	return sk
}

func TestGetSkills(t *testing.T) {
	a, st, _ := newSkillApp(t)
	insertDraftSkill(t, st, "git-session-preamble")

	got, err := a.GetSkills("lumen")
	if err != nil {
		t.Fatalf("GetSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "git-session-preamble" {
		t.Fatalf("GetSkills = %+v, want one row named git-session-preamble", got)
	}
}

// TestApproveSkill_WritesFileAndMarksApproved is the LN-10 "Готово когда"
// happy path: accept a draft → the file lands under
// <project>/.claude/skills/<name>/SKILL.md → the row reflects approved.
func TestApproveSkill_WritesFileAndMarksApproved(t *testing.T) {
	a, st, dir := newSkillApp(t)
	sk := insertDraftSkill(t, st, "git-session-preamble")

	path, err := a.ApproveSkill(sk.ID, "edited body", false)
	if err != nil {
		t.Fatalf("ApproveSkill: %v", err)
	}
	want := filepath.Join(dir, ".claude", "skills", "git-session-preamble", "SKILL.md")
	if wantAbs, _ := filepath.Abs(want); path != wantAbs {
		t.Errorf("path = %q, want %q", path, wantAbs)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "edited body" {
		t.Errorf("file content = %q, want the edited body", data)
	}

	got, err := st.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.ApprovedAt == nil {
		t.Error("ApprovedAt is nil after approval")
	}
}

// TestApproveSkill_SecondApprovalNeedsOverwrite: re-approving the same skill
// without overwrite=true must fail and leave the file untouched — the
// caller's cue to show the inline "already exists — overwrite?" banner.
func TestApproveSkill_SecondApprovalNeedsOverwrite(t *testing.T) {
	a, st, _ := newSkillApp(t)
	sk := insertDraftSkill(t, st, "git-session-preamble")

	if _, err := a.ApproveSkill(sk.ID, "v1", false); err != nil {
		t.Fatalf("first ApproveSkill: %v", err)
	}
	if _, err := a.ApproveSkill(sk.ID, "v2", false); !errors.Is(err, experience.ErrSkillFileExists) {
		t.Fatalf("second ApproveSkill: err = %v, want ErrSkillFileExists", err)
	}
	if _, err := a.ApproveSkill(sk.ID, "v2", true); err != nil {
		t.Fatalf("ApproveSkill with overwrite=true: %v", err)
	}
}

// TestApproveSkill_RejectsUnsafeName is the LN-10 unit test the task calls
// out explicitly: a row whose Name is not [a-z0-9-] (however it got that
// way) must never be written to disk.
func TestApproveSkill_RejectsUnsafeName(t *testing.T) {
	a, st, _ := newSkillApp(t)
	sk := insertDraftSkill(t, st, "../evil")

	if _, err := a.ApproveSkill(sk.ID, "body", false); err == nil {
		t.Fatal("ApproveSkill with unsafe name succeeded, want error")
	}

	got, err := st.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("Status = %q, want draft (approval must not have taken effect)", got.Status)
	}
}

func TestApproveSkill_UnknownID(t *testing.T) {
	a, _, _ := newSkillApp(t)
	if _, err := a.ApproveSkill(999, "body", false); err == nil {
		t.Fatal("ApproveSkill(unknown id) succeeded, want error")
	}
}

// TestArchiveSkill_RemovesFromActiveListButKeepsAnyWrittenFile: archiving is
// metadata-only — it must not touch a file already approved onto disk.
func TestArchiveSkill_RemovesFromActiveListButKeepsAnyWrittenFile(t *testing.T) {
	a, st, _ := newSkillApp(t)
	sk := insertDraftSkill(t, st, "git-session-preamble")

	path, err := a.ApproveSkill(sk.ID, "approved body", false)
	if err != nil {
		t.Fatalf("ApproveSkill: %v", err)
	}

	if err := a.ArchiveSkill(sk.ID); err != nil {
		t.Fatalf("ArchiveSkill: %v", err)
	}

	got, err := st.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Status != "archived" {
		t.Errorf("Status = %q, want archived", got.Status)
	}
	if got.ArchivedAt == nil {
		t.Error("ArchivedAt is nil after archiving")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("archiving removed the approved file: %v", err)
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

// TestUpsertP1Session_DoesNotUseManagerWorktree is the guard on the change that
// made an interrupted session recoverable at all. With the manager's own
// --worktree on, every process start gets a fresh anonymous worktree cut from
// HEAD, so an interrupted task's branch is invisible to the next run and the
// task is silently implemented again. The protocol written into the project
// owns the worktree instead (one persistent slot per developer).
func TestUpsertP1Session_DoesNotUseManagerWorktree(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "lumen"}}}
	upsertP1Session(cfg, "lumen")

	sessions := cfg.Projects[0].Sessions
	if len(sessions) != 1 || sessions[0].Name != "P1" {
		t.Fatalf("expected one P1 session, got %+v", sessions)
	}
	if sessions[0].UseWorktree {
		t.Error("P1 must not use the manager's --worktree: the project's protocol owns the worktree")
	}
	if sessions[0].TaskSource != "STATUS-P1.md" || !sessions[0].AutoRestart || !sessions[0].StopWhenNoTasks {
		t.Errorf("P1 queue wiring not set: %+v", sessions[0])
	}
	if sessions[0].Prompt != analysis.DefaultP1SessionPrompt {
		t.Error("P1 should carry DefaultP1SessionPrompt")
	}
}

// A P1 session left over from before this change carries use_worktree = true;
// re-approving a roadmap has to clear it, or the project keeps the exact
// behaviour the protocol exists to prevent. The user's own model/prompt edits
// still survive.
func TestUpsertP1Session_ClearsStaleWorktreeFlagOnExisting(t *testing.T) {
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{
		Name: "lumen",
		Sessions: []config.SessionConfig{{
			Name:        "P1",
			Model:       "opus",
			Prompt:      "my own prompt",
			UseWorktree: true,
		}},
	}}}
	upsertP1Session(cfg, "lumen")

	got := cfg.Projects[0].Sessions[0]
	if got.UseWorktree {
		t.Error("stale use_worktree = true was not cleared")
	}
	if got.Model != "opus" || got.Prompt != "my own prompt" {
		t.Errorf("manual edits were overwritten: %+v", got)
	}
	if got.TaskSource != "STATUS-P1.md" {
		t.Errorf("TaskSource = %q, want STATUS-P1.md", got.TaskSource)
	}
}

func TestSkillDistillInputFromCandidate(t *testing.T) {
	cand := experience.SkillCandidate{
		Sig: []string{"Bash:git status", "Bash:git branch -a"},
		Samples: []store.ActionRow{
			{Arg: "git status --short --branch"},
			{Arg: "git branch -a"},
		},
		RelatedFailures: []experience.FailureCluster{
			{
				ErrorKey: "fatal: not a git repository",
				Examples: []experience.FixPair{
					{FailedArg: "git status", FixedArg: "cd repo && git status"},
					{FailedArg: "git status (again)", FixedArg: "cd repo && git status (again)"},
				},
			},
			{ErrorKey: "empty cluster, no examples"}, // must not panic / must be skipped
		},
	}

	in, sourceJSON, err := skillDistillInputFromCandidate(cand, []string{"go build ./...", "go test ./..."})
	if err != nil {
		t.Fatalf("skillDistillInputFromCandidate: %v", err)
	}

	if len(in.Sig) != 2 || in.Sig[0] != "Bash:git status" {
		t.Errorf("Sig: got %v", in.Sig)
	}
	if len(in.Samples) != 2 || in.Samples[0].Command != "git status --short --branch" {
		t.Errorf("Samples: got %+v", in.Samples)
	}
	if len(in.Gates) != 2 || in.Gates[1] != "go test ./..." {
		t.Errorf("Gates: got %v", in.Gates)
	}
	// Only the empty-examples cluster is skipped; the real cluster contributes
	// exactly one representative example (its first).
	if len(in.RelatedFailures) != 1 {
		t.Fatalf("RelatedFailures: got %+v", in.RelatedFailures)
	}
	rf := in.RelatedFailures[0]
	if rf.ErrorKey != "fatal: not a git repository" || rf.FailedArg != "git status" || rf.FixedArg != "cd repo && git status" {
		t.Errorf("unexpected RelatedFailures[0]: %+v", rf)
	}

	if sourceJSON != `["Bash:git status","Bash:git branch -a"]` {
		t.Errorf("sourceJSON = %q", sourceJSON)
	}
}

func TestSkillDistillInputFromCandidate_DoesNotMutateCandidateSlices(t *testing.T) {
	sig := []string{"Bash:git status"}
	cand := experience.SkillCandidate{Sig: sig}
	gates := []string{"go build ./..."}

	in, _, err := skillDistillInputFromCandidate(cand, gates)
	if err != nil {
		t.Fatalf("skillDistillInputFromCandidate: %v", err)
	}
	in.Sig[0] = "mutated"
	in.Gates[0] = "mutated"
	if sig[0] != "Bash:git status" {
		t.Error("caller's Sig slice was mutated")
	}
	if gates[0] != "go build ./..." {
		t.Error("caller's gates slice was mutated")
	}
}
