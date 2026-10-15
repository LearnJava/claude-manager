package control

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"claude-manager/internal/experience"
	"claude-manager/internal/optimization"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// logManager serves a synthetic log of n entries: every 7th is an error, every
// 5th a Bash tool call, and each message carries msgLen bytes of padding.
type logManager struct {
	*mockManager
	n, msgLen int
}

func (m *logManager) GetSessionLog(id string, offset, limit int) ([]*store.LogEntry, error) {
	if id != "p/s" {
		return nil, fmt.Errorf("session %q not found", id)
	}
	var out []*store.LogEntry
	for i := offset; i < m.n && len(out) < limit; i++ {
		e := &store.LogEntry{ID: int64(i), Level: "text", Message: fmt.Sprintf("line %d ", i) + strings.Repeat("x", m.msgLen)}
		if i%7 == 0 {
			e.Level = "error"
		}
		if i%5 == 0 {
			e.ToolName, e.ToolInput = "Bash", "go test ./..."
		}
		out = append(out, e)
	}
	return out, nil
}

type logsApp struct {
	*mockApp
	calls *[]string
}

func (a logsApp) rec(s string) { *a.calls = append(*a.calls, s) }
func (a logsApp) GetProjectLogFiles(p string) ([]store.LogFileInfo, error) {
	a.rec("GetProjectLogFiles " + p)
	return nil, nil
}
func (a logsApp) ClearProjectLogs(p string) error { a.rec("ClearProjectLogs " + p); return nil }
func (a logsApp) CleanOldLogs(d int) error        { a.rec(fmt.Sprintf("CleanOldLogs %d", d)); return nil }
func (a logsApp) ImportProjectLogs(p, d string) (session.ImportStats, error) {
	a.rec("ImportProjectLogs " + p + " " + d)
	return session.ImportStats{Files: 2, Actions: 9}, nil
}
func (a logsApp) GetTopActions(p string, d int) ([]store.SignatureStat, error) {
	a.rec(fmt.Sprintf("GetTopActions %s %d", p, d))
	return []store.SignatureStat{{Sig: "Bash:ls", Count: 3}}, nil
}
func (a logsApp) GetActionSamples(p, sig string, n int) ([]store.ActionRow, error) {
	a.rec(fmt.Sprintf("GetActionSamples %s %s %d", p, sig, n))
	return nil, nil
}
func (a logsApp) GetDurationProfile(string) ([]experience.SignatureDuration, error) { return nil, nil }
func (a logsApp) GetTokenAttribution(p string, n int) (experience.AttributionReport, error) {
	a.rec(fmt.Sprintf("GetTokenAttribution %s %d", p, n))
	return experience.AttributionReport{TotalEstTokens: 10}, nil
}
func (a logsApp) GetModelRecommendation(p, n string) (*optimization.ModelRecommendation, error) {
	return &optimization.ModelRecommendation{Model: "sonnet"}, nil
}
func (a logsApp) GetAutoModelRouting() bool { return true }

func newLogsServer(t *testing.T, n, msgLen int) (*Server, *httptest.Server, string, *[]string) {
	t.Helper()
	var calls []string
	srv, token := newTestServer(t, &logManager{&mockManager{}, n, msgLen})
	srv.app = logsApp{&mockApp{}, &calls}
	srv.exportDir = t.TempDir()
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	t.Cleanup(ts.Close)
	return srv, ts, token, &calls
}

func TestRPC_SearchSessionLog(t *testing.T) {
	_, ts, token, _ := newLogsServer(t, 1200, 10) // three batches of the scan
	out := planRPC(t, ts, token, rpcBody("SearchSessionLog", `{"id":"p/s","level":"error","tool":"bash"}`))
	rows, _ := out.Result.([]any)
	// i%7==0 and i%5==0 means i%35==0: 0, 35, ... 1190.
	if out.Error != nil || len(rows) != 35 {
		t.Fatalf("matches: %d, err %v", len(rows), out.Error)
	}
	out = planRPC(t, ts, token, rpcBody("SearchSessionLog", `{"id":"p/s","contains":"LINE 1199","limit":5}`))
	if rows, _ := out.Result.([]any); len(rows) != 1 {
		t.Fatalf("contains is case-insensitive and reaches the last batch: %+v", out)
	}
	out = planRPC(t, ts, token, rpcBody("SearchSessionLog", `{"id":"p/s","level":"error","offset":3,"limit":2}`))
	rows, _ = out.Result.([]any)
	if len(rows) != 2 || !strings.HasPrefix(rows[0].(map[string]any)["Message"].(string), "line 21 ") {
		t.Fatalf("offset skips matches: %+v", out.Result)
	}
	if msg := callErr(t, ts, token, "SearchSessionLog", `{"id":"p/s"}`); !strings.Contains(msg, "at least one filter") {
		t.Errorf("no filter: %s", msg)
	}
	if msg := callErr(t, ts, token, "SearchSessionLog", `{"id":"p/none","level":"error"}`); !strings.Contains(msg, "not found") {
		t.Errorf("unknown session: %s", msg)
	}
}

