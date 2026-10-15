package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// buildBinary compiles ./cmd/<name> once per test run and returns its path.
func buildBinary(t *testing.T, name string) string {
	t.Helper()
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH: " + err.Error())
	}
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "cm-mcp-bin")
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	exe := filepath.Join(buildDir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	cmd := exec.Command(goExe, "build", "-o", exe, "./cmd/"+name+"/")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return exe
}

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// startControlPlane runs a real control-plane over an empty config, with
// fakeclaude as the CLI, and returns its address and token.
func startControlPlane(t *testing.T) (addr, token string) {
	t.Helper()
	claude := buildBinary(t, "fakeclaude")
	t.Setenv("FAKECLAUDE_SCENARIO", filepath.Join(repoRoot(t), "testdata", "scenarios"))

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(cfgPath) // no file: the empty default config
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.ClaudePath = claude
	cfg.Settings.DefaultRetryDelay = 1
	cfg.Settings.RateLimitPause = 1
	cfg.Settings.CrashRecovery = false
	cfg.Settings.SessionStartDelay = 0

	ce := control.NewControlEmitter(500)
	st, err := store.New(filepath.Join(filepath.Dir(cfgPath), "cm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := session.NewSessionManager(cfg, cfgPath, st, ce)
	token = "e2e-mcp-token"
	srv := control.NewServer(mgr, &e2eApp{cfg: cfg, path: cfgPath, mgr: mgr}, ce, token)
	ln, err := srv.Listen("0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(func() { mgr.StopAll(); cancel() })
	return "http://" + ln.Addr().String(), token
}

// e2eApp is the config side of the real App: it saves the config and hands it
// to the session manager, which is what makes a new session startable.
type e2eApp struct {
	cfg  *config.AppConfig
	path string
	mgr  *session.SessionManager
}

func (a *e2eApp) GetConfig() *config.AppConfig { return a.cfg }

func (a *e2eApp) UpdateConfig(cfg config.AppConfig) error {
	if err := config.Save(&cfg, a.path); err != nil {
		return err
	}
	a.cfg = &cfg
	a.mgr.SetConfig(&cfg)
	return nil
}

// connectMCP starts cm-mcp as a subprocess and connects an SDK client over stdio.
func connectMCP(t *testing.T, addr, token, level string) *mcp.ClientSession {
	t.Helper()
	exe := buildBinary(t, "cm-mcp")
	cmd := exec.Command(exe, "-level", level)
	cmd.Env = append(os.Environ(), "CM_CONTROL_ADDR="+addr, "CM_CONTROL_TOKEN="+token)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// tool calls a tool and returns its structuredContent; a tool error fails the test.
func tool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, textOf(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// waitStatus blocks until the session reaches status, failing on timeout.
func waitStatus(t *testing.T, cs *mcp.ClientSession, id, status string) {
	t.Helper()
	out := tool(t, cs, "wait_for_status", map[string]any{"session_id": id, "status": status, "timeout_ms": 20000})
	if out["reached"] != true {
		t.Fatalf("session %s never reached %s: %v", id, status, out)
	}
}

// pollStatus reads the session's current state until it shows status. Unlike
// wait_for_status it cannot be satisfied by a transition that already happened,
// which matters right after a restart.
func pollStatus(t *testing.T, cs *mcp.ClientSession, id, status string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		out := tool(t, cs, "get_session", map[string]any{"session_id": id})
		raw, _ := json.Marshal(out)
		last = string(raw)
		if sess, _ := out["session"].(map[string]any); sess["status"] == status || out["status"] == status {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session %s never showed %s; last: %s", id, status, last)
}

func TestE2E_FullControlThroughMCP(t *testing.T) {
	addr, token := startControlPlane(t)
	cs := connectMCP(t, addr, token, "admin")
	dir := t.TempDir()
	const id = "demo/S1"

	// Empty config -> project -> session.
	if p := tool(t, cs, "get_projects", nil); len(asList(p["projects"])) != 0 {
		t.Fatalf("config is not empty: %v", p)
	}
	tool(t, cs, "add_project", map[string]any{"name": "demo", "path": dir})
	tool(t, cs, "add_session", map[string]any{"project": "demo", "name": "S1", "fields": map[string]any{
		"prompt":          "mcp-full-control: start",
		"model":           "claude-sonnet-4-6",
		"permission_mode": "acceptEdits",
		"auto_restart":    false,
	}})

	// Launch -> question -> answer.
	tool(t, cs, "start_session", map[string]any{"session_id": id})
	waitStatus(t, cs, id, "waiting_for_user")
	qs := asList(tool(t, cs, "get_pending_questions", map[string]any{"session_id": id})["questions"])
	if len(qs) != 1 {
		t.Fatalf("want one pending question, got %v", qs)
	}
	q, _ := qs[0].(map[string]any)["question"].(map[string]any)
	qid, _ := q["id"].(string)
	tool(t, cs, "answer_question", map[string]any{"session_id": id, "question_id": qid, "answer": "Safe"})

	// Permission.
	waitStatus(t, cs, id, "waiting_permission")
	tool(t, cs, "approve_permission", map[string]any{"session_id": id, "request_id": "p1"})
	waitStatus(t, cs, id, "idle")

	// Log and metrics.
	logs, _ := json.Marshal(tool(t, cs, "get_session_logs", map[string]any{"session_id": id, "limit": 50}))
	if !strings.Contains(string(logs), "the file is updated") {
		t.Fatalf("log lacks the final reply: %s", logs)
	}
	m := tool(t, cs, "get_session_metrics", map[string]any{"session_id": id})
	if mj, _ := json.Marshal(m); !strings.Contains(string(mj), "0.02") {
		t.Fatalf("metrics lack the cost: %s", mj)
	}

	// The finished turn ends the process. Restart replays the script; the model
	// is then switched live and the session stopped.
	tool(t, cs, "restart_session", map[string]any{"session_id": id})
	pollStatus(t, cs, id, "waiting_for_user")
	tool(t, cs, "set_session_model", map[string]any{"session_id": id, "model": "claude-opus-4-6"})
	tool(t, cs, "stop_project", map[string]any{"project": "demo"})
	pollStatus(t, cs, id, "idle")

	// Remove the session, then the project.
	tool(t, cs, "remove_session", map[string]any{"session_id": id})
	tool(t, cs, "remove_project", map[string]any{"project": "demo"})
	if p := tool(t, cs, "get_projects", nil); len(asList(p["projects"])) != 0 {
		t.Fatalf("project not removed: %v", p)
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
