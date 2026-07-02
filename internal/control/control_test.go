package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/permission"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// ── mock implementations ──────────────────────────────────────────────────────

type mockManager struct {
	allSessions []session.SessionState
	startErr    error
	calls       []string // "<method> <args...>" of plan-related invocations
}

func (m *mockManager) record(method string, args ...string) {
	m.calls = append(m.calls, strings.TrimSpace(method+" "+strings.Join(args, " ")))
}

func (m *mockManager) StartSession(project, name string) error { return m.startErr }
func (m *mockManager) StartSessionWithOverride(project, name, model, effort string) error {
	return nil
}
func (m *mockManager) StopSession(id string, soft bool) error                        { return nil }
func (m *mockManager) StopAll()                                                       {}
func (m *mockManager) RestartSession(id string) error                                 { return nil }
func (m *mockManager) ResumeSession(id string) error                                  { return nil }
func (m *mockManager) StartProject(project string) error                              { return nil }
func (m *mockManager) StopProject(project string) error                               { return nil }
func (m *mockManager) SendMessage(id, message string) error                           { return nil }
func (m *mockManager) RespondPermission(id, requestID, decision string) error         { return nil }
func (m *mockManager) GetPendingPermissions() []permission.PermissionRequest          { return nil }
func (m *mockManager) GetAllSessions() []session.SessionState                         { return m.allSessions }
func (m *mockManager) GetSession(id string) (session.SessionState, bool)              { return session.SessionState{}, false }
func (m *mockManager) GetSessionLog(id string, offset, limit int) ([]*store.LogEntry, error) {
	return nil, nil
}
func (m *mockManager) GetHistory(project string, limit int) ([]*store.SessionRun, error) {
	return nil, nil
}
func (m *mockManager) GetSessionMetrics(id string) (session.SessionMetrics, error) {
	return session.SessionMetrics{}, nil
}
func (m *mockManager) GetDailyCost(date string) (float64, error)              { return 0, nil }
func (m *mockManager) GetProjectCost(project string, days int) (float64, error) { return 0, nil }
func (m *mockManager) GetRateLimitStatus() *session.RateLimitInfo              { return nil }
func (m *mockManager) ClearSessionState(project, name string)                  {}
func (m *mockManager) GetSessionState(project, name string) *session.PersistedState { return nil }
func (m *mockManager) RunPreflight(project, task string) (*analysis.TaskPlan, error) {
	m.record("RunPreflight", project, task)
	return &analysis.TaskPlan{Project: project, OriginalTask: task}, nil
}
func (m *mockManager) ApprovePlan(plan *analysis.TaskPlan) (*analysis.TaskPlan, error) {
	m.record("ApprovePlan", plan.Project)
	plan.Status = analysis.PlanStatusApproved
	return plan, nil
}
func (m *mockManager) ExecutePlan(planID int64) error {
	m.record("ExecutePlan", fmt.Sprint(planID))
	return nil
}
func (m *mockManager) GetPlan(planID int64) (*analysis.TaskPlan, error) {
	m.record("GetPlan", fmt.Sprint(planID))
	return &analysis.TaskPlan{ID: planID}, nil
}

type mockApp struct{}

func (a *mockApp) GetConfig() *config.AppConfig       { return &config.AppConfig{} }
func (a *mockApp) UpdateConfig(cfg config.AppConfig) error { return nil }

// newTestServer creates a Server backed by mock manager/app, ready for httptest.
func newTestServer(t *testing.T, mgr ManagerAPI) (*Server, string) {
	t.Helper()
	const token = "test-token-1234"
	emitter := NewControlEmitter(50)
	srv := NewServer(mgr, &mockApp{}, emitter, token)
	return srv, token
}

// ── token auth ────────────────────────────────────────────────────────────────

