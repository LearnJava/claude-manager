package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/permission"
	"claude-manager/internal/session"
)

// attnManager is a mockManager with scripted live state.
type attnManager struct {
	mockManager
	statuses  map[string]string
	questions []session.QuestionInfo
	perms     []permission.PermissionRequest
}

func (m *attnManager) GetSession(id string) (session.SessionState, bool) {
	st, ok := m.statuses[id]
	return session.SessionState{ID: id, Status: st}, ok
}
func (m *attnManager) GetPendingQuestions() []session.QuestionInfo { return m.questions }
func (m *attnManager) GetPendingPermissions() []permission.PermissionRequest {
	return m.perms
}

func waitServer(mgr ManagerAPI) (*Server, *ControlEmitter) {
	em := NewControlEmitter(50)
	return &Server{manager: mgr, app: &mockApp{}, emitter: em, token: "tok", registry: map[string]handler{}}, em
}

func doWait(srv *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWait(rr, req)
	return rr
}

func TestWait_TimeoutReportsCurrentStatus(t *testing.T) {
	srv, _ := waitServer(&attnManager{statuses: map[string]string{"p/s": "working"}})
	rr := doWait(srv, `{"event":"session:status","match":{"id":"p/s","status":"idle"},"timeout_ms":30}`)
	if rr.Code != http.StatusRequestTimeout {
		t.Fatalf("want 408, got %d", rr.Code)
	}
	var out waitTimeoutResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Reached || out.CurrentStatus != "working" || out.TimeoutMs != 30 {
		t.Fatalf("unexpected timeout body: %+v", out)
	}
}

func TestWait_ReachedFlag(t *testing.T) {
	srv, em := waitServer(&attnManager{})
	em.Emit("session:log", map[string]string{"id": "p/s"})
	rr := doWait(srv, `{"event":"session:log","timeout_ms":100}`)
	var out waitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != http.StatusOK || !out.Reached {
		t.Fatalf("want reached 200, got %d %s", rr.Code, rr.Body)
	}
}

func TestWait_Bounds(t *testing.T) {
	if MaxWaitTimeout != 10*time.Minute || DefaultWaitTimeout != 5*time.Second {
		t.Fatalf("wait bounds changed: default %v max %v", DefaultWaitTimeout, MaxWaitTimeout)
	}
	var schema struct {
		Properties map[string]struct {
			Maximum float64 `json:"maximum"`
		} `json:"properties"`
	}
	for _, name := range []string{"wait_for_status", "wait_for_event", "wait_for_worker_status", "wait_for_any"} {
		if err := json.Unmarshal(FindTool(name).InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties["timeout_ms"].Maximum; got != float64(MaxWaitTimeout.Milliseconds()) {
			t.Errorf("%s: timeout_ms maximum %v", name, got)
		}
	}
}

func TestWait_CancelReleasesSubscription(t *testing.T) {
	srv, em := waitServer(&attnManager{})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(`{"event":"session:log","timeout_ms":60000}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() { srv.handleWait(httptest.NewRecorder(), req); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for em.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if em.SubscriberCount() != 1 {
		t.Fatalf("waiter did not subscribe")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWait did not return after the request was cancelled")
	}
	if n := em.SubscriberCount(); n != 0 {
		t.Fatalf("subscription leaked: %d", n)
	}
}

func TestWaitAny_PendingStateCountsAtOnce(t *testing.T) {
	mgr := &attnManager{
		statuses:  map[string]string{"p/a": "working", "p/b": "working"},
		questions: []session.QuestionInfo{{SessionID: "p/b"}},
	}
	srv, _ := waitServer(mgr)
	rr := doWait(srv, `{"attention":["p/a","p/b"],"timeout_ms":1000}`)
	var out waitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || out.Event != session.EventNameQuestion || !strings.Contains(string(out.Data), `"p/b"`) {
		t.Fatalf("want question of p/b, got %d %s", rr.Code, rr.Body)
	}
}

func TestWaitAny_BlockedStatusCountsAtOnce(t *testing.T) {
	srv, _ := waitServer(&attnManager{statuses: map[string]string{"p/a": "waiting_permission"}})
	rr := doWait(srv, `{"attention":["p/a"],"timeout_ms":1000}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "waiting_permission") {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestWaitAny_FirstFutureEventWins(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/a": "working", "p/b": "working"}})
	go func() {
		time.Sleep(30 * time.Millisecond)
		em.Emit(session.EventNameLog, map[string]string{"id": "p/a"})                         // not attention
		em.Emit(session.EventNameStatus, map[string]string{"id": "p/a", "status": "working"}) // not attention
		em.Emit(session.EventNameQuestion, map[string]string{"id": "p/other"})                // not watched
		em.Emit(session.EventNameError, map[string]string{"id": "p/b"})                       // wins
		em.Emit(session.EventNamePermission, map[string]string{"id": "p/a"})                  // later
	}()
	rr := doWait(srv, `{"attention":["p/a","p/b"],"timeout_ms":2000}`)
	var out waitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || out.Event != session.EventNameError {
		t.Fatalf("want session:error, got %d %s", rr.Code, rr.Body)
	}
}

func TestWaitAny_IgnoresStaleRing(t *testing.T) {
	srv, em := waitServer(&attnManager{statuses: map[string]string{"p/a": "working"}})
	em.Emit(session.EventNameQuestion, map[string]string{"id": "p/a"}) // long answered
	rr := doWait(srv, `{"attention":["p/a"],"timeout_ms":40}`)
	if rr.Code != http.StatusRequestTimeout {
		t.Fatalf("a replayed event must not satisfy wait_for_any, got %d", rr.Code)
	}
	var out waitTimeoutResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Statuses["p/a"] != "working" {
		t.Fatalf("timeout must list statuses, got %+v", out)
	}
}

func TestEventNames_UniqueAndKnown(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range EventNames {
		if seen[e] || e == "" {
			t.Fatalf("bad or duplicate event name %q", e)
		}
		seen[e] = true
	}
	for _, must := range []string{"session:status", "session:question", "session:permission", "worker:done", "plan:status"} {
		if !seen[must] {
			t.Errorf("EventNames lacks %s", must)
		}
	}
}

func TestWaitForEvent_SchemaEnumIsEventNames(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(FindTool("wait_for_event").InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties["event"].Enum; strings.Join(got, ",") != strings.Join(EventNames, ",") {
		t.Fatalf("event enum %v differs from EventNames", got)
	}
}

func TestWaitResult_TimeoutShape(t *testing.T) {
	raw := map[string]any{"reached": false, "timeout_ms": float64(50), "current_status": "working"}
	out, summary, err := ShapeResult("wait_for_status", json.RawMessage(`{"session_id":"p/s","status":"idle"}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "timeout", FindTool("wait_for_status").OutputSchema, out)
	w := out.(waitOut)
	if w.Reached || w.CurrentStatus != "working" || !strings.Contains(w.Hint, "again") || !strings.Contains(summary, "Not reached") {
		t.Fatalf("unexpected: %+v / %s", w, summary)
	}
}

func TestWaitForAny_RequiresSessions(t *testing.T) {
	if _, _, err := TranslateTool("wait_for_any", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error without session_ids")
	}
	if _, _, err := TranslateTool("wait_for_any", json.RawMessage(`{"session_ids":["nope"]}`)); err == nil {
		t.Fatal("expected error for malformed id")
	}
}

func TestLongRunning_Registry(t *testing.T) {
	for name := range longRunning {
		if FindTool(name) == nil {
			t.Errorf("longRunning names unknown tool %s", name)
		}
	}
}
