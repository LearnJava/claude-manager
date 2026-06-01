package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubCP captures the last HTTP request made to it and returns a canned response.
type stubCP struct {
	lastPath string
	lastBody map[string]any
	// response is returned verbatim; defaults to a valid RPC null-result.
	response   string
	statusCode int
}

func (s *stubCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.lastPath = r.URL.Path
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &s.lastBody)

	code := s.statusCode
	if code == 0 {
		code = http.StatusOK
	}
	resp := s.response
	if resp == "" {
		resp = `{"jsonrpc":"2.0","id":1,"result":null}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, resp)
}

// newTestMCPServer creates an mcpServer wired to a stub control-plane.
func newTestMCPServer(t *testing.T, stub *stubCP) *mcpServer {
	t.Helper()
	ts := httptest.NewServer(stub)
	t.Cleanup(ts.Close)
	return &mcpServer{
		baseURL: ts.URL,
		token:   "test-token",
		client:  ts.Client(),
	}
}

// call is a test helper that invokes callTool and returns the captured stub state.
func call(t *testing.T, srv *mcpServer, stub *stubCP, tool, argsJSON string) {
	t.Helper()
	_, err := srv.callTool(tool, json.RawMessage(argsJSON))
	if err != nil {
		t.Logf("callTool(%s) error (may be expected for stub responses): %v", tool, err)
	}
}

// ── Actions ───────────────────────────────────────────────────────────────────

func TestDispatch_StartSession(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "start_session", `{"project":"myproj","session":"P1"}`)

	if stub.lastPath != "/rpc" {
		t.Fatalf("endpoint: want /rpc, got %s", stub.lastPath)
	}
	if stub.lastBody["method"] != "StartSession" {
		t.Fatalf("method: want StartSession, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["project"] != "myproj" || params["session"] != "P1" {
		t.Fatalf("params: want project=myproj session=P1, got %v", params)
	}
}

func TestDispatch_StopSession_Soft(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "stop_session", `{"project":"myproj","session":"P1","after_task":true}`)

	if stub.lastBody["method"] != "StopSession" {
		t.Fatalf("method: want StopSession, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["id"] != "myproj/P1" {
		t.Fatalf("id: want myproj/P1, got %v", params["id"])
	}
	if params["soft"] != true {
		t.Fatalf("soft: want true, got %v", params["soft"])
	}
}

func TestDispatch_StopSession_Hard(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "stop_session", `{"project":"myproj","session":"P1"}`)

	params := stub.lastBody["params"].(map[string]any)
	if params["soft"] != false {
		t.Fatalf("soft: want false (hard stop), got %v", params["soft"])
	}
}

func TestDispatch_RestartSession_Fresh(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "restart_session", `{"project":"myproj","session":"P1"}`)

	if stub.lastBody["method"] != "RestartSession" {
		t.Fatalf("method: want RestartSession, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["id"] != "myproj/P1" {
		t.Fatalf("id: want myproj/P1, got %v", params["id"])
	}
}

func TestDispatch_RestartSession_Resume(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "restart_session", `{"project":"myproj","session":"P1","resume":true}`)

	if stub.lastBody["method"] != "ResumeSession" {
		t.Fatalf("method: want ResumeSession, got %v", stub.lastBody["method"])
	}
}

func TestDispatch_SendMessage(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "send_message", `{"project":"myproj","session":"P1","text":"hello world"}`)

	if stub.lastBody["method"] != "SendMessage" {
		t.Fatalf("method: want SendMessage, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["id"] != "myproj/P1" {
		t.Fatalf("id: want myproj/P1, got %v", params["id"])
	}
	if params["message"] != "hello world" {
		t.Fatalf("message: want 'hello world', got %v", params["message"])
	}
}

func TestDispatch_ApprovePermission_DefaultScope(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "approve_permission", `{"session_id":"myproj/P1","request_id":"req-abc"}`)

	if stub.lastBody["method"] != "RespondPermission" {
		t.Fatalf("method: want RespondPermission, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["decision"] != "allow" {
		t.Fatalf("decision: want allow, got %v", params["decision"])
	}
	if params["request_id"] != "req-abc" {
		t.Fatalf("request_id: want req-abc, got %v", params["request_id"])
	}
}

func TestDispatch_ApprovePermission_WithScope(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "approve_permission", `{"session_id":"myproj/P1","request_id":"req-abc","scope":"always"}`)

	params := stub.lastBody["params"].(map[string]any)
	if params["decision"] != "always" {
		t.Fatalf("decision: want always (scope passed through), got %v", params["decision"])
	}
}

func TestDispatch_DenyPermission(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "deny_permission", `{"session_id":"myproj/P1","request_id":"req-abc"}`)

	if stub.lastBody["method"] != "RespondPermission" {
		t.Fatalf("method: want RespondPermission, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["decision"] != "deny" {
		t.Fatalf("decision: want deny, got %v", params["decision"])
	}
}

func TestDispatch_SetGlobalSettings(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "set_global_settings", `{"config":{"auto_model_routing":true}}`)

	if stub.lastBody["method"] != "UpdateConfig" {
		t.Fatalf("method: want UpdateConfig, got %v", stub.lastBody["method"])
	}
}

func TestDispatch_RunPreflight(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "run_preflight", `{"project":"myproj","task":"refactor auth"}`)

	if stub.lastPath != "/rpc" {
		t.Fatalf("endpoint: want /rpc, got %s", stub.lastPath)
	}
	if stub.lastBody["method"] != "RunPreflight" {
		t.Fatalf("method: want RunPreflight, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["task"] != "refactor auth" {
		t.Fatalf("task: want 'refactor auth', got %v", params["task"])
	}
}

func TestDispatch_ExecutePlan(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "execute_plan", `{"plan_id":"plan-42"}`)

	if stub.lastBody["method"] != "ExecutePlan" {
		t.Fatalf("method: want ExecutePlan, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["plan_id"] != "plan-42" {
		t.Fatalf("plan_id: want plan-42, got %v", params["plan_id"])
	}
}

// ── Queries ───────────────────────────────────────────────────────────────────

func TestDispatch_GetSessions(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_sessions", `{}`)

	if stub.lastPath != "/rpc" {
		t.Fatalf("endpoint: want /rpc, got %s", stub.lastPath)
	}
	if stub.lastBody["method"] != "GetAllSessions" {
		t.Fatalf("method: want GetAllSessions, got %v", stub.lastBody["method"])
	}
}

func TestDispatch_GetSessionLogs(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_session_logs", `{"project":"myproj","session":"P1","tail":50}`)

	if stub.lastBody["method"] != "GetSessionLog" {
		t.Fatalf("method: want GetSessionLog, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["id"] != "myproj/P1" {
		t.Fatalf("id: want myproj/P1, got %v", params["id"])
	}
	// JSON numbers unmarshal to float64 in map[string]any
	if params["limit"] != float64(50) {
		t.Fatalf("limit: want 50, got %v", params["limit"])
	}
}

func TestDispatch_GetSessionLogs_DefaultTail(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_session_logs", `{"project":"myproj","session":"P1"}`)

	params := stub.lastBody["params"].(map[string]any)
	if params["limit"] != float64(100) {
		t.Fatalf("limit: want default 100, got %v", params["limit"])
	}
}

func TestDispatch_GetPendingPermissions(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_pending_permissions", `{}`)

	if stub.lastBody["method"] != "GetPendingPermissions" {
		t.Fatalf("method: want GetPendingPermissions, got %v", stub.lastBody["method"])
	}
}

func TestDispatch_GetMetrics_WithProject(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_metrics", `{"project":"myproj","period":14}`)

	if stub.lastBody["method"] != "GetProjectCost" {
		t.Fatalf("method: want GetProjectCost, got %v", stub.lastBody["method"])
	}
	params := stub.lastBody["params"].(map[string]any)
	if params["project"] != "myproj" {
		t.Fatalf("project: want myproj, got %v", params["project"])
	}
	if params["days"] != float64(14) {
		t.Fatalf("days: want 14, got %v", params["days"])
	}
}

func TestDispatch_GetMetrics_NoProject(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "get_metrics", `{}`)

	// Falls back to GetRateLimitStatus when no project is given.
	if stub.lastBody["method"] != "GetRateLimitStatus" {
		t.Fatalf("method: want GetRateLimitStatus, got %v", stub.lastBody["method"])
	}
}

// ── Blocking primitives ───────────────────────────────────────────────────────

func TestDispatch_WaitForStatus(t *testing.T) {
	stub := &stubCP{
		response: `{"event":"session:status","data":{"id":"myproj/P1","status":"working"},"ts":"2025-01-01T00:00:00Z"}`,
	}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "wait_for_status", `{"project":"myproj","session":"P1","status":"working","timeout_ms":1000}`)

	if stub.lastPath != "/wait" {
		t.Fatalf("endpoint: want /wait, got %s", stub.lastPath)
	}
	if stub.lastBody["event"] != "session:status" {
		t.Fatalf("event: want session:status, got %v", stub.lastBody["event"])
	}
	match := stub.lastBody["match"].(map[string]any)
	if match["id"] != "myproj/P1" {
		t.Fatalf("match.id: want myproj/P1, got %v", match["id"])
	}
	if match["status"] != "working" {
		t.Fatalf("match.status: want working, got %v", match["status"])
	}
	if stub.lastBody["timeout_ms"] != float64(1000) {
		t.Fatalf("timeout_ms: want 1000, got %v", stub.lastBody["timeout_ms"])
	}
}

func TestDispatch_WaitForStatus_DefaultTimeout(t *testing.T) {
	stub := &stubCP{
		response: `{"event":"session:status","data":{},"ts":"2025-01-01T00:00:00Z"}`,
	}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "wait_for_status", `{"project":"myproj","session":"P1","status":"idle"}`)

	if stub.lastBody["timeout_ms"] != float64(5000) {
		t.Fatalf("timeout_ms: want default 5000, got %v", stub.lastBody["timeout_ms"])
	}
}

func TestDispatch_WaitForEvent(t *testing.T) {
	stub := &stubCP{
		response: `{"event":"session:log","data":{"level":"error"},"ts":"2025-01-01T00:00:00Z"}`,
	}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "wait_for_event", `{"event":"session:log","match":{"level":"error"},"timeout_ms":2000}`)

	if stub.lastPath != "/wait" {
		t.Fatalf("endpoint: want /wait, got %s", stub.lastPath)
	}
	if stub.lastBody["event"] != "session:log" {
		t.Fatalf("event: want session:log, got %v", stub.lastBody["event"])
	}
	match := stub.lastBody["match"].(map[string]any)
	if match["level"] != "error" {
		t.Fatalf("match.level: want error, got %v", match["level"])
	}
}

func TestDispatch_WaitForEvent_NoMatch(t *testing.T) {
	stub := &stubCP{
		response: `{"event":"session:status","data":{},"ts":"2025-01-01T00:00:00Z"}`,
	}
	srv := newTestMCPServer(t, stub)
	call(t, srv, stub, "wait_for_event", `{"event":"session:status"}`)

	if stub.lastPath != "/wait" {
		t.Fatalf("endpoint: want /wait, got %s", stub.lastPath)
	}
	// match field may be nil/absent when not provided
}

func TestDispatch_WaitTimeout(t *testing.T) {
	stub := &stubCP{
		statusCode: http.StatusRequestTimeout,
		response:   `{"error":"timeout"}`,
	}
	srv := newTestMCPServer(t, stub)
	_, err := srv.callTool("wait_for_status", json.RawMessage(`{"project":"p","session":"s","status":"idle"}`))
	if err == nil {
		t.Fatal("expected error for timeout response")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error should mention timeout, got: %v", err)
	}
}

func TestDispatch_UnknownTool(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	_, err := srv.callTool("no_such_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("error should mention unknown tool, got: %v", err)
	}
}

// ── MCP protocol ─────────────────────────────────────────────────────────────

func TestMCPProtocol_Initialize(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)

	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	var resp mcpResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response: %v (raw: %s)", err, out.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	result, _ := resp.Result.(map[string]any)
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion: want %s, got %v", mcpProtocolVersion, result["protocolVersion"])
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != mcpServerName {
		t.Fatalf("serverInfo.name: want %s, got %v", mcpServerName, info["name"])
	}
}

func TestMCPProtocol_Notification_NoResponse(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)

	// "initialized" is a notification (no id) — server must not respond.
	input := `{"jsonrpc":"2.0","method":"initialized","params":{}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected no output for notification, got: %s", out.String())
	}
}