func TestRPC_ExportLogWritesFile(t *testing.T) {
	srv, ts, token, _ := newLogsServer(t, 600, 4)
	r := call(t, ts, token, "ExportLog", `{"id":"p/s","format":"json"}`)
	path, _ := r["path"].(string)
	if !strings.HasPrefix(path, srv.exportDir) || !strings.HasSuffix(path, ".json") || r["entries"] != float64(600) {
		t.Fatalf("result %+v", r)
	}
	data, err := os.ReadFile(path)
	if err != nil || !json.Valid(data) {
		t.Fatalf("file: %v valid=%v", err, json.Valid(data))
	}
	if msg := callErr(t, ts, token, "ExportLog", `{"id":"p/s","format":"pdf"}`); !strings.Contains(msg, "md, json or txt") {
		t.Errorf("bad format: %s", msg)
	}
}

func TestRPC_LogsAppMethods(t *testing.T) {
	_, ts, token, calls := newLogsServer(t, 1, 1)
	call(t, ts, token, "GetDailyTokens", `{"date":"2026-10-15"}`)
	call(t, ts, token, "ClearProjectLogs", `{"project":"p"}`)
	call(t, ts, token, "CleanOldLogs", `{"retention_days":30}`)
	call(t, ts, token, "GetTopActions", `{"project":"p","days":7}`)
	r := call(t, ts, token, "ImportProjectLogs", `{"project":"p"}`)
	if r["actions"] != float64(9) {
		t.Errorf("import: %+v", r)
	}
	r = call(t, ts, token, "GetModelAdvice", `{"project":"p","session":"s"}`)
	if r["auto_routing"] != true || r["recommendation"] == nil {
		t.Errorf("advice: %+v", r)
	}
	want := "ClearProjectLogs p|CleanOldLogs 30|GetTopActions p 7|ImportProjectLogs p "
	if got := strings.Join(*calls, "|"); got != want {
		t.Errorf("app calls %q, want %q", got, want)
	}
	if msg := callErr(t, ts, token, "CleanOldLogs", `{"retention_days":0}`); !strings.Contains(msg, "at least 1") {
		t.Errorf("retention: %s", msg)
	}
	if msg := callErr(t, ts, token, "ImportProjectLogs", `{"project":"p","dir":"/no/such/dir"}`); !strings.Contains(msg, "does not exist") {
		t.Errorf("dir: %s", msg)
	}
}

func TestRPC_LogsNeedApp(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	for _, m := range []string{"GetProjectLogFiles", "ClearProjectLogs", "GetTopActions", "GetDurationProfile", "GetModelAdvice"} {
		out := planRPC(t, ts, token, rpcBody(m, `{"project":"p"}`))
		if out.Error == nil || !strings.Contains(out.Error.Message, "needs the Claude Manager app") {
			t.Errorf("%s without app: %+v", m, out.Error)
		}
	}
}

// Destructive tools refuse without confirm=true, before any call is made.
func TestLogsTools_DestructiveNeedConfirm(t *testing.T) {
	for _, tc := range []struct{ tool, args string }{
		{"clear_project_logs", `{"project":"p"}`},
		{"clean_old_logs", `{"retention_days":30}`},
	} {
		if _, _, err := TranslateTool(tc.tool, json.RawMessage(tc.args)); err == nil || !strings.Contains(err.Error(), "confirm=true") {
			t.Errorf("%s: %v", tc.tool, err)
		}
	}
	if _, _, err := TranslateTool("get_cost", json.RawMessage(`{"scope":"day","date":"15.10.2026"}`)); err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("bad date: %v", err)
	}
}

// A large log keeps the tool answer within MaxResponseBytes and says how to
// continue.
func TestLogsTools_LargeFixtureStaysInLimit(t *testing.T) {
	_, ts, token, _ := newLogsServer(t, 3000, 2000)
	args := json.RawMessage(`{"session_id":"p/s","level":"error","limit":500}`)
	_, body, err := TranslateTool("search_session_log", args)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	var req struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(b, &req)
	out := planRPC(t, ts, token, rpcBody("SearchSessionLog", string(req.Params)))
	if out.Error != nil {
		t.Fatal(out.Error.Message)
	}
	shaped, summary, err := ShapeResult("search_session_log", args, out.Result)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := json.Marshal(shaped)
	if len(sb) > MaxResponseBytes {
		t.Errorf("response %d bytes, limit %d", len(sb), MaxResponseBytes)
	}
	if !strings.Contains(summary, "More available") || !strings.Contains(string(sb), "next_cursor") {
		t.Errorf("no continuation: %q", summary)
	}
}
