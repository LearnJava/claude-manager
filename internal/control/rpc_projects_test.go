package control

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/envcheck"
	"claude-manager/internal/session"
)

// fileApp is a config-backed app like the real one: UpdateConfig saves to a
// file and keeps the new config, so a test can look at both.
type fileApp struct {
	cfg     *config.AppConfig
	cfgPath string
	updates int
}

func (a *fileApp) GetConfig() *config.AppConfig { return a.cfg }

func (a *fileApp) UpdateConfig(cfg config.AppConfig) error {
	if err := config.Save(&cfg, a.cfgPath); err != nil {
		return err
	}
	a.cfg = &cfg
	a.updates++
	return nil
}

func (a *fileApp) CheckEnvironment() envcheck.Report { return envcheck.Report{} }
func (a *fileApp) HasClaudeMd(path string) bool {
	_, err := os.Stat(filepath.Join(path, "CLAUDE.md"))
	return err == nil
}
func (a *fileApp) HasSessionProtocol(string) (bool, error)         { return false, nil }
func (a *fileApp) InstallSessionProtocol(string) ([]string, error) { return nil, nil }
func (a *fileApp) GenerateClaudeMdSession(string) error            { return nil }
func (a *fileApp) InitGitRepo(string) error                        { return nil }
func (a *fileApp) MirrorProjectStatus(string) (any, error)         { return map[string]any{"enabled": true}, nil }
func (a *fileApp) UnmirrorProject(string) error                    { return nil }
func (a *fileApp) SyncProjectMirrorsResults() (any, error)         { return []any{}, nil }
func (a *fileApp) MirrorStatusOf(string) any                       { return map[string]any{"enabled": false} }

func newProjectsServer(t *testing.T, mgr ManagerAPI) (*fileApp, *httptest.Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(path) // missing file: defaults, i.e. an empty config
	if err != nil {
		t.Fatal(err)
	}
	app := &fileApp{cfg: cfg, cfgPath: path}
	srv, token := newTestServer(t, mgr)
	srv.app = app
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	t.Cleanup(ts.Close)
	return app, ts, token
}

func call(t *testing.T, ts *httptest.Server, token, method, params string) map[string]any {
	t.Helper()
	out := planRPC(t, ts, token, rpcBody(method, params))
	if out.Error != nil {
		t.Fatalf("%s: %s", method, out.Error.Message)
	}
	m, _ := out.Result.(map[string]any)
	return m
}