func TestMCPProtocol_ToolsList(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)

	input := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	var resp mcpResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected non-empty tools list")
	}
	// Verify a known tool is present.
	found := false
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "start_session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected start_session in tools list")
	}
}

func TestMCPProtocol_ToolsCall_ViaHTTP(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)

	input := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sessions","arguments":{}}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	if stub.lastBody["method"] != "GetAllSessions" {
		t.Fatalf("method: want GetAllSessions, got %v", stub.lastBody["method"])
	}

	var resp mcpResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content in tools/call result")
	}
}

func TestMCPProtocol_ToolsCall_Error_IsErrorTrue(t *testing.T) {
	stub := &stubCP{
		response: `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"session not found"}}`,
	}
	srv := newTestMCPServer(t, stub)

	input := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_sessions","arguments":{}}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	var resp mcpResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// MCP: tool errors are returned as isError:true in result, not as JSON-RPC errors.
	if resp.Error != nil {
		t.Fatalf("unexpected json-rpc error (should be isError in result): %v", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("isError: want true, got %v", result["isError"])
	}
}

func TestMCPProtocol_UnknownMethod(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)

	input := `{"jsonrpc":"2.0","id":5,"method":"unknown/method","params":{}}` + "\n"
	var out strings.Builder
	_ = srv.run(strings.NewReader(input), &out)

	var resp mcpResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("code: want -32601, got %d", resp.Error.Code)
	}
}