func TestTokenAuth_Missing(t *testing.T) {
	srv, _ := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestTokenAuth_Wrong(t *testing.T) {
	srv, _ := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(`{}`))
	req.Header.Set("X-CM-Token", "wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestTokenAuth_Correct(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"GetAllSessions","params":{}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("X-CM-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// ── RPC dispatch ──────────────────────────────────────────────────────────────

func TestRPC_GetAllSessions(t *testing.T) {
	mgr := &mockManager{
		allSessions: []session.SessionState{
			{ID: "proj/S1", Status: "working"},
		},
	}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":42,"method":"GetAllSessions","params":{}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("X-CM-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("unexpected RPC error: %v", out.Error.Message)
	}
	// Result should be a non-empty JSON array.
	raw, _ := json.Marshal(out.Result)
	if !bytes.Contains(raw, []byte("proj/S1")) {
		t.Fatalf("expected session id in result, got: %s", raw)
	}
}

func TestRPC_UnknownMethod(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"DoSomethingUndefined","params":{}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("X-CM-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil || out.Error.Code != -32601 {
		t.Fatalf("expected -32601 method not found, got %+v", out.Error)
	}
}

func TestRPC_StartSession_Error(t *testing.T) {
	mgr := &mockManager{startErr: errForTest("project not found")}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"StartSession","params":{"project":"x","session":"s"}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("X-CM-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil {
		t.Fatal("expected RPC error for failed StartSession")
	}
	if !strings.Contains(out.Error.Message, "project not found") {
		t.Fatalf("unexpected error message: %s", out.Error.Message)
	}
}

// ── wait / ring buffer matching ───────────────────────────────────────────────

func TestWaitMatch_FromRingBuffer(t *testing.T) {
	emitter := NewControlEmitter(50)
	srv := &Server{
		manager:  &mockManager{},
		app:      &mockApp{},
		emitter:  emitter,
		token:    "tok",
		registry: make(map[string]handler),
	}

	// Pre-populate ring buffer.
	emitter.Emit("session:status", map[string]string{"id": "p/s1", "status": "working"})
	emitter.Emit("session:status", map[string]string{"id": "p/s2", "status": "idle"})

	// POST /wait for the "working" event that already happened.
	body := `{"event":"session:status","match":{"id":"p/s1","status":"working"},"timeout_ms":100}`
	req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(body))
	req.Header.Set("X-CM-Token", "tok")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.handleWait(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out waitResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Event != "session:status" {
		t.Fatalf("unexpected event: %s", out.Event)
	}
}

func TestWaitMatch_Timeout(t *testing.T) {
	emitter := NewControlEmitter(50)
	srv := &Server{
		manager:  &mockManager{},
		app:      &mockApp{},
		emitter:  emitter,
		token:    "tok",
		registry: make(map[string]handler),
	}

	body := `{"event":"session:status","match":{"id":"nobody","status":"done"},"timeout_ms":50}`
	req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(body))
	rr := httptest.NewRecorder()

	start := time.Now()
	srv.handleWait(rr, req)
	elapsed := time.Since(start)

	if rr.Code != http.StatusRequestTimeout {
		t.Fatalf("want 408, got %d", rr.Code)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("wait took too long: %v", elapsed)
	}
}

func TestWaitMatch_FutureEvent(t *testing.T) {
	emitter := NewControlEmitter(50)
	srv := &Server{
		manager:  &mockManager{},
		app:      &mockApp{},
		emitter:  emitter,
		token:    "tok",
		registry: make(map[string]handler),
	}

	// Fire the matching event after a short delay.
	go func() {
		time.Sleep(30 * time.Millisecond)
		emitter.Emit("session:status", map[string]string{"id": "p/s3", "status": "idle"})
	}()

	body := `{"event":"session:status","match":{"id":"p/s3","status":"idle"},"timeout_ms":500}`
	req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWait(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ── ControlEmitter ring buffer ────────────────────────────────────────────────

func TestControlEmitter_RingOverflow(t *testing.T) {
	ce := NewControlEmitter(3)
	for i := 0; i < 5; i++ {
		ce.Emit("e", map[string]int{"n": i})
	}
	recent := ce.Recent("e", 0)
	if len(recent) != 3 {
		t.Fatalf("expected 3 events in ring, got %d", len(recent))
	}
}

func TestControlEmitter_PerEventFilter(t *testing.T) {
	ce := NewControlEmitter(50)
	ce.Emit("session:log", map[string]string{"msg": "a"})
	ce.Emit("session:status", map[string]string{"status": "working"})
	ce.Emit("session:log", map[string]string{"msg": "b"})

	logs := ce.Recent("session:log", 0)
	if len(logs) != 2 {
		t.Fatalf("expected 2 log events, got %d", len(logs))
	}
	statuses := ce.Recent("session:status", 0)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status event, got %d", len(statuses))
	}
}

// ── match helper ─────────────────────────────────────────────────────────────

func TestMatchEnvelope(t *testing.T) {
	emit := func(data any) EnvelopedEvent {
		raw, _ := json.Marshal(data)
		return EnvelopedEvent{Event: "e", Data: raw}
	}
	match := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		_ = json.Unmarshal([]byte(s), &m)
		return m
	}

	cases := []struct {
		data  any
		match string
		want  bool
	}{
		{map[string]string{"id": "x", "status": "ok"}, `{"id":"x"}`, true},
		{map[string]string{"id": "x", "status": "ok"}, `{"id":"y"}`, false},
		{map[string]string{"id": "x", "status": "ok"}, `{"id":"x","status":"ok"}`, true},
		{map[string]string{"id": "x", "status": "ok"}, `{"id":"x","status":"err"}`, false},
		{map[string]string{"id": "x"}, `{}`, true}, // empty match = always true
	}
	for _, c := range cases {
		got := matchEnvelope(emit(c.data), match(c.match))
		if got != c.want {
			t.Errorf("matchEnvelope(%v, %s) = %v, want %v", c.data, c.match, got, c.want)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type testError string

func (e testError) Error() string { return string(e) }

func errForTest(msg string) error { return testError(msg) }