func callErr(t *testing.T, ts *httptest.Server, token, method, params string) string {
	t.Helper()
	out := planRPC(t, ts, token, rpcBody(method, params))
	if out.Error == nil {
		t.Fatalf("%s: want an error, got %+v", method, out.Result)
	}
	return out.Error.Message
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

// TestRPC_ConfigLifecycleFromEmpty is the MCP-08 acceptance scenario: from an
// empty config add a project, add a session, change its model, see dry_run
// leave the file alone, then remove.
func TestRPC_ConfigLifecycleFromEmpty(t *testing.T) {
	app, ts, token := newProjectsServer(t, &mockManager{})
	dir := t.TempDir()

	r := call(t, ts, token, "AddProject", jsonOf(map[string]any{"name": "lumen", "path": dir}))
	if r["changed"] != true || len(app.cfg.Projects) != 1 {
		t.Fatalf("AddProject: %+v, projects %d", r, len(app.cfg.Projects))
	}
	call(t, ts, token, "AddSession", `{"project":"lumen","session":"P1","fields":{"model":"sonnet"}}`)
	if got := app.cfg.Projects[0].Sessions; len(got) != 1 || got[0].Model != "sonnet" {
		t.Fatalf("session after add: %+v", got)
	}

	r = call(t, ts, token, "UpdateSession", `{"project":"lumen","session":"P1","fields":{"model":"opus","max_budget_usd":5}}`)
	ch, _ := r["changes"].(map[string]any)
	model, _ := ch["model"].(map[string]any)
	if model["was"] != "sonnet" || model["now"] != "opus" || ch["max_budget_usd"] == nil {
		t.Fatalf("diff: %s", jsonOf(r))
	}
	s := app.cfg.Projects[0].Sessions[0]
	if s.Model != "opus" || s.MaxBudgetUSD != 5 {
		t.Fatalf("session after update: %+v", s)
	}

	// dry_run: same diff, no write, no state change.
	before, _ := os.ReadFile(app.cfgPath)
	updates := app.updates
	r = call(t, ts, token, "UpdateSession", `{"project":"lumen","session":"P1","fields":{"model":"haiku"},"dry_run":true}`)
	after, _ := os.ReadFile(app.cfgPath)
	if r["dry_run"] != true || r["changed"] != false || r["changes"] == nil {
		t.Fatalf("dry run result: %s", jsonOf(r))
	}
	if string(before) != string(after) || app.updates != updates || app.cfg.Projects[0].Sessions[0].Model != "opus" {
		t.Fatal("dry_run must not change the config or the file")
	}

	call(t, ts, token, "RemoveSession", `{"project":"lumen","session":"P1"}`)
	if len(app.cfg.Projects[0].Sessions) != 0 {
		t.Fatal("session not removed")
	}
	call(t, ts, token, "RemoveProject", `{"name":"lumen"}`)
	if len(app.cfg.Projects) != 0 {
		t.Fatal("project not removed")
	}
}

func TestRPC_ConfigEditErrors(t *testing.T) {
	app, ts, token := newProjectsServer(t, &mockManager{})
	dir := t.TempDir()
	call(t, ts, token, "AddProject", jsonOf(map[string]any{"name": "lumen", "path": dir}))
	call(t, ts, token, "AddSession", `{"project":"lumen","session":"P1"}`)

	cases := []struct{ method, params, want string }{
		{"AddProject", jsonOf(map[string]any{"name": "x", "path": filepath.Join(dir, "missing")}), "does not exist"},
		{"AddProject", jsonOf(map[string]any{"name": "x", "path": "relative"}), "not absolute"},
		{"AddProject", jsonOf(map[string]any{"name": "lumen", "path": t.TempDir()}), "already exists"},
		{"AddProject", jsonOf(map[string]any{"name": "other", "path": dir}), `already project "lumen"`},
		{"AddSession", `{"project":"nope","session":"P1"}`, "configured projects are lumen"},
		{"AddSession", `{"project":"lumen","session":"P1"}`, "already exists"},
		{"UpdateSession", `{"project":"lumen","session":"P1","fields":{"bogus":1}}`, `no editable field "bogus"`},
		{"UpdateSession", `{"project":"lumen","session":"P1","fields":{"name":"P2"}}`, `no editable field "name"`},
		{"UpdateSession", `{"project":"lumen","session":"P1","fields":{"max_budget_usd":"lots"}}`, "wrong type"},
		{"UpdateSession", `{"project":"lumen","session":"P1","fields":{"runtime":"vim"}}`, "invalid runtime"},
		{"UpdateSession", `{"project":"lumen","session":"Q","fields":{"model":"x"}}`, "sessions of lumen are P1"},
		{"UpdateGlobalSettings", `{"fields":{"claude_path":"C:\\evil.exe"}}`, `no editable field "claude_path"`},
		{"UpdateProject", `{"project":"lumen","fields":{"session":[]}}`, `no editable field "session"`},
	}
	for _, c := range cases {
		if msg := callErr(t, ts, token, c.method, c.params); !strings.Contains(msg, c.want) {
			t.Errorf("%s %s: error %q does not contain %q", c.method, c.params, msg, c.want)
		}
	}
	if len(app.cfg.Projects) != 1 || len(app.cfg.Projects[0].Sessions) != 1 {
		t.Error("a refused edit must leave the config alone")
	}
}

func TestRPC_RemoveRefusedWhileRunning(t *testing.T) {
	mgr := &mockManager{allSessions: []session.SessionState{{ID: "lumen/P1", Project: "lumen", Name: "P1", Status: "working"}}}
	app, ts, token := newProjectsServer(t, mgr)
	call(t, ts, token, "AddProject", jsonOf(map[string]any{"name": "lumen", "path": t.TempDir()}))
	call(t, ts, token, "AddSession", `{"project":"lumen","session":"P1"}`)

	if msg := callErr(t, ts, token, "RemoveSession", `{"project":"lumen","session":"P1"}`); !strings.Contains(msg, "stop_session") {
		t.Errorf("RemoveSession: %q", msg)
	}
	if msg := callErr(t, ts, token, "RemoveProject", `{"name":"lumen"}`); !strings.Contains(msg, "stop_project") {
		t.Errorf("RemoveProject: %q", msg)
	}
	if len(app.cfg.Projects[0].Sessions) != 1 {
		t.Error("a running session must stay")
	}
}

func TestRPC_GlobalSettingsWhitelistAndTypes(t *testing.T) {
	app, ts, token := newProjectsServer(t, &mockManager{})
	r := call(t, ts, token, "UpdateGlobalSettings", `{"fields":{"daily_budget_alert":20,"language":"ru","crash_recovery":false}}`)
	if r["changed"] != true || app.cfg.Settings.DailyBudgetAlert != 20 || app.cfg.Settings.Language != "ru" {
		t.Fatalf("settings: %s, %+v", jsonOf(r), app.cfg.Settings)
	}
	// Whole numbers given for an integer field arrive as float64 from JSON.
	call(t, ts, token, "UpdateGlobalSettings", `{"fields":{"log_retention_days":14}}`)
	if app.cfg.Settings.LogRetentionDays != 14 {
		t.Errorf("log_retention_days = %d", app.cfg.Settings.LogRetentionDays)
	}
	// Setting a field to the value it has changes nothing and writes nothing.
	n := app.updates
	r = call(t, ts, token, "UpdateGlobalSettings", `{"fields":{"log_retention_days":14}}`)
	if r["changed"] != false || app.updates != n {
		t.Errorf("no-op edit wrote: %s", jsonOf(r))
	}
}

func TestRPC_ConfigViewAndVersion(t *testing.T) {
	app, ts, token := newProjectsServer(t, &mockManager{})
	call(t, ts, token, "AddProject", jsonOf(map[string]any{"name": "lumen", "path": t.TempDir()}))

	v := call(t, ts, token, "GetConfigView", `{}`)
	cfg, _ := v["config"].(map[string]any)
	if v["version"] == "" || cfg["project"] == nil || cfg["settings"] == nil {
		t.Fatalf("view: %s", jsonOf(v))
	}

	// ReplaceConfig with the current version applies; with a stale one it is refused.
	cfg["settings"].(map[string]any)["theme"] = "light"
	r := call(t, ts, token, "ReplaceConfig", jsonOf(map[string]any{"config": cfg, "version": v["version"]}))
	if app.cfg.Settings.Theme != "light" || r["version"] == v["version"] {
		t.Fatalf("replace: %s, theme %q", jsonOf(r), app.cfg.Settings.Theme)
	}
	msg := callErr(t, ts, token, "ReplaceConfig", jsonOf(map[string]any{"config": cfg, "version": v["version"]}))
	if !strings.Contains(msg, "changed since version") {
		t.Errorf("stale version: %q", msg)
	}
	if msg := callErr(t, ts, token, "ReplaceConfig", jsonOf(map[string]any{"config": cfg})); !strings.Contains(msg, "version is required") {
		t.Errorf("missing version: %q", msg)
	}
}

func TestRedactConfig(t *testing.T) {
	m := map[string]any{
		"worker": []map[string]any{{"key_env": "KILO_KEY", "api_key": "sk-123"}},
		"settings": map[string]any{"password": "hunter2", "theme": "dark", "token": ""},
	}
	redactConfig(m)
	if got := jsonOf(m); strings.Contains(got, "sk-123") || strings.Contains(got, "hunter2") || !strings.Contains(got, "KILO_KEY") {
		t.Errorf("redacted: %s", got)
	}
}

func TestRPC_ProjectActions(t *testing.T) {
	_, ts, token := newProjectsServer(t, &mockManager{})
	dir := t.TempDir()
	call(t, ts, token, "AddProject", jsonOf(map[string]any{"name": "lumen", "path": dir}))

	r := call(t, ts, token, "GetProjectSetup", `{"project":"lumen"}`)
	if r["claude_md"] != false || r["path"] != dir || r["mirror"] == nil {
		t.Fatalf("setup: %s", jsonOf(r))
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := call(t, ts, token, "GetProjectSetup", `{"project":"lumen"}`); r["claude_md"] != true {
		t.Fatalf("setup after CLAUDE.md: %s", jsonOf(r))
	}
	if msg := callErr(t, ts, token, "InitGitRepo", `{"project":"nope"}`); !strings.Contains(msg, "configured projects are lumen") {
		t.Errorf("unknown project: %q", msg)
	}
	for _, m := range []string{"InitGitRepo", "GenerateClaudeMdSession", "InstallSessionProtocol", "MirrorProject", "UnmirrorProject", "GetProjectMirror"} {
		if out := planRPC(t, ts, token, rpcBody(m, `{"project":"lumen"}`)); out.Error != nil {
			t.Errorf("%s: %s", m, out.Error.Message)
		}
	}
}

func TestRPC_ProjectsNeedApp(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	for _, m := range []string{"InitGitRepo", "MirrorProject", "HasClaudeMd"} {
		if msg := callErr(t, ts, token, m, `{"project":"p"}`); !strings.Contains(msg, "needs the Claude Manager app") {
			t.Errorf("%s: %q", m, msg)
		}
	}
}

func TestMCPTool_AddProjectChecksPathBeforeTheCall(t *testing.T) {
	_, _, err := TranslateTool("add_project", json.RawMessage(`{"name":"x","path":"D:\\definitely\\not\\here"}`))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("want a path error, got %v", err)
	}
}
